package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bpsTicketHTTPStub struct {
	HTTPUpstream
	send func(*http.Request, string) (*http.Response, error)
}

func (s bpsTicketHTTPStub) Do(r *http.Request, p string, _ int64, _ int) (*http.Response, error) {
	return s.send(r, p)
}

func bpsTestAccount() *Account {
	return &Account{ID: 23, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account", "plan_type": "plus"},
		Extra:       map[string]any{bpsTicketAccountKey: BPSTicketAccountConfig{BPS: true, Models: []string{"gpt-6-astra"}, ProxySource: "account"}}}
}

func bpsTestState(length int, issued time.Time, marker byte) string {
	blocks := map[int]int{780: 33, 292: 10, 332: 12}[length]
	raw := make([]byte, 57+16*blocks)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
	raw[9] = marker
	return base64.URLEncoding.EncodeToString(raw)
}

func bpsTestCookies() []string {
	raw, _ := json.Marshal(map[string]any{"exp": time.Now().Add(3 * time.Minute).Unix(), "route": "chat.gateway.unified-88.api.openai.com"})
	return []string{"__cflb=route-1", "__oailb=e30." + base64.RawURLEncoding.EncodeToString(raw) + ".signature"}
}

const bpsTestCompletion = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bps\",\"model\":\"gpt-6-astra\",\"status\":\"completed\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}],\"usage\":{\"input_tokens\":12,\"output_tokens\":2}}}\n\n"

