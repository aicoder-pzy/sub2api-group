package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const fastestFailoverTimeoutCooldown = 2 * time.Minute

type fastestFailoverAttemptKey struct{}

type fastestFailoverAttempt struct {
	mutex          sync.Mutex
	timer          *time.Timer
	deadline       time.Time
	cancel         context.CancelFunc
	body           io.ReadCloser
	firstTimeout   time.Duration
	cooldown       time.Duration
	idleTimeout    time.Duration
	outputStarted  bool
	timedOut       bool
	closed         bool
	state          *groupModelSchedulingRequestState
	totalBudget    time.Duration
	maxSubmissions int
	budgetLimited  bool
}

func fastestFailoverAttemptFromContext(ctx context.Context) *fastestFailoverAttempt {
	attempt, _ := ctx.Value(fastestFailoverAttemptKey{}).(*fastestFailoverAttempt)
	return attempt
}

func beginFastestFailoverAttempt(ctx context.Context, account *Account, settingsService ...*SettingService) (context.Context, *fastestFailoverAttempt) {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || group.AccountSchedulingMode != AccountSchedulingModeFastestFailover || account == nil || fastestFailoverAttemptFromContext(ctx) != nil {
		return ctx, nil
	}
	settings := DefaultFastestFailoverSettings()
	if len(settingsService) > 0 {
		settings = settingsService[0].FastestFailoverSettings(ctx)
	}
	ctx = WithFastestFailoverRequestState(ctx)
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	maxSubmissions := 2
	if account.IsPoolMode() && account.GetPoolModeRetryCount() == 0 {
		maxSubmissions = 1
	}
	attempt := &fastestFailoverAttempt{
		firstTimeout: time.Duration(settings.FirstOutputTimeoutSeconds) * time.Second,
		idleTimeout:  time.Duration(settings.StreamIdleTimeoutSeconds) * time.Second,
		cooldown:     time.Duration(settings.ModelCooldownSeconds) * time.Second,
		state:        state, totalBudget: time.Duration(settings.TotalAttemptBudgetSeconds) * time.Second,
		maxSubmissions: maxSubmissions,
	}
	return context.WithValue(ctx, fastestFailoverAttemptKey{}, attempt), attempt
}

func (attempt *fastestFailoverAttempt) bind(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	if attempt.closed || attempt.timedOut {
		cancel()
		return ctx
	}
	if attempt.cancel != nil {
		attempt.cancel()
	}
	attempt.body = nil
	attempt.cancel = cancel
	if attempt.timer == nil {
		attempt.deadline = time.Now().Add(attempt.firstTimeout)
		if attempt.state != nil {
			attempt.state.mu.Lock()
			if attempt.state.deadline.IsZero() {
				attempt.state.deadline = time.Now().Add(attempt.totalBudget)
			}
			if attempt.state.deadline.Before(attempt.deadline) {
				attempt.deadline = attempt.state.deadline
				attempt.budgetLimited = true
			}
			attempt.state.mu.Unlock()
		}
		attempt.timer = time.AfterFunc(time.Until(attempt.deadline), attempt.expire)
	}
	return ctx
}

func (attempt *fastestFailoverAttempt) expire() {
	attempt.mutex.Lock()
	if attempt.closed || attempt.timedOut {
		attempt.mutex.Unlock()
		return
	}
	if remaining := time.Until(attempt.deadline); remaining > 0 {
		attempt.timer.Reset(remaining)
		attempt.mutex.Unlock()
		return
	}
	attempt.timedOut = true
	cancel, body := attempt.cancel, attempt.body
	attempt.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	if body != nil {
		_ = body.Close()
	}
}

func (attempt *fastestFailoverAttempt) progress(output bool) {
	attempt.mutex.Lock()
	defer attempt.mutex.Unlock()
	if attempt.closed || attempt.timedOut {
		return
	}
	attempt.outputStarted = attempt.outputStarted || output
	if attempt.outputStarted {
		attempt.deadline = time.Now().Add(attempt.idleTimeout)
		attempt.budgetLimited = false
	}
}

func markFastestFailoverOutput(ctx context.Context) {
	if attempt := fastestFailoverAttemptFromContext(ctx); attempt != nil {
		attempt.progress(true)
	}
}

func fastestFailoverTotalBudgetExpired(ctx context.Context) bool {
	if attempt := fastestFailoverAttemptFromContext(ctx); attempt != nil {
		attempt.mutex.Lock()
		defer attempt.mutex.Unlock()
		return attempt.timedOut && attempt.budgetLimited && !attempt.outputStarted
	}
	return false
}

func markFastestFailoverResponseOutput(resp *http.Response) {
	if attempt := fastestFailoverAttemptFromResponse(resp); attempt != nil {
		attempt.progress(true)
	}
}

