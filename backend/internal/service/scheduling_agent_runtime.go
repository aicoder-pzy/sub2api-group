package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const schedulingAgentConnectionKey = "scheduling_agent_connection"
const schedulingAgentBudgetKey = "scheduling_agent_budget"
const schedulingAgentOutputLimit = 800

type SchedulingAgentConnection struct {
	Version         int64  `json:"version"`
	URL             string `json:"url"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
	APIKey          string `json:"api_key,omitempty"`
	KeyConfigured   bool   `json:"key_configured"`
	DailyCalls      int    `json:"daily_calls"`
	IntervalMinutes int    `json:"interval_minutes"`
	ClearKey        bool   `json:"clear_key,omitempty"`
}

type schedulingAgentDecision struct {
	Action    string `json:"action"`
	Model     string `json:"model"`
	AccountID int64  `json:"account_id"`
	WaitMS    int    `json:"wait_ms"`
	Reason    string `json:"reason"`
}

type SchedulingAgentRun struct {
	At                time.Time                 `json:"at"`
	Version           int64                     `json:"version"`
	ConnectionVersion int64                     `json:"connection_version"`
	AppliedRequests   int                       `json:"applied_requests"`
	CompletedAttempts int                       `json:"completed_attempts"`
	FailedAttempts    int                       `json:"failed_attempts"`
	History           []SchedulingAgentRun      `json:"history,omitempty"`
	Day               string                    `json:"day"`
	Calls             int                       `json:"calls"`
	State             string                    `json:"state"`
	Decision          schedulingAgentDecision   `json:"decision"`
	Expires           time.Time                 `json:"expires"`
	InputTokens       int                       `json:"input_tokens"`
	OutputTokens      int                       `json:"output_tokens"`
	Evidence          []schedulingAgentEvidence `json:"evidence,omitempty"`
	Feedback          []schedulingAgentEvidence `json:"feedback,omitempty"`
}

type schedulingAgentCacheReader interface {
	GetSchedulingAgentCacheRatios(context.Context, int64, string, []int64) (map[int64]*float64, error)
}

type schedulingAgentEvidence struct {
	Model          string                `json:"model"`
	AccountID      int64                 `json:"account_id"`
	Successes      int64                 `json:"successes"`
	Failures       int64                 `json:"failures"`
	RecentFailures int64                 `json:"recent_failures"`
	FirstOutputMS  float64               `json:"first_event_ms"`
	CacheRatio     *float64              `json:"cache_ratio,omitempty"`
	Speed          *ModelAutoSpeedBucket `json:"speed,omitempty"`
	QualityTier    string                `json:"quality_tier,omitempty"`
	FullSlots      int                   `json:"full_slots"`
	WaitedMS       int64                 `json:"waited_ms"`
}

type schedulingAgentRunEntry struct {
	run   SchedulingAgentRun
	until time.Time
}
type schedulingQueueKey struct {
	group   int64
	model   string
	account int64
}
type schedulingQueueSample struct {
	started time.Time
	at      time.Time
	full    int
	waited  int64
}
type schedulingActionCount struct {
	at        time.Time
	count     int
	completed int
	failed    int
}

type schedulingAgentRuntime struct {
	done         chan struct{}
	settings     *SettingService
	groups       GroupRepository
	accounts     AccountRepository
	observations modelAutoQualityRepository
	secrets      SecretEncryptor
	lock         LeaderLockCache
	client       *http.Client
	mu           sync.Mutex
	queue        map[schedulingQueueKey]schedulingQueueSample
	actions      map[int64]schedulingActionCount
	cancel       context.CancelFunc
}

func (s *SettingService) startSchedulingAgent(groups GroupRepository, accounts AccountRepository, usage UsageLogRepository, secrets SecretEncryptor, lock LeaderLockCache) {
	ctx, cancel := context.WithCancel(context.Background())
	observations, _ := usage.(modelAutoQualityRepository)
	runtime := &schedulingAgentRuntime{settings: s, groups: groups, accounts: accounts, observations: observations, secrets: secrets, lock: lock, cancel: cancel, queue: make(map[schedulingQueueKey]schedulingQueueSample), actions: make(map[int64]schedulingActionCount), client: newSchedulingAgentClient(), done: make(chan struct{})}
	s.schedulingAgent = runtime
	go runtime.loop(ctx)
}

func (s *SettingService) StopSchedulingAgent() {
	if r := s.schedulingAgent; r != nil {
		r.cancel()
		<-r.done
		r.client.CloseIdleConnections()
	}
}

func (s *SettingService) TestSchedulingAgentConnection(ctx context.Context) (SchedulingAgentRun, error) {
	r := s.schedulingAgent
	if r == nil {
		return SchedulingAgentRun{}, infraerrors.ServiceUnavailable("SCHEDULING_AGENT", "Scheduling agent unavailable")
	}
	c, err := readSchedulingJSON[SchedulingAgentConnection](ctx, s.settingRepo, schedulingAgentConnectionKey)
	if err != nil {
		return SchedulingAgentRun{}, err
	}
	if c.APIKey == "" {
		return SchedulingAgentRun{}, infraerrors.BadRequest("SCHEDULING_AGENT_KEY", "Save a model key first")
	}
	release, ok := r.acquireCallLock(ctx)
	if !ok {
		return SchedulingAgentRun{}, infraerrors.Conflict("SCHEDULING_AGENT_BUSY", "A scheduling analysis is running")
	}
	defer release()
	if ok, err := r.reserveCall(ctx, c.DailyCalls); err != nil {
		return SchedulingAgentRun{}, err
	} else if !ok {
		return SchedulingAgentRun{}, infraerrors.BadRequest("SCHEDULING_AGENT_BUDGET", "Daily call budget exhausted")
	}
	decision, in, out, err := r.call(ctx, c, []schedulingAgentEvidence{}, 500)
	result := SchedulingAgentRun{At: time.Now(), State: "connected", Decision: decision, InputTokens: in, OutputTokens: out}
	if err != nil {
		result.State = err.Error()
	}
	return result, nil
}

func newSchedulingAgentClient() *http.Client {
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	transport := &http.Transport{TLSHandshakeTimeout: 4 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 4}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !schedulingPublicIP(ip) {
				return nil, errors.New("assistant address is not public")
			}
		}
		for _, ip := range ips {
			if conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port)); err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("assistant endpoint unreachable")
	}
	return &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func schedulingPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return false
	}
	for _, prefix := range schedulingBlockedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var schedulingBlockedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16"),
}

func (s *SettingService) GetSchedulingAgentConnection(ctx context.Context) (SchedulingAgentConnection, error) {
	c, err := readSchedulingJSON[SchedulingAgentConnection](ctx, s.settingRepo, schedulingAgentConnectionKey)
	c.KeyConfigured = c.APIKey != ""
	c.APIKey = ""
	return c, err
}

func (s *SettingService) SetSchedulingAgentConnection(ctx context.Context, input SchedulingAgentConnection) error {
	runtime := s.schedulingAgent
	if runtime == nil {
		return errors.New("scheduling agent service unavailable")
	}
	endpoint, err := urlvalidator.ValidateHTTPSURL(input.URL, urlvalidator.ValidationOptions{})
	parsed, parseErr := url.Parse(endpoint)
	if err != nil || parseErr != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return infraerrors.BadRequest("SCHEDULING_AGENT_URL", "Set a public HTTPS base URL without credentials or query parameters")
	}
	input.URL = endpoint
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" || len(input.Model) > 160 || len(input.Effort) > 20 || strings.ContainsAny(input.Effort, " \r\n\t") || input.DailyCalls < 1 || input.DailyCalls > 500 || input.IntervalMinutes < 1 || input.IntervalMinutes > 1440 || len(input.APIKey) > 4096 {
		return infraerrors.BadRequest("SCHEDULING_AGENT_CONFIG", "Invalid model, thinking level, interval or daily budget")
	}
	previous, err := readSchedulingJSON[SchedulingAgentConnection](ctx, s.settingRepo, schedulingAgentConnectionKey)
	if err != nil {
		return err
	}
	switch {
	case input.ClearKey:
		input.APIKey = ""
	case strings.TrimSpace(input.APIKey) != "":
		input.APIKey, err = runtime.secrets.Encrypt(strings.TrimSpace(input.APIKey))
		if err != nil {
			return err
		}
	case previous.URL == input.URL:
		input.APIKey = previous.APIKey
	}
	input.Version = time.Now().UnixNano()
	input.ClearKey = false
	input.KeyConfigured = false
	if err := writeSchedulingJSON(ctx, s.settingRepo, schedulingAgentConnectionKey, input); err != nil {
		return err
	}
	s.schedulingIndex.mu.Lock()
	clear(s.schedulingIndex.runs)
	s.schedulingIndex.mu.Unlock()
	return nil
}

func (s *SettingService) GetSchedulingAgentRun(ctx context.Context, id int64) (SchedulingAgentRun, error) {
	return readSchedulingJSON[SchedulingAgentRun](ctx, s.settingRepo, schedulingAgentRunKey(id))
}

func (s *SettingService) RevokeSchedulingAgentRun(ctx context.Context, id int64) error {
	policy, err := s.GetSchedulingPolicy(ctx, id)
	if err != nil {
		return err
	}
	return s.SetSchedulingPolicy(ctx, id, policy)
}

func (r *schedulingAgentRuntime) loop(ctx context.Context) {
	defer close(r.done)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.cycle(ctx)
		}
	}
}

func (r *schedulingAgentRuntime) cycle(ctx context.Context) {
	config, err := readSchedulingJSON[SchedulingAgentConnection](ctx, r.settings.settingRepo, schedulingAgentConnectionKey)
	if err != nil || config.APIKey == "" || r.observations == nil || r.lock == nil {
		return
	}
	groups, err := r.groups.ListActive(ctx)
	if err != nil {
		return
	}
	for _, g := range groups {
		if ctx.Err() != nil {
			return
		}
		if g.AccountSchedulingMode != AccountSchedulingModeSmartChannel {
			continue
		}
		policy := r.settings.schedulingPolicy(ctx, g.ID)
		if policy.AgentMode != "observe" && policy.AgentMode != "auto" {
			continue
		}
		run, err := r.settings.GetSchedulingAgentRun(ctx, g.ID)
		if err != nil {
			continue
		}
		if time.Since(run.At) < time.Duration(config.IntervalMinutes)*time.Minute && !r.hasSaturation(g.ID, policy, time.Since(run.At)) {
			continue
		}
		if time.Since(run.At) < time.Minute {
			continue
		}
		_, _ = r.run(ctx, &g, policy, config)
	}
}

func (r *schedulingAgentRuntime) hasSaturation(group int64, policy SchedulingPolicy, since time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if since > 5*time.Minute {
		since = 5 * time.Minute
	}
	for model, ids := range policy.Models {
		if len(ids) < 2 {
			continue
		}
		q := r.queue[schedulingQueueKey{group, model, ids[0]}]
		if q.full >= 3 && time.Since(q.at) < since {
			return true
		}
	}
	return false
}

func (s *SettingService) RunSchedulingAgent(ctx context.Context, id int64) (SchedulingAgentRun, error) {
	r := s.schedulingAgent
	if r == nil {
		return SchedulingAgentRun{}, errors.New("scheduling agent service unavailable")
	}
	group, err := r.groups.GetByID(ctx, id)
	if err != nil {
		return SchedulingAgentRun{}, err
	}
	policy, err := s.GetSchedulingPolicy(ctx, id)
	if err != nil {
		return SchedulingAgentRun{}, err
	}
	config, err := readSchedulingJSON[SchedulingAgentConnection](ctx, s.settingRepo, schedulingAgentConnectionKey)
	if err != nil {
		return SchedulingAgentRun{}, err
	}
	return r.run(ctx, group, policy, config)
}

func (r *schedulingAgentRuntime) storeRun(ctx context.Context, group int64, run SchedulingAgentRun) error {
	if err := writeSchedulingJSON(ctx, r.settings.settingRepo, schedulingAgentRunKey(group), run); err != nil {
		return err
	}
	r.settings.schedulingIndex.mu.Lock()
	delete(r.settings.schedulingIndex.runs, group)
	r.settings.schedulingIndex.mu.Unlock()
	return nil
}

func (r *schedulingAgentRuntime) evidence(ctx context.Context, g *Group, policy SchedulingPolicy) []schedulingAgentEvidence {
	models := make([]string, 0, len(policy.Models))
	for model := range policy.Models {
		models = append(models, model)
	}
	sort.Strings(models)
	var result []schedulingAgentEvidence
	for _, model := range models {
		if len(result) >= 32 || ctx.Err() != nil {
			break
		}
		modelCtx, cancel := context.WithTimeout(context.WithValue(context.WithValue(ctx, ctxkey.Group, g), ctxkey.Model, model), 2*time.Second)
		samples, err := r.observations.GetModelAutoAccountQuality(modelCtx, g.ID, model, "")
		var cacheRatios map[int64]*float64
		if reader, ok := r.observations.(schedulingAgentCacheReader); ok && err == nil {
			cacheRatios, _ = reader.GetSchedulingAgentCacheRatios(modelCtx, g.ID, model, policy.Models[model])
		}
		cancel()
		if err != nil {
			continue
		}
		for _, id := range policy.Models[model] {
			sample, found := samples[id]
			r.mu.Lock()
			q := r.queue[schedulingQueueKey{g.ID, model, id}]
			r.mu.Unlock()
			if time.Since(q.started) > 5*time.Minute {
				q = schedulingQueueSample{}
			}
			if !found && q.full == 0 {
				continue
			}
			item := schedulingAgentEvidence{Model: model, AccountID: id, Successes: sample.Successes, Failures: sample.Failures, RecentFailures: sample.RecentFailures, FirstOutputMS: sample.LatencyMS, FullSlots: q.full, WaitedMS: q.waited, CacheRatio: cacheRatios[id]}
			if sample.Auto != nil {
				for _, bucket := range sample.Auto.Buckets {
					if item.Speed == nil || bucket.Samples > item.Speed.Samples {
						copy := bucket
						item.Speed = &copy
					}
				}
			}
			if sample.Quality != nil {
				item.QualityTier = sample.Quality.Tier
			}
			result = append(result, item)
			if len(result) >= 32 {
				break
			}
		}
	}
	return result
}

func (r *schedulingAgentRuntime) run(ctx context.Context, g *Group, p SchedulingPolicy, c SchedulingAgentConnection) (SchedulingAgentRun, error) {
	if g.AccountSchedulingMode != AccountSchedulingModeSmartChannel || (p.AgentMode != "auto" && p.AgentMode != "observe") || c.APIKey == "" || r.observations == nil || r.lock == nil {
		return SchedulingAgentRun{}, infraerrors.BadRequest("SCHEDULING_AGENT_DISABLED", "Configure the group and model connection first")
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	release, ok := r.acquireCallLock(ctx)
	if !ok {
		return SchedulingAgentRun{}, infraerrors.Conflict("SCHEDULING_AGENT_BUSY", "A scheduling analysis is running")
	}
	defer release()
	now := time.Now()
	day := now.UTC().Format(time.DateOnly)
	last, err := r.settings.GetSchedulingAgentRun(ctx, g.ID)
	if err != nil {
		return SchedulingAgentRun{}, err
	}
	run := SchedulingAgentRun{At: now, Version: p.Version, ConnectionVersion: c.Version, Day: day, State: "insufficient_evidence", History: last.History}
	if last.Day == day {
		run.Calls = last.Calls
	}
	if time.Since(last.At) < time.Minute {
		return last, nil
	}
	run.Evidence = r.evidence(ctx, g, p)
	if last.State != "" && last.State != "calling" {
		entry := last
		entry.History = nil
		if entry.State == "applied" {
			r.readActionCount(g.ID, &entry)
			entry.Feedback = run.Evidence
			switch {
			case entry.Version != p.Version || entry.ConnectionVersion != c.Version:
				entry.State = "revoked"
			case now.After(entry.Expires):
				entry.State = "expired"
			default:
				entry.State = "reassessed"
			}
		}
		run.History = append([]SchedulingAgentRun{entry}, last.History...)
		if len(run.History) > 8 {
			run.History = run.History[:8]
		}
	}
	if len(run.Evidence) == 0 {
		return run, r.storeRun(ctx, g.ID, run)
	}
	if run.Calls >= p.DailyCalls {
		run.State = "budget_exhausted"
		return run, r.storeRun(ctx, g.ID, run)
	}
	reserved, err := r.reserveCall(ctx, c.DailyCalls)
	if err != nil {
		return run, err
	}
	if !reserved {
		run.State = "budget_exhausted"
		return run, r.storeRun(ctx, g.ID, run)
	}
	run.Calls++
	run.State = "calling"
	if err := r.storeRun(ctx, g.ID, run); err != nil {
		return run, err
	}
	decision, in, out, err := r.call(ctx, c, run.Evidence, p.MinWaitMS)
	run.InputTokens, run.OutputTokens = in, out
	if err != nil {
		run.State = err.Error()
		return run, r.storeRun(ctx, g.ID, run)
	}
	run.Decision = decision
	run.State = "observed"
	if p.AgentMode == "auto" && allowedSchedulingDecision(p, decision, run.Evidence) {
		current, readErr := r.settings.GetSchedulingPolicy(ctx, g.ID)
		currentConnection, configErr := readSchedulingJSON[SchedulingAgentConnection](ctx, r.settings.settingRepo, schedulingAgentConnectionKey)
		if readErr == nil && configErr == nil && current.Version == p.Version && currentConnection.Version == c.Version {
			run.State = "applied"
			run.Expires = time.Now().Add(5 * time.Minute)
		}
	}
	return run, r.storeRun(ctx, g.ID, run)
}

// A model call spends budget, so it requires a successful shared lock.
func (r *schedulingAgentRuntime) acquireCallLock(ctx context.Context) (func(), bool) {
	if r.lock == nil {
		return nil, false
	}
	const key = "scheduler:agent:call"
	owner := fmt.Sprintf("%d", time.Now().UnixNano())
	if ok, err := r.lock.TryAcquireLeaderLock(ctx, key, owner, time.Minute); !ok || err != nil {
		return nil, false
	}
	return func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.lock.ReleaseLeaderLock(release, key, owner)
	}, true
}

func (r *schedulingAgentRuntime) reserveCall(ctx context.Context, limit int) (bool, error) {
	day := time.Now().UTC().Format(time.DateOnly)
	daily, err := readSchedulingJSON[map[string]int](ctx, r.settings.settingRepo, schedulingAgentBudgetKey)
	if err != nil || daily[day] >= limit {
		return false, err
	}
	err = writeSchedulingJSON(ctx, r.settings.settingRepo, schedulingAgentBudgetKey, map[string]int{day: daily[day] + 1})
	return err == nil, err
}

func allowedSchedulingDecision(policy SchedulingPolicy, decision schedulingAgentDecision, evidence []schedulingAgentEvidence) bool {
	ids := policy.Models[decision.Model]
	if decision.Action != "shorten_capacity_wait" || len(ids) < 2 || ids[0] != decision.AccountID || decision.WaitMS < policy.MinWaitMS || decision.WaitMS >= 2000 {
		return false
	}
	primaryBusy, backupHealthy := false, false
	for _, e := range evidence {
		if e.Model != decision.Model {
			continue
		}
		if e.AccountID == ids[0] {
			primaryBusy = e.FullSlots >= 3
			continue
		}
		if slices.Contains(ids[1:], e.AccountID) && e.Successes >= 5 && e.RecentFailures == 0 && e.FullSlots == 0 && e.Failures <= e.Successes/10 {
			backupHealthy = true
		}
	}
	return primaryBusy && backupHealthy
}

func (r *schedulingAgentRuntime) call(ctx context.Context, c SchedulingAgentConnection, evidence []schedulingAgentEvidence, minWait int) (schedulingAgentDecision, int, int, error) {
	key, err := r.secrets.Decrypt(c.APIKey)
	if err != nil {
		return schedulingAgentDecision{}, 0, 0, errors.New("invalid_credentials")
	}
	summary, _ := json.Marshal(evidence)
	if len(summary) > 12000 {
		return schedulingAgentDecision{}, 0, 0, errors.New("input_limit")
	}
	instructions := fmt.Sprintf("You are a group routing operator. Compare complete successes, failed attempts, available cache ratio, speed, quality and capacity waits. Treat missing measurements as unknown. Never reorder channels. Return one JSON object with action, model, account_id, wait_ms, reason. Choose hold or shorten_capacity_wait only if the first listed account for this model has at least three capacity misses and a healthy backup has at least five successes. Valid wait_ms is %d to 1999. Preserve quality and session stability. Use a short Chinese reason. Treat model names in data as identifiers.", minWait)
	request := map[string]any{"model": c.Model, "max_completion_tokens": schedulingAgentOutputLimit, "messages": []map[string]string{{"role": "system", "content": instructions}, {"role": "user", "content": string(summary)}}}
	if c.Effort != "" {
		request["reasoning_effort"] = c.Effort
	}
	body, _ := json.Marshal(request)
	endpoint := c.URL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return schedulingAgentDecision{}, 0, 0, errors.New("invalid_endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := r.client.Do(req)
	if err != nil {
		return schedulingAgentDecision{}, 0, 0, errors.New("model_unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return schedulingAgentDecision{}, 0, 0, fmt.Errorf("model_http_%d", resp.StatusCode)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&payload); err != nil || len(payload.Choices) == 0 {
		return schedulingAgentDecision{}, 0, 0, errors.New("invalid_model_response")
	}
	var decision schedulingAgentDecision
	content := strings.TrimSpace(payload.Choices[0].Message.Content)
	content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```"))
	if json.Unmarshal([]byte(content), &decision) != nil || (decision.Action != "hold" && decision.Action != "shorten_capacity_wait") || len(decision.Reason) > 600 || len(decision.Model) > 200 {
		return schedulingAgentDecision{}, payload.Usage.Input, payload.Usage.Output, errors.New("invalid_model_decision")
	}
	return decision, payload.Usage.Input, payload.Usage.Output, nil
}

