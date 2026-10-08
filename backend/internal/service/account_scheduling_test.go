package service

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type accountSchedulingQualityStub struct {
	UsageLogRepository
	quality map[int64]GroupModelAccountQuality
}

func (repo accountSchedulingQualityStub) GetGroupModelAccountQuality(context.Context, int64, string, time.Time) (map[int64]GroupModelAccountQuality, error) {
	return repo.quality, nil
}

func TestFastestFailoverQualityPriceAndPreference(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		preferred bool
		latency   float64
		failures  int64
		pinned    int64
		cheapRate float64
		want      int64
	}{
		{name: "cheaper within speed range", latency: 100, cheapRate: 0.5, want: 2},
		{name: "preference beats lower rate", preferred: true, latency: 100, cheapRate: 0.5, want: 1},
		{name: "preference preserves healthy binding", preferred: true, latency: 100, pinned: 2, cheapRate: 0.5, want: 2},
		{name: "slow preference preserves binding", preferred: true, latency: 300, pinned: 2, cheapRate: 0.5, want: 2},
		{name: "slow preference wins initially", preferred: true, latency: 300, cheapRate: 0.5, want: 1},
		{name: "unstable cheap account loses", latency: 100, failures: 30, cheapRate: 0.1, want: 1},
		{name: "healthy binding persists", latency: 100, pinned: 1, cheapRate: 0.1, want: 1},
		{name: "zero rate is valid", latency: 100, cheapRate: 0, want: 2},
		{name: "invalid rate does not affect reliability", latency: 100, cheapRate: math.NaN(), want: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			groupID := int64(5)
			normalRate := 0.8
			candidates := []*Account{
				{ID: 1, Priority: 10, RateMultiplier: &normalRate, Extra: map[string]any{"scheduling_preferred": scenario.preferred}},
				{ID: 2, Priority: 1, RateMultiplier: &scenario.cheapRate},
			}
			cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
			rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", scenario.pinned)
			repo := accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{
				1: {Successes: 100, LatencyMS: scenario.latency},
				2: {Successes: 100, Failures: scenario.failures, LatencyMS: 110},
			}}
			ordered := fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", candidates)
			if ordered[0].ID != scenario.want {
				t.Fatalf("selected %d, want %d", ordered[0].ID, scenario.want)
			}
			if candidates[0].ID != 1 {
				t.Fatal("selection mutated the input")
			}
		})
	}
}

func TestFastestFailoverMultiplePreferredAndUnknownModels(t *testing.T) {
	ctx := context.Background()
	groupID := int64(5)
	expensive, cheap := 2.0, 0.1
	candidates := []*Account{
		{ID: 1, Priority: 1, RateMultiplier: &expensive, Extra: map[string]any{"scheduling_preferred": true}},
		{ID: 2, Priority: 2, RateMultiplier: &cheap, Extra: map[string]any{"scheduling_preferred": true}},
	}
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 2)
	repo := accountSchedulingQualityStub{quality: map[int64]GroupModelAccountQuality{
		1: {Successes: 100, LatencyMS: 100},
		2: {Successes: 100, LatencyMS: 110},
	}}
	if got := fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", candidates)[0].ID; got != 2 {
		t.Fatalf("priority displaced the current channel: %d", got)
	}
	delete(repo.quality, 1)
	if got := fastestFailoverCandidateOrder(ctx, repo, nil, &groupID, "model-a", candidates)[0].ID; got != 2 {
		t.Fatalf("unmeasured preference displaced reliable backup: %d", got)
	}
	candidates[0].Extra = nil
	candidates[1].Extra = nil
	if got := fastestFailoverCandidateOrder(ctx, nil, nil, &groupID, "new-model", candidates)[0].ID; got != 1 {
		t.Fatalf("cold start ignored configured priority: %d", got)
	}
}

type accountSchedulingLatencyStub struct {
	UsageLogRepository
	latencies map[string]map[int64]float64
}

