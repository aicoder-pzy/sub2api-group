package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const fastestFailoverFirstOutputTimeout = 60 * time.Second
const fastestFailoverStreamIdleTimeout = 120 * time.Second
const fastestFailoverTimeoutCooldown = 2 * time.Minute

type fastestFailoverAttemptKey struct{}

type fastestFailoverAttempt struct {
	mutex         sync.Mutex
	timer         *time.Timer
	deadline      time.Time
	cancel        context.CancelFunc
	body          io.ReadCloser
	firstTimeout  time.Duration
	idleTimeout   time.Duration
	outputStarted bool
	timedOut      bool
	closed        bool
}

func fastestFailoverAttemptFromContext(ctx context.Context) *fastestFailoverAttempt {
	attempt, _ := ctx.Value(fastestFailoverAttemptKey{}).(*fastestFailoverAttempt)
	return attempt
}

func (s *OpenAIGatewayService) beginFastestFailoverAttempt(ctx context.Context, account *Account) (context.Context, *fastestFailoverAttempt) {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || group.AccountSchedulingMode != AccountSchedulingModeFastestFailover || account == nil || account.Platform != PlatformOpenAI || fastestFailoverAttemptFromContext(ctx) != nil {
		return ctx, nil
	}
	attempt := &fastestFailoverAttempt{firstTimeout: fastestFailoverFirstOutputTimeout, idleTimeout: fastestFailoverStreamIdleTimeout}
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
		attempt.timer = time.AfterFunc(attempt.firstTimeout, attempt.expire)
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
	}
}

func markFastestFailoverOutput(ctx context.Context) {
	if attempt := fastestFailoverAttemptFromContext(ctx); attempt != nil {
		attempt.progress(true)
	}
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

func (s *OpenAIGatewayService) finishFastestFailoverAttempt(ctx context.Context, c *gin.Context, account *Account, body []byte, attempt *fastestFailoverAttempt, forwardErr error) error {
	if attempt == nil {
		return forwardErr
	}
	attempt.mutex.Lock()
	attempt.closed = true
	if attempt.timer != nil {
		attempt.timer.Stop()
	}
	timedOut, outputStarted, cancel := attempt.timedOut, attempt.outputStarted, attempt.cancel
	attempt.mutex.Unlock()
	if cancel != nil {
		cancel()
	}
	var failoverErr *UpstreamFailoverError
	upstreamTimeout := errors.As(forwardErr, &failoverErr) && (failoverErr.StatusCode == http.StatusGatewayTimeout || failoverErr.StatusCode == http.StatusRequestTimeout)
	if !timedOut && !(ctx.Err() == nil && (upstreamTimeout || errors.Is(forwardErr, context.DeadlineExceeded))) {
		return forwardErr
	}
	model := gjson.GetBytes(body, "model").String()
	modelKey := modelRateLimitKeyForUpstreamModelNotFound(ctx, account, model)
	if s.accountRepo != nil && modelKey != "" && account.GetModelRateLimitRemainingTimeWithContext(ctx, model) < fastestFailoverTimeoutCooldown {
		updateCtx, release := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		err := s.accountRepo.SetModelRateLimit(updateCtx, account.ID, modelKey, time.Now().Add(fastestFailoverTimeoutCooldown), "fastest_failover_timeout")
		release()
		if err != nil {
			logger.LegacyPrintf("service.openai_gateway", "fastest failover timeout cooldown failed: account=%d model=%s error=%v", account.ID, modelKey, err)
		}
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, Kind: "fastest_failover_timeout",
		Message: "Upstream response timed out; account model cooling down",
	})
	logger.LegacyPrintf("service.openai_gateway", "fastest failover timeout: account=%d model=%s output_started=%t cooldown=%s", account.ID, modelKey, outputStarted, fastestFailoverTimeoutCooldown)
	return &UpstreamFailoverError{
		StatusCode:   http.StatusGatewayTimeout,
		ResponseBody: []byte(`{"error":{"type":"upstream_timeout","message":"Upstream response timed out"}}`),
	}
}