func fastestFailoverAttemptFromResponse(resp *http.Response) *fastestFailoverAttempt {
	if resp == nil || resp.Request == nil {
		return nil
	}
	return fastestFailoverAttemptFromContext(resp.Request.Context())
}

func fastestFailoverChatChunkStartsOutput(chunk *apicompat.ChatCompletionsChunk) bool {
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil || len(choice.Delta.ToolCalls) > 0 ||
			(choice.Delta.Content != nil && *choice.Delta.Content != "") ||
			(choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "") {
			return true
		}
	}
	return false
}

func (attempt *fastestFailoverAttempt) wrapBody(body io.ReadCloser) io.ReadCloser {
	attempt.mutex.Lock()
	attempt.body = body
	timedOut := attempt.timedOut
	attempt.mutex.Unlock()
	if timedOut {
		_ = body.Close()
	}
	return &fastestFailoverReadCloser{ReadCloser: body, attempt: attempt}
}

type fastestFailoverReadCloser struct {
	io.ReadCloser
	attempt *fastestFailoverAttempt
}

func (body *fastestFailoverReadCloser) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	body.attempt.mutex.Lock()
	timedOut := body.attempt.timedOut
	body.attempt.mutex.Unlock()
	if timedOut {
		return 0, context.DeadlineExceeded
	}
	if count > 0 {
		body.attempt.progress(false)
	}
	return count, err
}

func finishFastestFailoverAttempt(ctx context.Context, repo AccountRepository, c *gin.Context, account *Account, body []byte, attempt *fastestFailoverAttempt, forwardErr error) error {
	forwardErr = limitFastestFailoverRetry(ctx, forwardErr, 1, account.ID)
	if attempt == nil {
		return forwardErr
	}
	attempt.mutex.Lock()
	attempt.closed = true
	if attempt.timer != nil {
		attempt.timer.Stop()
	}
	timedOut, outputStarted, cancel := attempt.timedOut, attempt.outputStarted, attempt.cancel
	budgetLimited := attempt.budgetLimited
	attempt.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	if timedOut && budgetLimited {
		forwardErr = fastestFailoverBudgetError(true)
		if c != nil {
			if value, ok := c.Get(OpsUpstreamErrorsKey); ok {
				if events, ok := value.([]*OpsUpstreamErrorEvent); ok && len(events) > 0 {
					event := events[len(events)-1]
					if event != nil && event.AccountID == account.ID && (event.UpstreamStatusCode == 0 || event.UpstreamStatusCode == http.StatusGatewayTimeout) {
						event.Scope, event.Reason = "request", "fastest_failover_budget_exhausted"
					}
				}
			}
		}
	}
	forwardErr = classifyFastestFailoverRequestError(ctx, c, account, forwardErr)
	var failoverErr *UpstreamFailoverError
	if errors.As(forwardErr, &failoverErr) && (failoverErr.Reason == "fastest_failover_budget_exhausted" || failoverErr.Reason == "fastest_failover_retry_limit" || failoverErr.Reason == "fastest_failover_shared_failure_limit") {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			Kind: string(failoverErr.Reason), Scope: "request", Reason: string(failoverErr.Reason), Message: "Request retry budget reached"})
		return forwardErr
	}
	upstreamTimeout := errors.As(forwardErr, &failoverErr) && (failoverErr.StatusCode == http.StatusGatewayTimeout || failoverErr.StatusCode == http.StatusRequestTimeout)
	if !timedOut && (ctx.Err() != nil || (!upstreamTimeout && !errors.Is(forwardErr, context.DeadlineExceeded))) {
		return forwardErr
	}
	cooldown := attempt.cooldown
	if cooldown <= 0 {
		cooldown = fastestFailoverTimeoutCooldown
	}
	model := gjson.GetBytes(body, "model").String()
	modelKey := modelRateLimitKeyForUpstreamModelNotFound(ctx, account, model)
	if repo != nil && modelKey != "" && account.GetModelRateLimitRemainingTimeWithContext(ctx, model) < cooldown {
		updateCtx, release := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		err := repo.SetModelRateLimit(updateCtx, account.ID, modelKey, time.Now().Add(cooldown), "fastest_failover_timeout")
		release()
		if err != nil {
			logger.LegacyPrintf("service.account_scheduling", "fastest failover timeout cooldown failed: account=%d model=%s error=%v", account.ID, modelKey, err)
		}
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, Kind: "fastest_failover_timeout",
		Message: "Upstream response timed out; account model cooling down",
	})
	logger.LegacyPrintf("service.account_scheduling", "fastest failover timeout: account=%d model=%s output_started=%t cooldown=%s", account.ID, modelKey, outputStarted, cooldown)
	return &UpstreamFailoverError{
		StatusCode:   http.StatusGatewayTimeout,
		ResponseBody: []byte(`{"error":{"type":"upstream_timeout","message":"Upstream response timed out"}}`),
	}
}