func (s accountSchedulingLatencyStub) GetGroupModelAccountQuality(_ context.Context, _ int64, model string, _ time.Time) (map[int64]GroupModelAccountQuality, error) {
	quality := make(map[int64]GroupModelAccountQuality)
	for accountID, latency := range s.latencies[model] {
		quality[accountID] = GroupModelAccountQuality{Successes: 100, LatencyMS: latency}
	}
	return quality, nil
}

type accountSchedulingCacheStub struct {
	GatewayCache
	mu        sync.Mutex
	bindings  map[string]int64
	revisions map[string]int64
}

func (s *accountSchedulingCacheStub) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings[key], nil
}

func (s *accountSchedulingCacheStub) SetSessionAccountID(_ context.Context, _ int64, key string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revisions == nil {
		s.revisions = make(map[string]int64)
	}
	s.bindings[key] = accountID
	s.revisions[key]++
	return nil
}

func (s *accountSchedulingCacheStub) GetSessionAccountState(_ context.Context, _ int64, key string) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings[key], s.revisions[key], nil
}

func (s *accountSchedulingCacheStub) CompareAndSwapSessionAccountID(_ context.Context, _ int64, key string, revision int64, accountID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revisions[key] != revision {
		return false, nil
	}
	if s.revisions == nil {
		s.revisions = make(map[string]int64)
	}
	s.bindings[key] = accountID
	s.revisions[key]++
	return true, nil
}

func TestFastestFailoverBindingConfirmationRejectsStaleRequests(t *testing.T) {
	groupID := int64(5)
	group := &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover}
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	base := context.WithValue(context.Background(), ctxkey.Group, group)
	rememberGroupModelSchedulingAccount(base, cache, &groupID, "model-a", 1)
	first := prepareFastestFailoverBinding(base, cache, "model-a")
	late := prepareFastestFailoverBinding(base, cache, "model-a")
	confirmGroupModelSchedulingAccount(first, "model-a", 2)
	if got := groupModelSchedulingActiveAccount(base, cache, &groupID, "model-a"); got != 2 {
		t.Fatalf("confirmed backup = %d, want 2", got)
	}
	// A retry can see the new channel without replacing its original revision.
	prepareFastestFailoverBinding(late, cache, "model-a")
	confirmGroupModelSchedulingAccount(late, "model-a", 3)
	confirmGroupModelSchedulingAccount(late, "model-a", 1)
	if got := groupModelSchedulingActiveAccount(base, cache, &groupID, "model-a"); got != 2 {
		t.Fatalf("late request replaced the confirmed channel: %d", got)
	}
}

func TestFastestFailoverBindingManualRefreshInvalidatesOldRequest(t *testing.T) {
	groupID := int64(5)
	base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	for _, winner := range []int64{1, 3} {
		cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
		rememberGroupModelSchedulingAccount(base, cache, &groupID, "model-a", 1)
		old := prepareFastestFailoverBinding(base, cache, "model-a")
		rememberGroupModelSchedulingAccount(base, cache, &groupID, "model-a", winner)
		confirmGroupModelSchedulingAccount(old, "model-a", 2)
		if got := groupModelSchedulingActiveAccount(base, cache, &groupID, "model-a"); got != winner {
			t.Fatalf("old request displaced manual winner %d: %d", winner, got)
		}
	}
}

func TestFastestFailoverBindingConcurrentConfirmations(t *testing.T) {
	groupID := int64(5)
	base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	requests := make([]context.Context, 20)
	for index := range requests {
		requests[index] = prepareFastestFailoverBinding(base, cache, "model-a")
	}
	var workers sync.WaitGroup
	for index, ctx := range requests {
		workers.Add(1)
		go func(ctx context.Context, id int64) {
			defer workers.Done()
			confirmGroupModelSchedulingAccount(ctx, "model-a", id)
		}(ctx, int64(index+1))
	}
	workers.Wait()
	_, revision, err := cache.GetSessionAccountState(base, groupID, groupModelSchedulingStickyKey("model-a"))
	if err != nil || revision != 1 {
		t.Fatalf("concurrent requests committed %d revisions: %v", revision, err)
	}
}

