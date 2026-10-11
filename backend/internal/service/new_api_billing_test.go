package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewAPIBillingLiveGroupSyncAndRateProtection(t *testing.T) {
	for _, sync := range []bool{false, true} {
		s, repo, upstream, accounts := newAPIConfigFixture()
		_, err := s.SaveNewAPIConfig(context.Background(), 1, NewAPIConfigRequest{UserID: 7, AccessToken: "management-secret", AccountIDs: []int64{1}})
		require.NoError(t, err)
		repo.bindings[1].Group = "old-group"
		account := accounts.accounts[1]
		initial := 1.0
		account.RateMultiplier = &initial
		account.Extra = map[string]any{UpstreamBillingProbeEnabledExtraKey: true, UpstreamBillingRateSyncEnabledExtraKey: sync}
		upstream.bodies["GET /api/user/self/groups"] = `{"success":true,"data":{"vip":{"ratio":0.2}}}`
		upstream.check = func(req *http.Request) {
			require.Equal(t, "GET", req.Method)
			require.Equal(t, "Bearer management-secret", req.Header.Get("Authorization"))
			require.Equal(t, "7", req.Header.Get("New-Api-User"))
			require.NotContains(t, req.URL.Path, "/responses")
		}
		snapshot, err := s.ProbeAccount(context.Background(), 1)
		require.NoError(t, err)
		require.Equal(t, "ok", snapshot.Status)
		require.Equal(t, "newapi.key_billing", snapshot.Data["object"])
		require.Equal(t, "vip", snapshot.Data["group_name"])
		require.Equal(t, 0.2, snapshot.Data["resolved_rate_multiplier"])
		if sync {
			require.Equal(t, 0.2, *account.RateMultiplier)
		} else {
			require.Equal(t, 1.0, *account.RateMultiplier)
		}
		limit := 0.2
		veto, _ := accountRateLimitVeto(&limit, account, time.Now())
		require.False(t, veto)
		upstream.bodies["GET /api/user/self/groups"] = `{"success":true,"data":{"vip":{"ratio":0.3}}}`
		_, err = s.ProbeAccount(context.Background(), 1)
		require.NoError(t, err)
		veto, reason := accountRateLimitVeto(&limit, account, time.Now())
		require.True(t, veto)
		require.Equal(t, accountRateLimitExceeded, reason)
		// A transport failure keeps cost and prior evidence only until its expiry.
		delete(upstream.bodies, "GET /api/user/self/groups")
		failed, err := s.ProbeAccount(context.Background(), 1)
		require.NoError(t, err)
		require.Equal(t, "failed", failed.Status)
		require.Equal(t, 0.3, failed.Data["resolved_rate_multiplier"])
		_, known := accountRateForLimit(account, failed.FreshUntil.Add(time.Second))
		require.False(t, known)
		raw, err := json.Marshal(failed)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "management-secret")
		require.NotContains(t, string(raw), "encrypted-")
		upstream.bodies["GET /api/token/?p=0&page_size=100"] = `{"success":true,"data":{"items":[{"id":3,"key":"abcd**********wxyz","group":"auto"}],"total":1,"p":0,"page_size":100}}`
		dynamic, err := s.ProbeAccount(context.Background(), 1)
		require.NoError(t, err)
		require.Equal(t, "dynamic_token_group", dynamic.LastError)
		_, known = accountRateForLimit(account, time.Now())
		require.False(t, known, "dynamic groups invalidate even fresh prior evidence")
	}
}

func TestNewAPIBillingRejectsUnprovableRates(t *testing.T) {
	for _, tt := range []struct{ name, token, groups string }{
		{"missing", `"group":"vip"`, `{"vip":{}}`},
		{"negative", `"group":"vip"`, `{"vip":{"ratio":-1}}`},
		{"string", `"group":"vip"`, `{"vip":{"ratio":"0.2"}}`},
		{"unknown", `"group":"other"`, `{"vip":{"ratio":0.2}}`},
		{"auto", `"group":"auto"`, `{"auto":{"ratio":0.01}}`},
		{"cross_group", `"group":"vip","cross_group_retry":true`, `{"vip":{"ratio":0.2}}`},
		{"routes", `"group":"vip","route_groups":["other"]`, `{"vip":{"ratio":0.2}}`},
		{"not_owned", `"group":"vip","key":"foreign"`, `{"vip":{"ratio":0.2}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _, upstream, accounts := newAPIConfigFixture()
			_, err := s.SaveNewAPIConfig(context.Background(), 1, NewAPIConfigRequest{UserID: 7, AccessToken: "secret", AccountIDs: []int64{1}})
			require.NoError(t, err)
			upstream.bodies["GET /api/token/?p=0&page_size=100"] = `{"success":true,"data":{"items":[{"id":3,"key":"abcd**********wxyz",` + tt.token + `}],"total":1,"p":0,"page_size":100}}`
			upstream.bodies["GET /api/user/self/groups"] = `{"success":true,"data":` + tt.groups + `}`
			snapshot, err := s.ProbeAccount(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, "failed", snapshot.Status)
			require.Nil(t, snapshot.SyncedRateMultiplier)
			_, known := accountRateForLimit(accounts.accounts[1], time.Now())
			require.False(t, known)
		})
	}
}