func TestBPSTicketTransportIsolationAndCompletion(t *testing.T) {
	a := bpsTestAccount()
	calls := 0
	s := &OpenAIGatewayService{httpUpstream: bpsTicketHTTPStub{send: func(r *http.Request, _ string) (*http.Response, error) {
		calls++
		require.Equal(t, "https://bps.openai.com/basispoints/api/responses", r.URL.String())
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.Equal(t, "test-account", r.Header.Get("Chatgpt-Account-Id"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get(openAICodexTurnStateHeader))
		require.Equal(t, "basispoints-excel-plugin", r.Header.Get("X-Openai-Internal-Basispoints-Client-Product"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "gpt-6-astra")
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(bpsTestCompletion))}, nil
	}}}
	req, err := http.NewRequest(http.MethodPost, chatgptCodexURL, strings.NewReader(`{"model":"gpt-6-astra","input":"Hello","stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Chatgpt-Account-Id", "test-account")
	req.Header.Set("Cookie", "secret=native")
	req.Header.Set(openAICodexTurnStateHeader, "native-ticket")
	resp, err := s.doOpenAIUpstream(req, "", a)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), "response.completed")
	require.Contains(t, string(raw), `"input_tokens":12`)
	require.Equal(t, 1, calls)
	// The original request remains native; no global header or account mutation.
	require.Equal(t, "chatgpt.com", req.URL.Host)
	require.Equal(t, "secret=native", req.Header.Get("Cookie"))
}

func TestBPSTicketIncompleteStreamNeverReportsCompletionOrReplays(t *testing.T) {
	a := bpsTestAccount()
	calls := 0
	s := &OpenAIGatewayService{httpUpstream: bpsTicketHTTPStub{send: func(*http.Request, string) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"))}, nil
	}}}
	req, _ := http.NewRequest(http.MethodPost, chatgptCodexURL, strings.NewReader(`{"model":"gpt-6-astra","input":"Hello","stream":true}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Chatgpt-Account-Id", "test-account")
	resp, err := s.doOpenAIUpstream(req, "", a)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.Error(t, err)
	require.NotContains(t, string(raw), "response.completed")
	require.Equal(t, 1, calls)
}

func TestBPSTicketModelsPlansAndManualOverride(t *testing.T) {
	a := bpsTestAccount()
	require.True(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"))
	require.False(t, a.IsExcelBPSEnabledForModel("other"))
	a.Credentials["plan_type"] = " FREE "
	require.False(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"))
	a.Credentials["plan_type"] = "team"
	require.Equal(t, 332, codexTicketExpectedLength(a, BPSTicketSettings{TargetLength: 292}))
	a.Credentials["plan_type"] = "plus"
	cfg := a.BPSTicketConfig()
	cfg.BPS = false
	cfg.AutoProbe = true
	cfg.AutoSwitch = true
	a.Extra[bpsTicketAccountKey] = cfg
	state := a.BPSTicketState()
	state.Models["gpt-6-astra"] = BPSTicketModelState{AutoBPS: true}
	a.Extra[bpsTicketStateKey] = state
	require.True(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"))
	cfg.AutoSwitch = false
	a.Extra[bpsTicketAccountKey] = cfg
	require.False(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"), "manual edit invalidates automatic override")
	cfg.BPS = true
	a.Extra[bpsTicketAccountKey] = cfg
	require.True(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"), "manual BPS is independent of automatic state")
}

func TestBPSTicketProbeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mint, cont string
		cookies    []string
		streamErr  error
		want       OpenAICodexStateVerdict
	}{
		{"same", bpsTestState(780, time.Now(), 1), bpsTestState(780, time.Now(), 1), nil, nil, OpenAICodexStateHealthy},
		{"new", bpsTestState(780, time.Now(), 1), bpsTestState(780, time.Now(), 2), nil, nil, OpenAICodexStateDegraded},
		{"lite", bpsTestState(292, time.Now(), 1), "", nil, nil, OpenAICodexStateInconclusive},
		{"team_lite", bpsTestState(332, time.Now(), 1), "", nil, nil, OpenAICodexStateInconclusive},
		{"expired", bpsTestState(780, time.Now().Add(-time.Hour), 1), "", nil, nil, OpenAICodexStateInconclusive},
		{"wrong_continuation", bpsTestState(780, time.Now(), 1), bpsTestState(292, time.Now(), 2), nil, nil, OpenAICodexStateInconclusive},
		{"route_changed", bpsTestState(780, time.Now(), 1), bpsTestState(780, time.Now(), 2), []string{"__cflb=route-2"}, nil, OpenAICodexStateInconclusive},
		{"incomplete", bpsTestState(780, time.Now(), 1), bpsTestState(780, time.Now(), 2), nil, io.ErrUnexpectedEOF, OpenAICodexStateInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &OpenAICodexStateProbeResult{}
			calls := 0
			cookies := bpsTestCookies()
			runOpenAICodexStateProbe(context.Background(), result, "", func(state, cookie string) (openAICodexStateShot, error) {
				calls++
				if calls == 1 {
					return openAICodexStateShot{status: 200, state: tc.mint, cookies: cookies}, nil
				}
				require.Equal(t, tc.mint, state)
				require.Equal(t, strings.Join(cookies, "; "), cookie)
				return openAICodexStateShot{status: 200, state: tc.cont, cookies: tc.cookies, streamErr: tc.streamErr}, nil
			})
			require.Equal(t, tc.want, result.Verdict)
		})
	}
}

func TestBPSTicketUnknownNeverChangesAutomaticRouting(t *testing.T) {
	cfg := DefaultBPSTicketSettings()
	state := BPSTicketModelState{}
	result := func(v OpenAICodexStateVerdict) *OpenAICodexStateProbeResult {
		return &OpenAICodexStateProbeResult{Verdict: v}
	}
	state = advanceBPSTicketState(state, result(OpenAICodexStateDegraded), cfg, true)
	require.False(t, state.AutoBPS)
	state = advanceBPSTicketState(state, result(OpenAICodexStateInconclusive), cfg, true)
	require.False(t, state.AutoBPS)
	require.Zero(t, state.DegradedCount)
	for i := 0; i < 2; i++ {
		state = advanceBPSTicketState(state, result(OpenAICodexStateDegraded), cfg, true)
	}
	require.True(t, state.AutoBPS)
	state = advanceBPSTicketState(state, result(OpenAICodexStateInconclusive), cfg, true)
	require.True(t, state.AutoBPS)
	for i := 0; i < 2; i++ {
		state = advanceBPSTicketState(state, result(OpenAICodexStateHealthy), cfg, true)
	}
	require.False(t, state.AutoBPS)
}

func TestBPSTicketRateLimitKeepsNativeRouteUsable(t *testing.T) {
	a := bpsTestAccount()
	calls := 0
	s := &OpenAIGatewayService{httpUpstream: bpsTicketHTTPStub{send: func(req *http.Request, _ string) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader("do not echo sensitive upstream text"))}, nil
	}}}
	request := func(model string) *http.Request {
		req, _ := http.NewRequest("POST", chatgptCodexURL, strings.NewReader(`{"model":`+jsonString(model)+`,"input":"OK"}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Chatgpt-Account-Id", "test-account")
		return req
	}
	for i := 0; i < 2; i++ {
		resp, handled, err := s.routeBPSTicketHTTP(request("gpt-6-astra"), a)
		require.True(t, handled)
		require.NoError(t, err)
		require.Equal(t, 503, resp.StatusCode)
		raw, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		_ = resp.Body.Close()
		require.NotContains(t, string(raw), "sensitive upstream")
	}
	require.Equal(t, 1, calls, "a BPS cooldown must not hit upstream again")
	_, handled, err := s.routeBPSTicketHTTP(request("gpt-5.6-sol"), a)
	require.NoError(t, err)
	require.False(t, handled, "another model must retain its native route")
}

func TestBPSTicketMappedModelsUseOutboundIdentity(t *testing.T) {
	a := bpsTestAccount()
	a.Credentials["model_mapping"] = map[string]any{"client-alias": "gpt-6-astra"}
	cfg := a.BPSTicketConfig()
	cfg.Models = []string{"client-alias"}
	cfg.BPS = false
	cfg.AutoProbe = true
	cfg.AutoSwitch = true
	a.Extra[bpsTicketAccountKey] = cfg
	state := a.BPSTicketState()
	state.Models["gpt-6-astra"] = BPSTicketModelState{AutoBPS: true}
	a.Extra[bpsTicketStateKey] = state
	require.True(t, a.IsExcelBPSEnabledForModel("client-alias"))
	require.True(t, a.IsExcelBPSEnabledForModel("gpt-6-astra"))
	require.False(t, a.IsExcelBPSEnabledForModel("another-model"))
}

func TestBPSTicketFailClosedAndScopedCredential(t *testing.T) {
	a := bpsTestAccount()
	cfg := DefaultBPSTicketSettings()
	cfg.HarvestEnabled = true
	cfg.FailClosed = true
	ac := a.BPSTicketConfig()
	ac.BPS = false
	ac.Tickets = true
	a.Extra[bpsTicketAccountKey] = ac
	key := codexTicketKey(a, "gpt-6-astra")
	state := bpsTestState(780, time.Now(), 1)
	s := &OpenAIGatewayService{bpsTickets: bpsTicketRuntime{settings: cfg, settingsAt: time.Now()}, httpUpstream: bpsTicketHTTPStub{send: func(req *http.Request, _ string) (*http.Response, error) {
		require.Equal(t, state, req.Header.Get(openAICodexTurnStateHeader))
		require.Contains(t, req.Header.Get("Cookie"), "__cflb=")
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(bpsTestCompletion))}, nil
	}}}
	request := func() *http.Request {
		r, _ := http.NewRequest("POST", chatgptCodexURL, strings.NewReader(`{"model":"gpt-6-astra","input":"OK","stream":true}`))
		return r
	}
	resp, handled, err := s.routeBPSTicketHTTP(request(), a)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 503, resp.StatusCode)
	_ = resp.Body.Close()
	s.bpsTickets.tickets = map[string]*codexTicketCredential{key: {state: state, cookies: bpsTestCookies(), expires: time.Now().Add(time.Minute)}}
	resp, handled, err = s.routeBPSTicketHTTP(request(), a)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 200, resp.StatusCode)
	_ = resp.Body.Close()
	a.Credentials["access_token"] = "rotated"
	resp, _, err = s.routeBPSTicketHTTP(request(), a)
	require.NoError(t, err)
	require.Equal(t, 503, resp.StatusCode)
	_ = resp.Body.Close()
}

func TestBPSTicketMintChecksStreamModelAndRoute(t *testing.T) {
	a := bpsTestAccount()
	cfg := DefaultBPSTicketSettings()
	cfg.TargetGateway = "unified-88"
	for _, status := range []int{200, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := &OpenAIGatewayService{httpUpstream: bpsTicketHTTPStub{send: func(req *http.Request, _ string) (*http.Response, error) {
				require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
				require.Empty(t, req.Header.Get("Cookie"))
				require.Empty(t, req.Header.Get(responsesLiteHeaderKey))
				headers := http.Header{}
				headers.Set(openAICodexTurnStateHeader, bpsTestState(780, time.Now(), 1))
				for _, c := range bpsTestCookies() {
					headers.Add("Set-Cookie", c+"; Path=/")
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(bpsTestCompletion))}, nil
			}}}
			ticket, code, err := s.mintBPSTicket(context.Background(), a, "test-token", "gpt-6-astra", "", cfg)
			require.Equal(t, status, code)
			if status == 200 {
				require.NoError(t, err)
				require.Len(t, ticket.state, 780)
			} else {
				require.Error(t, err)
				require.Nil(t, ticket)
			}
		})
	}
}