// Bind after streaming contexts have been detached, so the attempt still cancels
// both header waits and body reads. Retries share the original first-output deadline.
func doFastestFailoverUpstream(upstream HTTPUpstream, request *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile, senders ...func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if err := reserveFastestFailoverSubmission(request.Context(), accountID); err != nil {
		return nil, err
	}
	attempt := fastestFailoverAttemptFromContext(request.Context())
	if attempt != nil {
		request = request.WithContext(attempt.bind(request.Context()))
	}
	var response *http.Response
	var err error
	if len(senders) > 0 {
		response, err = senders[0](request)
	} else {
		response, err = upstream.DoWithTLS(request, proxyURL, accountID, concurrency, profile)
	}
	if attempt != nil && response != nil && response.Body != nil {
		if response.Request == nil {
			response.Request = request
		}
		response.Body = attempt.wrapBody(response.Body)
	}
	return response, err
}

func fastestFailoverBudgetError(total bool) *UpstreamFailoverError {
	err := &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable, Scope: GatewayFailureScopeRequest,
		RequestScopedTransient: true, Reason: "fastest_failover_retry_limit",
		ResponseBody: []byte(`{"error":{"type":"upstream_error","message":"Channel retry limit reached"}}`),
	}
	if total {
		err.StatusCode, err.NextAccountAction, err.Reason = http.StatusGatewayTimeout, NextAccountStop, "fastest_failover_budget_exhausted"
		err.ResponseBody = []byte(`{"error":{"type":"upstream_timeout","message":"Failover time budget exhausted"}}`)
	}
	return err
}

func reserveFastestFailoverSubmission(ctx context.Context, accountID int64) error {
	attempt := fastestFailoverAttemptFromContext(ctx)
	if attempt == nil || attempt.state == nil {
		return nil
	}
	state := attempt.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.deadline.IsZero() && !time.Now().Before(state.deadline) {
		return fastestFailoverBudgetError(true)
	}
	if state.submissions == nil {
		state.submissions = make(map[int64]int)
	}
	if state.sharedFailureOrigin > 0 && state.sharedFailureOrigin != accountID {
		if (state.sharedAlternateID > 0 && state.sharedAlternateID != accountID) || state.submissions[accountID] > 0 {
			err := fastestFailoverBudgetError(false)
			err.NextAccountAction, err.Reason = NextAccountStop, "fastest_failover_shared_failure_limit"
			return err
		}
		state.sharedAlternateID = accountID
	}
	if state.submissions[accountID] >= attempt.maxSubmissions {
		return fastestFailoverBudgetError(false)
	}
	state.submissions[accountID]++
	return nil
}

