package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
)

type schedulingEvaluationKey struct{}

const schedulingProbeObserverKey = "scheduling_probe_observer"

// One evaluation per group in this server process, including different models.
var schedulingRefreshes sync.Map

type SchedulingRefreshEvent struct {
	Type          string  `json:"type"`
	AccountID     int64   `json:"account_id,omitempty"`
	Name          string  `json:"name,omitempty"`
	Status        string  `json:"status,omitempty"`
	Error         string  `json:"error,omitempty"`
	FirstOutputMS float64 `json:"first_output_ms,omitempty"`
	Total         int     `json:"total,omitempty"`
	Model         string  `json:"model,omitempty"`
}

// Scheduling tests use the existing platform probes, observing content events
// rather than flushed SSE headers. Empty or unfinished responses cannot win.
func (s *AccountTestService) probeSchedulingAccount(ctx context.Context, id int64, model string, settings FastestFailoverSettings) (float64, error) {
	attempt := &fastestFailoverAttempt{
		firstTimeout: time.Duration(settings.FirstOutputTimeoutSeconds) * time.Second,
		idleTimeout:  time.Duration(settings.StreamIdleTimeoutSeconds) * time.Second,
	}
	// A total bound also stops a provider that emits an endless trickle of output.
	ctx, cancel := context.WithTimeout(ctx, attempt.firstTimeout+attempt.idleTimeout)
	defer cancel()
	ctx = attempt.bind(ctx)
	defer func() {
		attempt.mutex.Lock()
		attempt.closed = true
		attempt.timer.Stop()
		attempt.mutex.Unlock()
		attempt.cancel()
	}()
	started := time.Now()
	var latency float64
	var completed bool
	var eventError string
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = (&http.Request{}).WithContext(ctx)
	c.Set(schedulingProbeObserverKey, func(event TestEvent) {
		switch event.Type {
		case "content":
			if event.Text != "" {
				if latency == 0 {
					latency = math.Max(0.001, float64(time.Since(started).Nanoseconds())/1e6)
				}
				attempt.progress(true)
			}
		case "test_complete":
			completed = event.Success
		case "error":
			eventError = event.Error
		}
	})
	err := s.TestAccountConnection(c, id, model, "Reply with OK only.", AccountTestModeDefault)
	attempt.mutex.Lock()
	timedOut := attempt.timedOut
	attempt.mutex.Unlock()
	if timedOut {
		return 0, fmt.Errorf("probe timed out: %w", context.DeadlineExceeded)
	}
	if ctx.Err() != nil {
		return 0, fmt.Errorf("probe timed out or canceled: %w", ctx.Err())
	}
	if err != nil {
		return 0, err
	}
	if eventError != "" {
		return 0, errors.New(eventError)
	}
	if !completed || latency <= 0 {
		return 0, errors.New("probe returned no completed text response")
	}
	return latency, nil
}

var schedulingProbeTransientError = regexp.MustCompile(`(?i)(?:\breturned (?:429|5[0-9]{2}):|\brequest failed:|\bstream read error:)`)

func schedulingProbeRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var upstream *UpstreamFailoverError
	return errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &upstream) && (upstream.StatusCode == http.StatusTooManyRequests || upstream.StatusCode >= 500)) ||
		schedulingProbeTransientError.MatchString(err.Error())
}

func ValidateSchedulingProbeModel(model string) error {
	if model == "" || len(model) > 200 || strings.ContainsAny(model, "*?\r\n\x00") {
		return infraerrors.BadRequest("INVALID_PROBE_MODEL", "Choose a concrete text model")
	}
	// The account test dispatcher can route these names to paid media generation.
	for _, word := range []string{"image", "imagine", "video", "audio", "tts", "stt", "realtime", "whisper", "dall-e", "sora", "embedding"} {
		if strings.Contains(strings.ToLower(model), word) {
			return infraerrors.BadRequest("INVALID_PROBE_MODEL", "Scheduling evaluation only supports text models")
		}
	}
	return nil
}

