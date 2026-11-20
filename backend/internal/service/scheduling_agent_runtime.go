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
const schedulingAgentCallTimeout = 90 * time.Second
const schedulingAgentRunTimeout = 5 * time.Minute

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
	Effort    string `json:"effort,omitempty"`
	Hours     int    `json:"hours,omitempty"`
}

type SchedulingAgentRun struct {
	At                time.Time                        `json:"at"`
	Version           int64                            `json:"version"`
	ConnectionVersion int64                            `json:"connection_version"`
	AppliedRequests   int                              `json:"applied_requests"`
	CompletedAttempts int                              `json:"completed_attempts"`
	FailedAttempts    int                              `json:"failed_attempts"`
	History           []SchedulingAgentRun             `json:"history,omitempty"`
	Day               string                           `json:"day"`
	Calls             int                              `json:"calls"`
	State             string                           `json:"state"`
	Decision          schedulingAgentDecision          `json:"decision"`
	Expires           time.Time                        `json:"expires"`
	InputTokens       int                              `json:"input_tokens"`
	OutputTokens      int                              `json:"output_tokens"`
	Evidence          []schedulingAgentEvidence        `json:"evidence,omitempty"`
	Signal            *SchedulingAgentActivity         `json:"signal,omitempty"`
	Traffic           []SchedulingAgentTraffic         `json:"traffic_last_hour,omitempty"`
	Feedback          []schedulingAgentEvidence        `json:"feedback,omitempty"`
	Routes            map[string]SchedulingAgentRoute  `json:"routes,omitempty"`
	NextModel         int                              `json:"next_model,omitempty"`
	Reviews           map[string]schedulingAgentReview `json:"reviews,omitempty"`
	Inspections       []schedulingAgentDecision        `json:"inspections,omitempty"`
}

type schedulingAgentCacheReader interface {
	GetSchedulingAgentCacheRatios(context.Context, int64, string, []int64) (map[int64]*float64, error)
}

