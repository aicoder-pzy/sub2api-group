//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type prismAdminAccounts struct {
	AccountRepository
	account *Account
}

func (r prismAdminAccounts) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }
func (r prismAdminAccounts) ListByPlatform(context.Context, string) ([]Account, error) {
	return []Account{*r.account}, nil
}

const prismAdminCompletion = `{"id":"resp_prism_test","model":"gpt-5.6-sol","status":"completed","reasoning":{"effort":"medium"},"usage":null,"output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK"}]}]}`

func prismAdminFixture(t *testing.T, endpoint string) *OpenAIGatewayService {
	t.Helper()
	a := bpsTestAccount()
	s := &OpenAIGatewayService{accountRepo: prismAdminAccounts{account: a}, settingService: &SettingService{settingRepo: newStubSettingRepo()}}
	_, err := s.SavePrismAdmin(context.Background(), PrismAdminSettingsInput{Enabled: true, BaseURL: endpoint + "/v1", APIKey: strings.Repeat("k", 32)})
	require.NoError(t, err)
	return s
}

func TestPrismAdminSettingsPrivacyAndEndpoint(t *testing.T) {
	ctx := context.Background()
	s := prismAdminFixture(t, "http://127.0.0.1:8319")
	d, err := s.GetPrismAdmin(ctx)
	require.NoError(t, err)
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(raw), strings.Repeat("k", 32))
	require.NotContains(t, string(raw), "test-token")
	require.NotContains(t, string(raw), "credentials")
	_, err = s.SavePrismAdmin(ctx, PrismAdminSettingsInput{BaseURL: "http://127.0.0.1:8319/v1", Enabled: false})
	require.NoError(t, err)
	v, err := s.prismAdminSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("k", 32), v.APIKey)
	require.False(t, v.Enabled)
	for _, endpoint := range []string{"https://127.0.0.1:8319/v1", "http://localhost:8319/v1", "http://example.com:8319/v1", "http://127.0.0.2:8319/v1", "http://u:p@127.0.0.1:8319/v1", "http://127.0.0.1:8319/v1?x=1", "http://127.0.0.1:8319/v1?", "http://127.0.0.1:8319/v1#x", "http://127.0.0.1:8319/v1/responses", "http://127.0.0.1:80/v1"} {
		_, err := prismAdminBaseURL(endpoint)
		require.Error(t, err, endpoint)
	}
	for _, endpoint := range []string{"http://127.0.0.1:8319/v1", "http://[::1]:8319/v1/"} {
		_, err := prismAdminBaseURL(endpoint)
		require.NoError(t, err)
	}
	for _, key := range []string{"short", strings.Repeat("k", 31) + "\n", strings.Repeat("k", 257)} {
		_, err := s.SavePrismAdmin(ctx, PrismAdminSettingsInput{BaseURL: v.BaseURL, Enabled: true, APIKey: key})
		require.Error(t, err)
	}
}

func TestPrismAdminTransportAndNativeRoutingIsolation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer "+strings.Repeat("k", 32), r.Header.Get("Authorization"))
		require.Equal(t, "test-token", r.Header.Get("X-Prism-OAuth-Token"))
		require.Equal(t, "23", r.Header.Get("X-Prism-Account-ID"))
		require.Empty(t, r.Header.Get("X-Prism-Session-ID"), "admin tests always create a fresh project")
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get(openAICodexTurnStateHeader))
		raw, _ := io.ReadAll(r.Body)
		require.JSONEq(t, `{"model":"gpt-5.6-sol","input":"hi","stream":false,"reasoning":{"effort":"medium"}}`, string(raw))
		_, _ = io.WriteString(w, prismAdminCompletion)
	}))
	defer server.Close()
	s := prismAdminFixture(t, server.URL)
	a := s.accountRepo.(prismAdminAccounts).account
	before, _ := json.Marshal(a)
	result, err := s.TestPrismAdmin(context.Background(), 23, PrismAdminTestInput{Model: "gpt-5.6-sol", Effort: "medium", Prompt: "hi"})
	require.NoError(t, err)
	require.True(t, result.Success)
	require.False(t, result.UsageAvailable)
	require.Equal(t, "OK", result.Text)
	after, _ := json.Marshal(a)
	require.Equal(t, before, after)
	// BPS is enabled for astra in this fixture. Other native models remain native.
	s.httpUpstream = bpsTicketHTTPStub{send: func(r *http.Request, _ string) (*http.Response, error) {
		require.Equal(t, "chatgpt.com", r.URL.Host)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("native"))}, nil
	}}
	r, _ := http.NewRequest(http.MethodPost, chatgptCodexURL, strings.NewReader(`{"model":"gpt-5.6-sol","input":"hi"}`))
	resp, err := s.doOpenAIUpstream(r, "", a)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.EqualValues(t, 1, calls.Load())
}

func TestPrismAdminNoReplayRedirectOrErrorLeaks(t *testing.T) {
	var destinationCalls, calls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	status := http.StatusForbidden
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"type":"secret-token","message":"private prompt and password"}}`)
	}))
	defer server.Close()
	s := prismAdminFixture(t, server.URL)
	input := PrismAdminTestInput{Model: "gpt-5.6-sol", Effort: "medium", Prompt: "hi"}
	for _, code := range []int{403, 302, 500} {
		status = code
		_, err := s.TestPrismAdmin(context.Background(), 23, input)
		require.Equal(t, 502, infraerrors.Code(err))
		require.NotContains(t, err.Error(), "secret-token")
		require.NotContains(t, err.Error(), "private prompt")
	}
	require.EqualValues(t, 3, calls.Load())
	require.Zero(t, destinationCalls.Load())
}

func TestPrismAdminTerminalAndAdmission(t *testing.T) {
	input := PrismAdminTestInput{Model: "gpt-5.6-sol", Effort: "medium", Prompt: "hi"}
	for _, raw := range []string{
		`{}`, strings.ReplaceAll(prismAdminCompletion, "gpt-5.6-sol", "gpt-6-luna"),
		strings.ReplaceAll(prismAdminCompletion, "medium", "low"), strings.ReplaceAll(prismAdminCompletion, "completed", "in_progress"),
		strings.ReplaceAll(prismAdminCompletion, `"type":"message"`, `"type":"function_call"`),
		strings.ReplaceAll(prismAdminCompletion, `"text":"OK"`, `"text":""`),
	} {
		_, err := prismAdminResponse([]byte(raw), input)
		require.Error(t, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, prismAdminCompletion)
	}))
	defer server.Close()
	defer close(release)
	s := prismAdminFixture(t, server.URL)
	done := make(chan error, 1)
	go func() { _, err := s.TestPrismAdmin(context.Background(), 23, input); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("test exited before reaching adapter: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("test did not reach adapter")
	}
	_, err := s.TestPrismAdmin(context.Background(), 23, input)
	require.Equal(t, 429, infraerrors.Code(err))
	release <- struct{}{}
	require.NoError(t, <-done)
	input.Model = "gpt-6-astra"
	_, err = s.TestPrismAdmin(context.Background(), 23, input)
	require.Equal(t, 400, infraerrors.Code(err))
}