func (s *GatewayService) groupSchedulingSelector(ctx context.Context, tests *AccountTestService, group *Group, model string) (context.Context, string, func(map[int64]struct{}) (*Account, error), error) {
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	ctx, _ = WithGatewayTokenRequestPricing(ctx)
	ctx = s.withGatewayProfitControlGate(ctx, &group.ID)
	platform, probeModel := group.Platform, model
	if platform == PlatformComposite {
		decision, ok, err := s.resolveCompositeRouteDecision(ctx, group, model, CompositeRouteEndpointAny)
		if err != nil {
			return ctx, "", nil, err
		}
		if !ok {
			return ctx, "", nil, errors.New("no route for this model")
		}
		platform, probeModel = decision.TargetPlatform, decision.UpstreamModel
		ctx = WithCompositeRouteDecision(ctx, decision)
	}
	if err := ValidateSchedulingProbeModel(probeModel); err != nil {
		return ctx, "", nil, err
	}
	if s.checkChannelPricingRestriction(ctx, &group.ID, probeModel) {
		return ctx, "", nil, errors.New("model is restricted by channel pricing")
	}
	selectAccount := func(excluded map[int64]struct{}) (*Account, error) {
		if platform == PlatformOpenAI || NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
			if tests == nil || tests.openaiGatewayService == nil {
				return nil, errors.New("OpenAI scheduler unavailable")
			}
			openai := tests.openaiGatewayService
			return openai.selectAccountForModelWithExclusions(openai.withOpenAIQuotaAutoPauseContext(ctx), &group.ID, platform, "", probeModel, excluded, false, 0, "", false)
		}
		if platform == PlatformAnthropic || platform == PlatformGemini {
			return s.selectAccountWithMixedScheduling(ctx, &group.ID, "", probeModel, excluded, platform)
		}
		return s.selectAccountForModelWithPlatform(ctx, &group.ID, "", probeModel, excluded, platform)
	}
	return ctx, probeModel, selectAccount, nil
}