type schedulingAgentEvidence struct {
	PoolID          string                       `json:"pool_id,omitempty"`
	PoolName        string                       `json:"pool_name,omitempty"`
	Eligible        bool                         `json:"eligible"`
	Model           string                       `json:"model"`
	AccountID       int64                        `json:"account_id"`
	Successes       int64                        `json:"successes"`
	Failures        int64                        `json:"failures"`
	RecentFailures  int64                        `json:"recent_failures"`
	FirstOutputMS   float64                      `json:"first_event_ms"`
	CacheRatio      *float64                     `json:"cache_ratio,omitempty"`
	Speed           *ModelAutoSpeedBucket        `json:"speed,omitempty"`
	QualityTier     string                       `json:"quality_tier,omitempty"`
	FullSlots       int                          `json:"full_slots"`
	WaitedMS        int64                        `json:"waited_ms"`
	Current         bool                         `json:"current,omitempty"`
	Trial           bool                         `json:"trial,omitempty"`
	RouteReason     string                       `json:"route_reason,omitempty"`
	TrialReason     string                       `json:"trial_reason,omitempty"`
	QualityEffort   string                       `json:"quality_effort,omitempty"`
	QualityVerified bool                         `json:"quality_verified,omitempty"`
	Windows         []SchedulingAgentSpeedWindow `json:"recent_windows,omitempty"`
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
	return &http.Client{Transport: transport, Timeout: schedulingAgentCallTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
		_, _ = r.runForActivity(ctx, &g, policy, config, true)
	}
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

func (r *schedulingAgentRuntime) evidence(ctx context.Context, g *Group, policy SchedulingPolicy, models []string, routes map[string]SchedulingAgentRoute) []schedulingAgentEvidence {
	perModel := max(2, 32/max(1, len(models)))
	var accounts map[int64]*Account
	if r.accounts != nil {
		members, err := r.accounts.ListByGroup(ctx, g.ID)
		if err != nil {
			return nil
		}
		accounts = make(map[int64]*Account, len(members))
		for i := range members {
			accounts[members[i].ID] = &members[i]
		}
	}
	var result []schedulingAgentEvidence
	for _, model := range models {
		if len(result) >= 32 || ctx.Err() != nil {
			break
		}
		base := context.WithValue(context.WithValue(ctx, ctxkey.Group, g), ctxkey.Model, model)
		modelCtx, cancel := context.WithTimeout(base, 2*time.Second)
		samples, err := r.observations.GetModelAutoAccountQuality(modelCtx, g.ID, model, "")
		var cacheRatios map[int64]*float64
		order := policy.order(model)
		if reader, ok := r.observations.(schedulingAgentCacheReader); ok && err == nil {
			cacheRatios, _ = reader.GetSchedulingAgentCacheRatios(modelCtx, g.ID, model, order)
		}
		cancel()
		if err != nil {
			continue
		}
		var windows map[int64][]SchedulingAgentSpeedWindow
		if reader, ok := r.observations.(schedulingAgentSpeedReader); ok && len(order) >= 2 {
			read, cancel := context.WithTimeout(base, 2*time.Second)
			windows, _ = reader.GetSchedulingAgentSpeedWindows(read, g.ID, model, order)
			cancel()
		}
		var entries []schedulingAgentEvidence
		for _, id := range order {
			sample, found := samples[id]
			r.mu.Lock()
			q := r.queue[schedulingQueueKey{g.ID, model, id}]
			r.mu.Unlock()
			if time.Since(q.started) > 5*time.Minute {
				q = schedulingQueueSample{}
			}
			if accounts == nil && !found && q.full == 0 && len(windows[id]) == 0 {
				continue
			}
			item := schedulingAgentEvidence{Model: model, AccountID: id, Successes: sample.Successes, Failures: sample.Failures, RecentFailures: sample.RecentFailures, FirstOutputMS: sample.LatencyMS, FullSlots: q.full, WaitedMS: q.waited, CacheRatio: cacheRatios[id]}
			item.Eligible = true
			if accounts != nil {
				account := accounts[id]
				item.Eligible = account != nil && account.IsSchedulableForModelWithContext(base, model) && r.settings.IsModelAccountAllowed(base, &g.ID, model, id, account)
			}
			if pool := policy.pool(id); pool != nil {
				item.PoolID, item.PoolName = pool.ID, pool.Name
			}
			if sample.Auto != nil {
				for _, bucket := range sample.Auto.Buckets {
					if item.Speed == nil || bucket.Samples > item.Speed.Samples {
						copy := bucket
						item.Speed = &copy
					}
				}
			}
			if sample.Quality != nil {
				item.QualityTier = QualityEvidenceTier(sample.Quality)
				item.QualityEffort = sample.Quality.TestEffort
				item.QualityVerified = sample.Quality.tier(time.Now()) <= 1 && !modelAutoPending(sample) && !sample.qualityDegraded()
			}
			item.Windows = windows[id]
			entries = append(entries, item)
		}
		route := routes[model]
		current := -1
		for i, item := range entries {
			sample := samples[item.AccountID]
			if !item.Eligible || sample.qualityDegraded() || sample.reliabilityTier(10) == 3 {
				continue
			}
			if current == -1 {
				current = i
			}
			if item.AccountID == route.AccountID && schedulingRouteCandidate(sample) {
				current = i
				break
			}
		}
		if current >= 0 {
			entries[current].Current = true
			if entries[current].AccountID == route.AccountID {
				entries[current].RouteReason = route.Reason
			}
		}
		for i := range entries {
			if entries[i].AccountID == route.TrialID {
				entries[i].Trial = time.Now().Before(route.TrialUntil)
				entries[i].TrialReason = route.TrialReason
			}
		}
		slices.SortStableFunc(entries, func(a, b schedulingAgentEvidence) int {
			rank := func(item schedulingAgentEvidence) int {
				switch {
				case item.Current:
					return 0
				case item.Trial:
					return 1
				case item.Eligible && item.QualityVerified:
					return 2
				case item.Eligible:
					return 3
				default:
					return 4
				}
			}
			return rank(a) - rank(b)
		})
		result = append(result, entries[:min(len(entries), perModel, 32-len(result))]...)
	}
	return result
}

func (r *schedulingAgentRuntime) run(ctx context.Context, g *Group, p SchedulingPolicy, c SchedulingAgentConnection) (SchedulingAgentRun, error) {
	return r.runForActivity(ctx, g, p, c, false)
}

func (r *schedulingAgentRuntime) runForActivity(ctx context.Context, g *Group, p SchedulingPolicy, c SchedulingAgentConnection, automatic bool) (SchedulingAgentRun, error) {
	if g.AccountSchedulingMode != AccountSchedulingModeSmartChannel || (p.AgentMode != "auto" && p.AgentMode != "observe") || c.APIKey == "" || r.observations == nil || r.lock == nil {
		return SchedulingAgentRun{}, infraerrors.BadRequest("SCHEDULING_AGENT_DISABLED", "Configure the group and model connection first")
	}
	ctx, cancel := context.WithTimeout(ctx, schedulingAgentRunTimeout)
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
	models := p.analysisModels()
	if last.Version == p.Version && last.ConnectionVersion == c.Version {
		run.Routes = last.Routes
		run.Reviews = last.Reviews
		if len(models) > 0 && last.NextModel > 0 {
			offset := last.NextModel % len(models)
			models = append(models[offset:len(models):len(models)], models[:offset]...)
		}
	}
	var signal SchedulingAgentActivity
	if automatic {
		signal, err = r.nextActivity(ctx, g.ID, p, models, run.Reviews, last.At)
		if err != nil || signal.Model == "" {
			return last, err
		}
		if now.Sub(last.At) < time.Duration(c.IntervalMinutes)*time.Minute &&
			!(signal.urgentAfter(last.At) && signal.urgentAfter(run.Reviews[signal.Model].Through)) {
			return last, nil
		}
		run.Signal = &signal
		models = []string{signal.Model}
	}
	daily, err := readSchedulingJSON[map[string]int](ctx, r.settings.settingRepo, schedulingAgentBudgetKey)
	if err != nil {
		return last, err
	}
	// The shared call lock keeps the round's remaining slots available until its final answer.
	calls := min(3, c.DailyCalls-daily[day], p.DailyCalls-run.Calls)
	if calls <= 0 && last.State == "budget_exhausted" && last.Day == day && last.Version == p.Version && last.ConnectionVersion == c.Version {
		return last, nil
	}
	if calls > 0 {
		batch := min(8, len(models))
		run.NextModel = (max(1, len(models)) + last.NextModel + batch) % max(1, len(models))
		run.Evidence = r.evidence(ctx, g, p, models[:batch], run.Routes)
	}
	if last.State != "" && last.State != "calling" {
		entry := last
		entry.History = nil
		entry.Routes = nil
		entry.Reviews = nil
		if entry.State == "route_selected" || entry.State == "route_trial" {
			for _, item := range run.Evidence {
				if item.Model == entry.Decision.Model {
					entry.Feedback = append(entry.Feedback, item)
				}
			}
		}
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
	if calls <= 0 {
		run.State = "budget_exhausted"
		return run, r.storeRun(ctx, g.ID, run)
	}
	if len(run.Evidence) == 0 {
		return run, r.storeRun(ctx, g.ID, run)
	}
	decision, err := r.investigate(ctx, g, p, c, &run, signal, calls)
	if err != nil {
		run.State = err.Error()
		if ctx.Err() != nil && run.Calls > 0 {
			write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			return run, r.storeRun(write, g.ID, run)
		}
		return run, r.storeRun(ctx, g.ID, run)
	}
	if decision.Model == "" && signal.Model != "" && decision.Action == "hold" {
		decision.Model = signal.Model
	}
	run.Decision = decision
	if signal.Model != "" {
		if run.Reviews == nil {
			run.Reviews = make(map[string]schedulingAgentReview)
		}
		run.Reviews[signal.Model] = schedulingAgentReview{Through: signal.At, Decision: decision}
	}
	run.State = "observed"
	route, changeRoute := schedulingRouteChange(p, run.Routes[decision.Model], decision, run.Evidence, time.Now())
	if p.AgentMode == "auto" && (changeRoute || allowedSchedulingDecision(p, decision, run.Evidence)) {
		current, readErr := r.settings.GetSchedulingPolicy(ctx, g.ID)
		currentConnection, configErr := readSchedulingJSON[SchedulingAgentConnection](ctx, r.settings.settingRepo, schedulingAgentConnectionKey)
		if readErr == nil && configErr == nil && current.Version == p.Version && currentConnection.Version == c.Version {
			if changeRoute {
				if run.Routes == nil {
					run.Routes = make(map[string]SchedulingAgentRoute)
				}
				run.Routes[decision.Model] = route
				switch decision.Action {
				case schedulingTrialRoute:
					run.State, run.Expires = "route_trial", route.TrialUntil
				case "stop_trial":
					run.State = "trial_stopped"
				default:
					run.State = "route_selected"
				}
			} else {
				run.State = "applied"
				run.Expires = time.Now().Add(5 * time.Minute)
			}
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
	if ok, err := r.lock.TryAcquireLeaderLock(ctx, key, owner, schedulingAgentRunTimeout+10*time.Second); !ok || err != nil {
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
	if !slices.Contains(policy.analysisModels(), decision.Model) {
		return false
	}
	ids := policy.order(decision.Model)
	if len(ids) < 2 || !slices.Contains(ids, decision.AccountID) {
		return false
	}
	if decision.Action == "defer_member" {
		if policy.pool(decision.AccountID) == nil {
			return false
		}
		unhealthy, backupHealthy := false, false
		for _, e := range evidence {
			if e.Model != decision.Model {
				continue
			}
			if e.AccountID == decision.AccountID {
				unhealthy = e.RecentFailures >= 2
				continue
			}
			if slices.Contains(ids, e.AccountID) && schedulingEvidenceHealthy(e) {
				backupHealthy = true
			}
		}
		return unhealthy && backupHealthy
	}
	if decision.Action != "shorten_capacity_wait" || decision.WaitMS < policy.MinWaitMS || decision.WaitMS >= 2000 {
		return false
	}
	primaryBusy, backupHealthy := false, false
	for _, e := range evidence {
		if e.Model != decision.Model {
			continue
		}
		if e.AccountID == decision.AccountID {
			primaryBusy = e.FullSlots >= 3
			continue
		}
		if slices.Index(ids, e.AccountID) > slices.Index(ids, decision.AccountID) && schedulingEvidenceHealthy(e) {
			backupHealthy = true
		}
	}
	return primaryBusy && backupHealthy
}

func schedulingEvidenceHealthy(e schedulingAgentEvidence) bool {
	return e.Eligible && e.Successes >= 5 && e.RecentFailures == 0 && e.FullSlots == 0 && e.Failures <= e.Successes/10 && e.QualityTier != "degraded"
}

func (r *schedulingAgentRuntime) call(ctx context.Context, c SchedulingAgentConnection, evidence []schedulingAgentEvidence, minWait int) (schedulingAgentDecision, int, int, error) {
	summary, _ := json.Marshal(evidence)
	return r.exchange(ctx, c, []map[string]string{{"role": "system", "content": schedulingAgentInstructions(minWait, false)}, {"role": "user", "content": string(summary)}})
}

func (r *schedulingAgentRuntime) exchange(ctx context.Context, c SchedulingAgentConnection, messages []map[string]string) (schedulingAgentDecision, int, int, error) {
	key, err := r.secrets.Decrypt(c.APIKey)
	if err != nil {
		return schedulingAgentDecision{}, 0, 0, errors.New("invalid_credentials")
	}
	request := map[string]any{"model": c.Model, "max_completion_tokens": schedulingAgentOutputLimit, "messages": messages}
	if c.Effort != "" {
		request["reasoning_effort"] = c.Effort
	}
	body, _ := json.Marshal(request)
	if len(body) > 64000 {
		return schedulingAgentDecision{}, 0, 0, errors.New("input_limit")
	}
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
		if schedulingAgentTimedOut(err) {
			return schedulingAgentDecision{}, 0, 0, errors.New("model_timeout")
		}
		return schedulingAgentDecision{}, 0, 0, errors.New("model_unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
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
	err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&payload)
	if schedulingAgentTimedOut(err) {
		return schedulingAgentDecision{}, 0, 0, errors.New("model_timeout")
	}
	if err != nil || len(payload.Choices) == 0 {
		return schedulingAgentDecision{}, 0, 0, errors.New("invalid_model_response")
	}
	var decision schedulingAgentDecision
	content := strings.TrimSpace(payload.Choices[0].Message.Content)
	content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```"))
	if json.Unmarshal([]byte(content), &decision) != nil || len(decision.Reason) > 600 || len(decision.Model) > 200 || len(decision.Effort) > 20 {
		return schedulingAgentDecision{}, payload.Usage.Input, payload.Usage.Output, errors.New("invalid_model_decision")
	}
	switch decision.Action {
	case "hold", "shorten_capacity_wait", "defer_member", "inspect", "stop_trial", schedulingTrialRoute, schedulingPreferRoute:
		return decision, payload.Usage.Input, payload.Usage.Output, nil
	default:
		return schedulingAgentDecision{}, payload.Usage.Input, payload.Usage.Output, errors.New("invalid_model_decision")
	}
}

func schedulingAgentTimedOut(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
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
	if len(scope.order) < 2 || scope.policy.AgentMode != "auto" {
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
	now := time.Now()
	run, ok := currentSchedulingAgentRun(ctx, scope)
	if !ok || run.State != "applied" || run.Decision.Action != "shorten_capacity_wait" || now.After(run.Expires) || run.Decision.Model != scope.model || run.Decision.AccountID != id || run.Decision.WaitMS < scope.policy.MinWaitMS || run.Decision.WaitMS >= 2000 {
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
	runtime       *schedulingAgentRuntime
	group         int64
	at            time.Time
	backup        int64
	recovery      SchedulingPoolRecoveryCache
	recoveryKey   string
	recoveryOwner string
}

func recordSchedulingActionOutcome(ctx context.Context, account int64, success bool) {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil {
		return
	}
	state.mu.Lock()
	effect := state.agentEffect
	if effect.backup != account || (effect.runtime == nil && effect.recovery == nil) {
		state.mu.Unlock()
		return
	}
	state.agentEffect = schedulingActionEffect{}
	state.mu.Unlock()
	if effect.recovery != nil {
		mode := "failure"
		if success {
			mode = "success"
		}
		update, cancel := context.WithTimeout(context.WithoutCancel(ctx), 300*time.Millisecond)
		defer cancel()
		_, _ = effect.recovery.SchedulingPoolRecovery(update, effect.recoveryKey, mode, effect.recoveryOwner)
		return
	}
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