func isFastestFailoverUnknownGrokForbidden(ctx context.Context, account *Account, status int, body []byte) bool {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || !fastestFailoverEnabled(ctx, &group.ID) || account == nil || !account.IsGrok() || status != http.StatusForbidden {
		return false
	}
	if len(matchTempUnschedulableRules(account, status, body)) > 0 || isGrokContentPolicyRejection(status, body) || grokAccountAccessMessage(string(body)) {
		return false
	}
	if classifyGrokUpstreamFailure(status, body, "").Class != GrokFailureNone {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"invalid api key", "invalid_api_key", "invalid token", "invalid_token", "expired token", "token expired", "token has expired", "authentication failed", "unauthenticated", "credentials expired", "api key expired"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	for _, marker := range []string{"insufficient credits", "insufficient balance", "billing", "payment required", "quota exceeded", "quota_exceeded", "quota exhausted", "usage limit", "rate limit", "rate_limit"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	var payload any
	if json.Unmarshal(body, &payload) == nil && grokStructuredAccountAccessMarker(payload) {
		return false
	}
	return true
}

func classifyFastestFailoverRequestError(ctx context.Context, c *gin.Context, account *Account, err error) error {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	var failoverErr *UpstreamFailoverError
	if state == nil || !errors.As(err, &failoverErr) {
		return err
	}
	limited := *failoverErr
	unknown := !failoverErr.IsCredentialFailure() && isFastestFailoverUnknownGrokForbidden(ctx, account, failoverErr.StatusCode, failoverErr.ResponseBody)
	state.mu.Lock()
	if unknown && state.sharedFailureOrigin == 0 {
		state.sharedFailureOrigin = account.ID
	}
	stop := state.sharedFailureOrigin > 0 && state.sharedFailureOrigin != account.ID
	state.mu.Unlock()
	if unknown {
		limited.Scope, limited.Reason, limited.RequestScopedTransient = GatewayFailureScopeRequest, "grok_unknown_forbidden", true
		limited.RetryableOnSameAccount = false
		if c != nil {
			if value, ok := c.Get(OpsUpstreamErrorsKey); ok {
				if events, ok := value.([]*OpsUpstreamErrorEvent); ok {
					for _, event := range events {
						if event != nil && event.AccountID == account.ID && event.UpstreamStatusCode == http.StatusForbidden {
							event.Scope, event.Reason = "request", "grok_unknown_forbidden"
						}
					}
				}
			}
		}
	}
	if stop {
		limited.NextAccountAction, limited.RetryableOnSameAccount = NextAccountStop, false
	}
	return &limited
}

// Hold protocol preludes until meaningful output, keeping the handler's normal
// written-byte guard usable. Usage is still parsed immediately by the caller.
// This writer is local to a stream; it never replaces Gin's response writer.
type fastestFailoverStreamWriter struct {
	writer  gin.ResponseWriter
	attempt *fastestFailoverAttempt
	pending bytes.Buffer
	started bool
	err     error
}

func newFastestFailoverStreamWriter(writer gin.ResponseWriter, resp *http.Response) *fastestFailoverStreamWriter {
	attempt := fastestFailoverAttemptFromResponse(resp)
	return &fastestFailoverStreamWriter{writer: writer, attempt: attempt, started: attempt == nil}
}

func (w *fastestFailoverStreamWriter) observeGemini(data []byte) {
	if w.attempt == nil || w.started {
		return
	}
	event := gjson.ParseBytes(data)
	starts := event.Get("error").Exists() || event.Get("promptFeedback.blockReason").String() != ""
	for _, candidate := range event.Get("candidates").Array() {
		if candidate.Get("finishReason").String() != "" {
			starts = true
		}
		for _, part := range candidate.Get("content.parts").Array() {
			if part.Get("text").String() != "" || part.Get("functionCall").Exists() || part.Get("inlineData").Exists() || part.Get("fileData").Exists() {
				starts = true
			}
		}
	}
	if starts {
		w.started = true
		w.attempt.progress(true)
	}
}

func (w *fastestFailoverStreamWriter) observeAnthropic(data string) {
	if w.attempt != nil && !w.started && anthropicEventStartsOutput(data) {
		w.started = true
		w.attempt.progress(true)
	}
}

func (w *fastestFailoverStreamWriter) Write(data []byte) (int, error) {
	if w.attempt != nil {
		w.attempt.mutex.Lock()
		timedOut := w.attempt.timedOut
		w.attempt.mutex.Unlock()
		if timedOut {
			return 0, context.DeadlineExceeded
		}
	}
	if !w.started {
		// Bound memory even if a broken upstream floods empty events.
		if w.pending.Len()+len(data) > 1024*1024 {
			w.err = w.incompletePreludeError()
			return 0, w.err
		}
		return w.pending.Write(data)
	}
	if w.pending.Len() > 0 {
		if _, err := w.pending.WriteTo(w.writer); err != nil {
			return 0, err
		}
	}
	return w.writer.Write(data)
}

func (w *fastestFailoverStreamWriter) Flush() {
	if w.started {
		w.writer.Flush()
	}
}

func (w *fastestFailoverStreamWriter) incompletePreludeError() error {
	if w.started {
		return nil
	}
	return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: gatewayTransportFailoverBody}
}

func anthropicEventStartsOutput(data string) bool {
	data = strings.TrimSpace(data)
	if data == "" {
		return false
	}
	if data == "[DONE]" {
		return true
	}
	event := gjson.Parse(data)
	switch event.Get("type").String() {
	case "ping":
		return false
	case "message_start":
		return len(event.Get("message.content").Array()) > 0
	case "content_block_start":
		block := event.Get("content_block")
		switch block.Get("type").String() {
		case "text":
			return block.Get("text").String() != ""
		case "thinking":
			return block.Get("thinking").String() != "" || block.Get("signature").String() != ""
		default:
			return true // Tool starts and unknown content must not be replayed.
		}
	case "content_block_delta":
		delta := event.Get("delta")
		switch delta.Get("type").String() {
		case "text_delta":
			return delta.Get("text").String() != ""
		case "thinking_delta":
			return delta.Get("thinking").String() != ""
		case "signature_delta":
			return delta.Get("signature").String() != ""
		case "input_json_delta":
			return delta.Get("partial_json").String() != ""
		default:
			return true
		}
	case "content_block_stop":
		return false
	case "message_delta":
		return event.Get("delta.stop_reason").String() != ""
	default:
		return true // Includes terminal events and errors; never discard them.
	}
}
