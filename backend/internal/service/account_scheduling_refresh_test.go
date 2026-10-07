package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type schedulingRefreshRepo struct {
	AccountRepository
	accounts []Account
	cooled   []int64
}

func (r *schedulingRefreshRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("missing account")
}
func (r *schedulingRefreshRepo) ListByGroup(_ context.Context, id int64) ([]Account, error) {
	var result []Account
	for _, a := range r.accounts {
		for _, g := range a.AccountGroups {
			if g.GroupID == id {
				result = append(result, a)
			}
		}
	}
	return result, nil
}
func (r *schedulingRefreshRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, id int64, _ []string) ([]Account, error) {
	return r.ListByGroup(ctx, id)
}
func (r *schedulingRefreshRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, _ string) ([]Account, error) {
	return r.ListByGroup(ctx, id)
}

func (r *schedulingRefreshRepo) SetModelRateLimit(_ context.Context, id int64, _ string, _ time.Time, _ ...string) error {
	r.cooled = append(r.cooled, id)
	return nil
}

type schedulingRefreshGroups struct {
	GroupRepository
	group *Group
}

func (r schedulingRefreshGroups) GetByIDLite(context.Context, int64) (*Group, error) {
	return r.group, nil
}

type schedulingProbeHTTP struct {
	HTTPUpstream
	calls []int64
}

func (u *schedulingProbeHTTP) DoWithTLS(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls = append(u.calls, id)
	req.Header.Set("X-Test-Account", fmt.Sprint(id))
	return http.DefaultClient.Do(req)
}

func TestRefreshGroupSchedulingActiveProbesAndAtomicBinding(t *testing.T) {
	for _, scenario := range []string{"success", "all_failed", "empty", "truncated", "cancel", "paused_after_test", "write_failure", "failed_preferred"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "all_failed" || (scenario == "failed_preferred" && r.Header.Get("X-Test-Account") == "1") {
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, anthropicTimeoutPrelude)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Errorf("flush upstream prelude: %v", err)
				}
				if r.Header.Get("X-Test-Account") == "1" {
					time.Sleep(40 * time.Millisecond)
				}
				if scenario != "empty" {
					fmt.Fprint(w, anthropicTimeoutText)
				}
				if scenario != "truncated" {
					fmt.Fprint(w, anthropicTimeoutEnd)
				}
			}))
			defer server.Close()
			groupID := int64(71)
			repo := &schedulingRefreshRepo{}
			for id := int64(1); id <= 5; id++ {
				repo.accounts = append(repo.accounts, Account{ID: id, Name: fmt.Sprint(id), Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: id != 3, Concurrency: 1,
					Credentials: map[string]any{"api_key": "local-stub", "base_url": server.URL}, GroupIDs: []int64{groupID}, AccountGroups: []AccountGroup{{GroupID: groupID}}})
			}
			repo.accounts[3].Credentials["model_mapping"] = map[string]any{"other": "other"}
			repo.accounts[4].AccountGroups = []AccountGroup{{GroupID: 72}}
			if scenario == "failed_preferred" {
				repo.accounts[0].Extra = map[string]any{"scheduling_preferred": true}
			}
			group := &Group{Hydrated: true, ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, AccountSchedulingMode: AccountSchedulingModeFastestFailover}
			cache := &refreshWriteCache{accountSchedulingCacheStub: accountSchedulingCacheStub{bindings: map[string]int64{groupModelSchedulingStickyKey("model-a"): 1, groupModelSchedulingStickyKey("other"): 99}}, fail: scenario == "write_failure"}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			upstream := &schedulingProbeHTTP{}
			tests := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: cfg}
			svc := &GatewayService{accountRepo: repo, groupRepo: schedulingRefreshGroups{group: group}, cache: cache, cfg: cfg}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []SchedulingRefreshEvent
			err := svc.RefreshGroupScheduling(ctx, tests, groupID, "model-a", func(event SchedulingRefreshEvent) {
				events = append(events, event)
				if event.Type != "complete" {
					require.Equal(t, int64(1), cache.bindings[groupModelSchedulingStickyKey("model-a")])
				}
				if event.Type == "result" && event.AccountID == 1 && scenario == "cancel" {
					cancel()
				}
				if event.Type == "result" && event.AccountID == 2 && scenario == "paused_after_test" {
					repo.accounts[0].Schedulable = false
					repo.accounts[1].Schedulable = false
				}
			})
			if scenario == "success" || scenario == "failed_preferred" {
				require.NoError(t, err, "%+v", events)
				require.Equal(t, int64(2), cache.bindings[groupModelSchedulingStickyKey("model-a")])
				require.Equal(t, "complete", events[len(events)-1].Type)
				require.Equal(t, 4, events[0].Total)
			} else {
				require.Error(t, err)
				require.Equal(t, int64(1), cache.bindings[groupModelSchedulingStickyKey("model-a")])
			}
			if scenario == "cancel" {
				require.Equal(t, []int64{1}, upstream.calls)
			} else {
				require.Equal(t, []int64{1, 2}, upstream.calls)
			}
			if scenario == "failed_preferred" {
				require.Equal(t, []int64{1}, repo.cooled)
			}
			require.Equal(t, int64(99), cache.bindings[groupModelSchedulingStickyKey("other")])
		})
	}
}

type refreshWriteCache struct {
	accountSchedulingCacheStub
	fail bool
}

func (c *refreshWriteCache) SetSessionAccountID(ctx context.Context, group int64, key string, id int64, ttl time.Duration) error {
	if c.fail {
		return errors.New("redis unavailable")
	}
	return c.accountSchedulingCacheStub.SetSessionAccountID(ctx, group, key, id, ttl)
}

func TestSchedulingProbeFirstOutputDeadlineCancelsHTTP(t *testing.T) {
	ended := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, anthropicTimeoutPrelude)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush upstream prelude: %v", err)
		}
		<-r.Context().Done()
		close(ended)
	}))
	defer server.Close()
	repo := &schedulingRefreshRepo{accounts: []Account{{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "local", "base_url": server.URL}}}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{accountRepo: repo, httpUpstream: &schedulingProbeHTTP{}, cfg: cfg}
	_, err := svc.probeSchedulingAccount(context.Background(), 1, "model-a", FastestFailoverSettings{1, 10, 1})
	require.Error(t, err)
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not canceled")
	}
}