func TestFastestFailoverBindingDoesNotCommitCanceledRequest(t *testing.T) {
	groupID := int64(5)
	base := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	ctx, cancel := context.WithCancel(prepareFastestFailoverBinding(base, cache, "model-a"))
	cancel()
	confirmGroupModelSchedulingAccount(ctx, "model-a", 2)
	if got := groupModelSchedulingActiveAccount(base, cache, &groupID, "model-a"); got != 0 {
		t.Fatalf("canceled request committed channel %d", got)
	}
}

func TestFastestFailoverSameChannelRetryIsBounded(t *testing.T) {
	group := &Group{ID: 5, AccountSchedulingMode: AccountSchedulingModeFastestFailover}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	original := &UpstreamFailoverError{StatusCode: 503, RetryableOnSameAccount: true, SameAccountRetryDeadline: time.Now().Add(time.Minute)}
	limited, ok := limitFastestFailoverRetry(ctx, original, 1).(*UpstreamFailoverError)
	if !ok || limited.SameAccountRetryMax != 1 || original.SameAccountRetryMax != 0 || limited.SameAccountRetryDeadline != original.SameAccountRetryDeadline {
		t.Fatal("retry cap must preserve the error and its deadline without mutating it")
	}
	exhausted, ok := limitFastestFailoverRetry(ctx, original, 2).(*UpstreamFailoverError)
	if !ok || exhausted.RetryableOnSameAccount {
		t.Fatal("an internal retry must not receive another handler retry")
	}
	group.AccountSchedulingMode = AccountSchedulingModePriority
	if limitFastestFailoverRetry(ctx, original, 1) != original {
		t.Fatal("ordinary scheduling retry policy changed")
	}
}

func TestFastestFailoverCandidateOrderUsesRequestedModelLatency(t *testing.T) {
	groupID := int64(3)
	accounts := []*Account{{ID: 1, Priority: 1}, {ID: 2, Priority: 1}}
	repo := accountSchedulingLatencyStub{latencies: map[string]map[int64]float64{
		"gpt-5":  {1: 500, 2: 100},
		"claude": {1: 80, 2: 450},
	}}

	for _, test := range []struct {
		model string
		first int64
	}{{model: "gpt-5", first: 2}, {model: "claude", first: 1}} {
		ordered := fastestFailoverCandidateOrder(context.Background(), repo, nil, &groupID, test.model, accounts)
		if ordered[0].ID != test.first {
			t.Fatalf("model %q: first account = %d, want %d", test.model, ordered[0].ID, test.first)
		}
	}
}

func TestFastestFailoverCandidateOrderPinsThenFailsOver(t *testing.T) {
	ctx := context.Background()
	groupID := int64(5)
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	repo := accountSchedulingLatencyStub{latencies: map[string]map[int64]float64{"model-a": {1: 50, 2: 300}}}
	accounts := []*Account{{ID: 1}, {ID: 2}}

	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 2)
	ordered := fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", accounts)
	if ordered[0].ID != 2 {
		t.Fatalf("pinned account = %d, want 2", ordered[0].ID)
	}

	ordered = fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", accounts[0:1])
	if ordered[0].ID != 1 {
		t.Fatalf("failover account = %d, want 1", ordered[0].ID)
	}
	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", ordered[0].ID)
	ordered = fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", accounts)
	if ordered[0].ID != 1 {
		t.Fatalf("active account after failover = %d, want 1", ordered[0].ID)
	}
}

func TestFastestFailoverPinIsScopedToRequestedModel(t *testing.T) {
	ctx := context.Background()
	groupID := int64(8)
	cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 3)
	rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-b", 4)

	if got := groupModelSchedulingActiveAccount(ctx, cache, &groupID, "model-a"); got != 3 {
		t.Fatalf("model-a active account = %d, want 3", got)
	}
	if got := groupModelSchedulingActiveAccount(ctx, cache, &groupID, "model-b"); got != 4 {
		t.Fatalf("model-b active account = %d, want 4", got)
	}
}

