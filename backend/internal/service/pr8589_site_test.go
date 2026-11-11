package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

func pr8589SiteFixture(t *testing.T, model, mode, identity string, single, disabled bool) (*OpenAIGatewayService, context.Context, int64, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	first := []byte(fmt.Sprintf(`{"model":%q,"input":[{"role":"system","content":"Be helpful."},{"role":"user","content":"Look up the weather"}]}`, model))
	next := []byte(fmt.Sprintf(`{"model":%q,"input":[{"role":"system","content":"Be helpful."},{"role":"user","content":"Look up the weather"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"Sunny"},{"role":"developer","content":"Use the tool result."},{"role":"user","content":"And tomorrow?"}]}`, model))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	switch identity {
	case "header":
		c.Request.Header.Set("session-id", "synthetic-stable-conversation")
	case "prompt_cache_key":
		var err error
		first, err = sjson.SetBytes(first, "prompt_cache_key", "synthetic-stable-conversation")
		require.NoError(t, err)
		next, err = sjson.SetBytes(next, "prompt_cache_key", "synthetic-stable-conversation")
		require.NoError(t, err)
	}
	groupID := int64(24)
	ctx := context.WithValue(context.Background(), ctxkey.Model, model)
	if mode == "smart_channel" {
		ctx = WithFastestFailoverRequestState(context.WithValue(ctx, ctxkey.Group,
			&Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
				AccountSchedulingMode: AccountSchedulingModeSmartChannel}))
	}
	advanced := "false"
	if mode == "advanced" {
		advanced = "true"
	}
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
			Schedulable: !disabled, Concurrency: 1, Priority: 100, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"model_mapping": map[string]any{model: model}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
			Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"model_mapping": map[string]any{model: model}}},
	}
	if single {
		accounts[1].Credentials = map[string]any{"model_mapping": map[string]any{"different-model": "different-model"}}
	}
	bindings := make(map[string]int64)
	cache := &accountSchedulingCacheStub{bindings: bindings,
		GatewayCache: &schedulerTestGatewayCache{sessionBindings: bindings}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 1
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache,
		cfg: cfg, settingService: &SettingService{settingRepo: &panelRateLimitSettingRepo{}},
		rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	firstHash, nextHash := svc.GenerateSessionHash(c, first), svc.GenerateSessionHash(c, next)
	require.NotEmpty(t, firstHash)
	require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, firstHash, 1, time.Hour))
	if mode == "smart_channel" {
		rememberGroupModelSchedulingAccount(ctx, cache, &groupID, model, 1)
	}
	if identity != "content" {
		require.Equal(t, firstHash, nextHash, "explicit identity must retain precedence")
	}
	return svc, ctx, groupID, firstHash, nextHash
}

func TestPR8589SiteAPIKeyBinding(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-astra"} {
		for _, mode := range []string{"legacy", "advanced", "smart_channel"} {
			for _, identity := range []string{"content", "header", "prompt_cache_key"} {
				t.Run(model+"/"+mode+"/"+identity, func(t *testing.T) {
					svc, ctx, groupID, firstHash, nextHash := pr8589SiteFixture(t, model, mode, identity, false, false)
					selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", nextHash, model, nil, OpenAIUpstreamTransportAny, false)
					require.NoError(t, err)
					require.NotNil(t, selection)
					if selection.ReleaseFunc != nil {
						defer selection.ReleaseFunc()
					}
					t.Logf("seed_changed=%t selected_account=%d layer=%s", firstHash != nextHash, selection.Account.ID, decision.Layer)
					require.EqualValues(t, 1, selection.Account.ID, "healthy existing binding must survive the reminder")
				})
			}
		}
	}
}

func TestPR8589SiteSingleChannel(t *testing.T) {
	svc, ctx, groupID, firstHash, nextHash := pr8589SiteFixture(t, "gpt-6.1-sol", "smart_channel", "content", true, false)
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", nextHash, "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	t.Logf("seed_changed=%t selected_account=%d", firstHash != nextHash, selection.Account.ID)
	require.EqualValues(t, 1, selection.Account.ID, "undeclared backup cannot win")
}

func TestPR8589SiteDisabledAccountStillExcluded(t *testing.T) {
	for _, mode := range []string{"legacy", "advanced", "smart_channel"} {
		t.Run(mode, func(t *testing.T) {
			svc, ctx, groupID, _, nextHash := pr8589SiteFixture(t, "gpt-6.1-sol", mode, "header", false, true)
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", nextHash, "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			require.EqualValues(t, 2, selection.Account.ID, "stable identity must not bypass a disabled switch")
		})
	}
}