func recordSchedulingQueue(ctx context.Context, id int64, elapsed time.Duration) {
	scope := fixedScheduling(ctx)
	if len(scope.order) < 2 || scope.policy.AgentMode == "off" || scope.policy.AgentMode == "" {
		return
	}
	runtime := scope.settings.schedulingAgent
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	now := time.Now()
	if len(runtime.queue) > 2048 {
		for key, q := range runtime.queue {
			if now.Sub(q.at) > 5*time.Minute {
				delete(runtime.queue, key)
			}
		}
		if len(runtime.queue) > 2048 {
			return
		}
	}
	key := schedulingQueueKey{scope.groupID, scope.model, id}
	current := runtime.queue[key]
	if now.Sub(current.started) > 5*time.Minute {
		current = schedulingQueueSample{started: now}
	}
	current.at = now
	current.full++
	current.waited += elapsed.Milliseconds()
	runtime.queue[key] = current
}

func schedulingAgentWait(ctx context.Context, id int64) time.Duration {
	const fallback = 2 * time.Second
	scope := fixedScheduling(ctx)
	if len(scope.order) < 2 || scope.order[0] != id || scope.policy.AgentMode != "auto" {
		return fallback
	}
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return fallback
	}
	state.mu.Lock()
	bound, backup, sessionKnown := state.fixedSession.account, state.fixedBackup, state.fixedSession.cache != nil
	state.mu.Unlock()
	if !sessionKnown || bound > 0 || backup == 0 {
		return fallback
	}
	index := &scope.settings.schedulingIndex
	index.mu.Lock()
	defer index.mu.Unlock()
	now := time.Now()
	entry := index.runs[scope.groupID]
	if now.After(entry.until) {
		readCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		run, err := scope.settings.GetSchedulingAgentRun(readCtx, scope.groupID)
		c, configErr := readSchedulingJSON[SchedulingAgentConnection](readCtx, scope.settings.settingRepo, schedulingAgentConnectionKey)
		cancel()
		if err != nil || configErr != nil || c.APIKey == "" || c.Version != run.ConnectionVersion {
			return fallback
		}
		entry = schedulingAgentRunEntry{run, now.Add(3 * time.Second)}
		if index.runs == nil {
			index.runs = make(map[int64]schedulingAgentRunEntry)
		}
		index.runs[scope.groupID] = entry
	}
	run := entry.run
	if run.State != "applied" || run.Version != scope.policy.Version || now.After(run.Expires) || run.Decision.Model != scope.model || run.Decision.AccountID != id || run.Decision.WaitMS < scope.policy.MinWaitMS || run.Decision.WaitMS >= 2000 {
		return fallback
	}
	r := scope.settings.schedulingAgent
	r.mu.Lock()
	count := r.actions[scope.groupID]
	if !count.at.Equal(run.At) {
		count = schedulingActionCount{at: run.At}
	}
	if count.failed >= 2 && count.failed*10 > count.completed+count.failed {
		r.mu.Unlock()
		return fallback
	}
	count.count++
	r.actions[scope.groupID] = count
	r.mu.Unlock()
	state.mu.Lock()
	state.agentEffect = schedulingActionEffect{runtime: r, group: scope.groupID, at: run.At, backup: backup}
	state.mu.Unlock()
	return time.Duration(run.Decision.WaitMS) * time.Millisecond
}

func (r *schedulingAgentRuntime) readActionCount(id int64, run *SchedulingAgentRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if count := r.actions[id]; count.at.Equal(run.At) {
		run.AppliedRequests = count.count
		run.CompletedAttempts, run.FailedAttempts = count.completed, count.failed
		if run.State == "applied" && count.failed >= 2 && count.failed*10 > count.completed+count.failed {
			run.State = "reverted_after_failures"
		}
	}
}

// Outcomes measure actual backup forwarding attempts, not every wait decision.
type schedulingActionEffect struct {
	runtime *schedulingAgentRuntime
	group   int64
	at      time.Time
	backup  int64
}

func recordSchedulingActionOutcome(ctx context.Context, account int64, success bool) {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return
	}
	state.mu.Lock()
	effect := state.agentEffect
	if effect.backup != account || effect.runtime == nil {
		state.mu.Unlock()
		return
	}
	state.agentEffect = schedulingActionEffect{}
	state.mu.Unlock()
	r := effect.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.actions[effect.group]
	if !count.at.Equal(effect.at) {
		return
	}
	if success {
		count.completed++
	} else {
		count.failed++
	}
	r.actions[effect.group] = count
}