func TestFastestFailoverCandidateOrderFallsBackWithoutSamples(t *testing.T) {
	accounts := []*Account{{ID: 1, Priority: 10}, {ID: 2, Priority: 1}}
	ordered := fastestFailoverCandidateOrder(context.Background(), nil, nil, nil, "model-a", accounts)
	if ordered[0].ID != 2 {
		t.Fatalf("legacy fallback account = %d, want 2", ordered[0].ID)
	}
}

func TestFastestFailoverEnabledIsOptIn(t *testing.T) {
	groupID := int64(7)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModePriority})
	if fastestFailoverEnabled(ctx, &groupID) {
		t.Fatal("priority group unexpectedly enables fastest failover")
	}
	ctx = context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	if !fastestFailoverEnabled(ctx, &groupID) {
		t.Fatal("fastest-failover group did not enable the mode")
	}
}

func TestFastestFailoverAuthSnapshotPreservesMode(t *testing.T) {
	svc := &APIKeyService{}
	apiKey := profitAuthTestAPIKey()
	apiKey.Group.AccountSchedulingMode = AccountSchedulingModeFastestFailover
	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	var restored APIKeyAuthCacheEntry
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &restored)
	if err != nil || !used || materialized == nil || materialized.Group == nil {
		t.Fatalf("restore auth snapshot: used=%v, err=%v", used, err)
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, materialized.Group)
	if !fastestFailoverEnabled(ctx, materialized.GroupID) {
		t.Fatal("auth cache lost the scheduling mode")
	}
	restored.Snapshot.Version = 24
	if _, used, err := svc.applyAuthCacheEntry(apiKey.Key, &restored); err != nil || used {
		t.Fatalf("old auth snapshot must be rebuilt: used=%v, err=%v", used, err)
	}
}

func TestFastestFailoverOpenAISchedulerUsesModelPinWithAdvancedSchedulerEnabled(t *testing.T) {
	for _, loadBatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "load_aware"}[loadBatch], func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(101)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
				AccountSchedulingMode: AccountSchedulingModeFastestFailover,
			})
			accounts := []Account{
				{ID: 1, Priority: 0, Credentials: map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}},
				{ID: 2, Priority: 10, Credentials: map[string]any{"model_mapping": map[string]any{"model-a": "model-a"}}},
				{ID: 3, Priority: 1, Credentials: map[string]any{"model_mapping": map[string]any{"model-a": "model-a"}}},
			}
			for index := range accounts {
				accounts[index].Platform = PlatformOpenAI
				accounts[index].Type = AccountTypeAPIKey
				accounts[index].Status = StatusActive
				accounts[index].Schedulable = true
				accounts[index].Concurrency = 1
				accounts[index].GroupIDs = []int64{groupID}
			}
			cache := &accountSchedulingCacheStub{bindings: map[string]int64{"old-session": 3}}
			rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 2)
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg,
				usageLogRepo:       accountSchedulingLatencyStub{latencies: map[string]map[int64]float64{"model-a": {1: 1, 2: 50, 3: 300}}},
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			if !svc.isOpenAIAdvancedSchedulerEnabled(ctx) {
				t.Fatal("advanced scheduler must be enabled for this regression test")
			}
			for _, request := range []struct {
				session  string
				excluded map[int64]struct{}
				want     int64
			}{
				{session: "old-session", want: 2},
				{session: "new-session", want: 2},
				{session: "failover", excluded: map[int64]struct{}{2: {}}, want: 3},
				{session: "after-failover", want: 3},
			} {
				requestCtx := WithFastestFailoverRequestState(ctx)
				selection, _, err := svc.SelectAccountWithScheduler(requestCtx, &groupID, "", request.session, "model-a", request.excluded, OpenAIUpstreamTransportAny, false)
				if err != nil {
					t.Fatal(err)
				}
				if selection == nil || selection.Account == nil || selection.Account.ID != request.want {
					t.Fatalf("session %q: selection = %+v, want account %d", request.session, selection, request.want)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				confirmGroupModelSchedulingAccount(requestCtx, "model-a", selection.Account.ID)
			}
		})
	}
}

