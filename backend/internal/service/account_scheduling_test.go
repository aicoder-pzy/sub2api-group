package service

import (
	"context"
	"encoding/json"
	"math"
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
		{name: "preference takes over binding", preferred: true, latency: 100, pinned: 2, cheapRate: 0.5, want: 1},
		{name: "slow preference takes over", preferred: true, latency: 300, pinned: 2, cheapRate: 0.5, want: 1},
		{name: "slow preference wins initially", preferred: true, latency: 300, cheapRate: 0.5, want: 1},
		{name: "unstable cheap account loses", latency: 100, failures: 30, cheapRate: 0.1, want: 1},
		{name: "healthy binding persists", latency: 100, pinned: 1, cheapRate: 0.1, want: 1},
		{name: "zero rate is valid", latency: 100, cheapRate: 0, want: 2},
		{name: "invalid rate falls back", latency: 100, cheapRate: math.NaN(), want: 1},
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
	if got := fastestFailoverCandidateOrder(ctx, repo, cache, &groupID, "model-a", candidates)[0].ID; got != 1 {
		t.Fatalf("preferred priority selected %d", got)
	}
	delete(repo.quality, 1)
	if got := fastestFailoverCandidateOrder(ctx, repo, nil, &groupID, "model-a", candidates)[0].ID; got != 1 {
		t.Fatalf("manual preference ignored on unmeasured account: %d", got)
	}
	candidates[0].Extra = nil
	candidates[1].Extra = nil
	if got := fastestFailoverCandidateOrder(ctx, nil, nil, &groupID, "new-model", candidates)[0].ID; got != 2 {
		t.Fatalf("cold start ignored lower rate: %d", got)
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
	bindings map[string]int64
}

func (s *accountSchedulingCacheStub) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	return s.bindings[key], nil
}

func (s *accountSchedulingCacheStub) SetSessionAccountID(_ context.Context, _ int64, key string, accountID int64, _ time.Duration) error {
	s.bindings[key] = accountID
	return nil
}

func TestFastestFailoverCandidateOrderUsesRequestedModelLatency(t *testing.T) {
	groupID := int64(3)
	accounts := []*Account{{ID: 1, Priority: 1}, {ID: 2, Priority: 2}}
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
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"old-session": 3}}
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
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", request.session, "model-a", request.excluded, OpenAIUpstreamTransportAny, false)
				if err != nil {
					t.Fatal(err)
				}
				if selection == nil || selection.Account == nil || selection.Account.ID != request.want {
					t.Fatalf("session %q: selection = %+v, want account %d", request.session, selection, request.want)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			}
		})
	}
}

func TestFastestFailoverPreferenceTakesOverAcrossSchedulerPaths(t *testing.T) {
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
			cache := &schedulerTestGatewayCache{sessionBindings: make(map[string]int64)}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = scenario.loadBatch
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
				{preferred: true, want: 1},
				{preferred: true, excluded: map[int64]struct{}{1: {}}, want: 2},
			} {
				accounts[0].Extra = map[string]any{"scheduling_preferred": request.preferred}
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "session", "model-a", request.excluded, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, scenario.platform)
				if err != nil {
					t.Fatal(err)
				}
				if selection == nil || selection.Account == nil || selection.Account.ID != request.want {
					t.Fatalf("selection = %+v, want account %d", selection, request.want)
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			}
		})
	}
}