// RefreshGroupScheduling evaluates the entire group, independent of UI pages or
// selected rows. No binding changes until the final eligible winner is known.
func (s *GatewayService) RefreshGroupScheduling(ctx context.Context, tests *AccountTestService, groupID int64, model string, emit func(SchedulingRefreshEvent)) error {
	if err := ValidateSchedulingProbeModel(model); err != nil {
		return err
	}
	if _, running := schedulingRefreshes.LoadOrStore(groupID, true); running {
		return infraerrors.Conflict("SCHEDULING_REFRESH_RUNNING", "This group is already being evaluated")
	}
	defer schedulingRefreshes.Delete(groupID)
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return err
	}
	if group == nil || group.AccountSchedulingMode != AccountSchedulingModeFastestFailover || group.Status != StatusActive {
		return infraerrors.BadRequest("INVALID_SCHEDULING_MODE", "Choose an active group using fastest failover scheduling")
	}
	if !group.ModelAllowlist.Allows(model) {
		return infraerrors.BadRequest("MODEL_NOT_ALLOWED", "Model is not allowed by this group")
	}
	if s.cache == nil || tests == nil {
		return errors.New("scheduling evaluation is unavailable")
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
	if err != nil {
		return err
	}
	quality := make(map[int64]GroupModelAccountQuality)
	failed := make([]*Account, 0)
	ctx = context.WithValue(ctx, schedulingEvaluationKey{}, quality)
	ctx, probeModel, selectAccount, err := s.groupSchedulingSelector(ctx, tests, group, model)
	if err != nil {
		return err
	}
	history := groupModelAccountQuality(context.WithValue(ctx, schedulingEvaluationKey{}, struct{}{}), s.usageLogRepo, &groupID, probeModel)
	settings := s.settingService.FastestFailoverSettings(ctx)
	emit(SchedulingRefreshEvent{Type: "start", Total: len(accounts), Model: model})
	// ponytail: an explicit admin action reuses the live selector per account
	// (O(n²)); extract its candidate filter if very large groups need batching.
	for i := range accounts {
		if err := ctx.Err(); err != nil {
			return err
		}
		account := &accounts[i]
		event := SchedulingRefreshEvent{Type: "result", AccountID: account.ID, Name: account.Name}
		excluded := make(map[int64]struct{}, len(accounts))
		for _, other := range accounts {
			if other.ID != account.ID {
				excluded[other.ID] = struct{}{}
			}
		}
		selected, selectionErr := selectAccount(excluded)
		if account.IsSyntheticUITest() || selectionErr != nil || selected == nil || selected.ID != account.ID {
			event.Status, event.Error = "skipped", "not currently eligible for this model (availability, quota, platform or model restrictions)"
			emit(event)
			continue
		}
		// Refresh membership and model mapping immediately before a paid call.
		fresh, freshErr := s.accountRepo.GetByID(ctx, account.ID)
		if freshErr != nil {
			return freshErr
		}
		if !s.isAccountInGroup(fresh, &groupID) || !fresh.IsSchedulableForModelWithContext(ctx, probeModel) || fresh.IsSyntheticUITest() || !s.isModelSupportedByAccountWithContext(ctx, fresh, probeModel) {
			event.Status, event.Error = "skipped", "account availability or group membership changed"
			emit(event)
			continue
		}
		if err := ValidateSchedulingProbeModel(fresh.GetMappedModel(probeModel)); err != nil {
			event.Status, event.Error = "skipped", err.Error()
			emit(event)
			continue
		}
		slot, slotErr := s.tryAcquireAccountSlot(ctx, account.ID, fresh.Concurrency)
		if slotErr != nil || slot == nil || !slot.Acquired {
			event.Status, event.Error = "skipped", "account concurrency is full or unavailable"
			emit(event)
			continue
		}
		emit(SchedulingRefreshEvent{Type: "testing", AccountID: account.ID, Name: account.Name})
		latency, probeErr := tests.probeSchedulingAccount(ctx, account.ID, probeModel, settings)
		if ctx.Err() == nil && schedulingProbeRetryable(probeErr) {
			emit(SchedulingRefreshEvent{Type: "testing", AccountID: account.ID, Name: account.Name, Status: "retrying"})
			latency, probeErr = tests.probeSchedulingAccount(ctx, account.ID, probeModel, settings)
		}
		slot.ReleaseFunc()
		if err := ctx.Err(); err != nil {
			return err
		}
		if probeErr != nil {
			event.Status, event.Error = "failed", probeErr.Error()
			failed = append(failed, fresh)
		} else {
			event.Status, event.FirstOutputMS = "success", latency
			sample := history[account.ID]
			sample.ProbeSucceeded = true
			if sample.LatencyMS <= 0 {
				sample.LatencyMS = latency
			}
			quality[account.ID] = sample
		}
		emit(event)
	}
	if len(quality) == 0 {
		return errors.New("no successful eligible account; previous scheduling binding retained")
	}
	// Exclude failures and any accounts newly added during the evaluation.
	latest, err := s.accountRepo.ListByGroup(ctx, groupID)
	if err != nil {
		return err
	}
	excluded := make(map[int64]struct{})
	for _, account := range latest {
		if _, ok := quality[account.ID]; !ok {
			excluded[account.ID] = struct{}{}
		}
	}
	for _, account := range accounts {
		if _, ok := quality[account.ID]; !ok {
			excluded[account.ID] = struct{}{}
		}
	}
	winner, err := selectAccount(excluded)
	if err != nil {
		return err
	}
	if _, tested := quality[winner.ID]; !tested {
		return errors.New("selected account was not tested")
	}
	group, err = s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return err
	}
	fresh, err := s.accountRepo.GetByID(ctx, winner.ID)
	if err != nil {
		return err
	}
	if group.AccountSchedulingMode != AccountSchedulingModeFastestFailover || group.Status != StatusActive || !group.ModelAllowlist.Allows(model) || !s.isAccountInGroup(fresh, &groupID) || !fresh.IsSchedulableForModelWithContext(ctx, probeModel) {
		return errors.New("group or account changed during evaluation; previous binding retained")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Failed probes enter the existing model cooldown without affecting other models.
	cooldown := time.Duration(settings.ModelCooldownSeconds) * time.Second
	for _, account := range failed {
		if account.GetModelRateLimitRemainingTimeWithContext(ctx, probeModel) >= cooldown {
			continue
		}
		key := modelRateLimitKeyForUpstreamModelNotFound(ctx, account, probeModel)
		if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, key, time.Now().Add(cooldown), "scheduling_probe_failed"); err != nil {
			return err
		}
	}
	if err := s.cache.SetSessionAccountID(ctx, groupID, groupModelSchedulingStickyKey(probeModel), winner.ID, groupModelSchedulingStickyTTL); err != nil {
		return err
	}
	emit(SchedulingRefreshEvent{Type: "complete", AccountID: winner.ID, Name: winner.Name, Model: model, FirstOutputMS: quality[winner.ID].LatencyMS})
	return nil
}