func TestFastestFailoverHealthyChannelPersistsAcrossSchedulerPaths(t *testing.T) {
	for _, scenario := range []struct {
		platform  string
		loadBatch bool
	}{
		{PlatformOpenAI, false}, {PlatformOpenAI, true},
		{PlatformGrok, false}, {PlatformGrok, true},
	} {
		t.Run(scenario.platform+"/"+map[bool]string{false: "legacy", true: "load_aware"}[scenario.loadBatch], func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(102)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: groupID, Platform: scenario.platform, Status: StatusActive, Hydrated: true,
				AccountSchedulingMode: AccountSchedulingModeFastestFailover,
			})
			expensive, cheap := 1.0, 0.5
			accounts := []Account{
				{ID: 1, Priority: 1, RateMultiplier: &expensive},
				{ID: 2, Priority: 10, RateMultiplier: &cheap},
				{ID: 3, Priority: 0, RateMultiplier: &cheap, Extra: map[string]any{"scheduling_preferred": true}},
			}
			for index := range accounts {
				accounts[index].Platform = scenario.platform
				accounts[index].Type = AccountTypeAPIKey
				accounts[index].Status = StatusActive
				accounts[index].Schedulable = true
				accounts[index].Concurrency = 1
				accounts[index].GroupIDs = []int64{groupID}
				accounts[index].Credentials = map[string]any{"model_mapping": map[string]any{"model-a": "model-a"}}
			}
			accounts[2].Credentials = map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}
			cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = scenario.loadBatch
			rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 2)
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg,
				usageLogRepo:       accountSchedulingLatencyStub{latencies: map[string]map[int64]float64{"model-a": {1: 1000, 2: 110, 3: 1}}},
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			for _, request := range []struct {
				preferred bool
				excluded  map[int64]struct{}
				want      int64
			}{
				{want: 2},
				{preferred: true, want: 2},
				{preferred: true, excluded: map[int64]struct{}{2: {}}, want: 1},
				{want: 1},
			} {
				accounts[0].Extra = map[string]any{"scheduling_preferred": request.preferred}
				requestCtx := WithFastestFailoverRequestState(ctx)
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(requestCtx, &groupID, "", "session", "model-a", request.excluded, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, scenario.platform)
				if err != nil {
					t.Fatal(err)
				}
				if selection == nil || selection.Account == nil || selection.Account.ID != request.want {
					t.Fatalf("selection = %+v, want account %d", selection, request.want)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				confirmGroupModelSchedulingAccount(requestCtx, "model-a", selection.Account.ID)
			}
		})
	}
}

func TestFastestFailoverFullCurrentChannelWaitsWithoutSwitching(t *testing.T) {
	for _, loadBatch := range []bool{false, true} {
		groupID := int64(103)
		ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: groupID, Platform: PlatformOpenAI, AccountSchedulingMode: AccountSchedulingModeFastestFailover})
		accounts := []Account{
			{ID: 1, Priority: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}},
			{ID: 2, Priority: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}},
		}
		cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
		rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "model-a", 1)
		cfg := &config.Config{}
		cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
		var attempted []int64
		svc := &OpenAIGatewayService{
			accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg,
			concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &attempted, acquireResults: map[int64]bool{1: false, 2: true}}),
		}
		selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "model-a", nil)
		if err != nil || selection == nil || selection.Account.ID != 1 || selection.WaitPlan == nil {
			t.Fatalf("loadBatch=%t: full current channel did not wait: selection=%+v error=%v", loadBatch, selection, err)
		}
		if len(attempted) != 1 || attempted[0] != 1 {
			t.Fatalf("local capacity triggered another channel: %v", attempted)
		}
		if groupModelSchedulingActiveAccount(ctx, cache, &groupID, "model-a") != 1 {
			t.Fatal("waiting changed the current channel")
		}
	}
}
