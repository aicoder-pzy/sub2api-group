//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type assessmentMemoryRepo struct{ record *PelicanAssessmentRecord }

func (r *assessmentMemoryRepo) Get(_ context.Context, hash string) (*PelicanAssessmentRecord, error) {
	if r.record == nil || r.record.Hash != hash {
		return nil, nil
	}
	copy := *r.record
	return &copy, nil
}
func (r *assessmentMemoryRepo) Save(_ context.Context, record *PelicanAssessmentRecord) error {
	copy := *record
	r.record = &copy
	return nil
}
func (r *assessmentMemoryRepo) Finish(ctx context.Context, record *PelicanAssessmentRecord) error {
	if r.record != nil && r.record.TaskID == record.TaskID && r.record.Status == "running" {
		return r.Save(ctx, record)
	}
	return nil
}

type assessmentTransport func(*http.Request) (*http.Response, error)

func (f assessmentTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func assessmentResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPelicanAssessmentHTMLOnlyAndPersistence(t *testing.T) {
	repo := &assessmentMemoryRepo{}
	svc := NewPelicanAssessmentService(repo)
	html := "<!doctype html><html><body><svg><text>pelican</text></svg></body></html>"
	calls := 0
	svc.client.Transport = assessmentTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https", req.URL.Scheme)
		require.Equal(t, "manxue.ai", req.URL.Host)
		require.Empty(t, req.URL.RawQuery)
		require.Nil(t, req.URL.User)
		for _, name := range []string{"Authorization", "Cookie", "X-API-Key", "Proxy-Authorization", "Chatgpt-Account-Id", "Referer"} {
			require.Empty(t, req.Header.Get(name), name)
		}
		if req.Method == http.MethodPost {
			require.Equal(t, "/api/v1/tests", req.URL.Path)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
			require.Equal(t, map[string]any{"benchmark": "pelican", "html": html}, payload)
			require.NotEmpty(t, req.Header.Get("Idempotency-Key"))
			return assessmentResponse(202, `{"id":"safe-task-id","benchmark":"pelican","status":"running"}`), nil
		}
		require.Equal(t, "/api/v1/tests/safe-task-id", req.URL.Path)
		return assessmentResponse(200, `{"id":"safe-task-id","benchmark":"pelican","status":"succeeded","assessment":{"quality":"degraded","reason":"simple artwork","source":"source_classifier:test"}}`), nil
	})
	ctx := context.WithValue(context.Background(), bpsRequestScopeKey{}, "DO_NOT_FORWARD_KEY")
	saved, err := svc.Lookup(ctx, html)
	require.NoError(t, err)
	require.Nil(t, saved)
	require.Zero(t, calls)
	record, err := svc.Start(ctx, html)
	require.NoError(t, err)
	require.Equal(t, "running", record.Status)
	_, err = svc.Start(ctx, html)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "a running task is reused")
	record, err = svc.Poll(ctx, record.Hash)
	require.NoError(t, err)
	require.Equal(t, "degraded", record.Assessment.Quality)
	require.False(t, record.Assessment.CheckedAt.IsZero())
	_, err = svc.Start(ctx, html)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "saved assessments do not upload again")
	public, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(public), "safe-task-id")
	require.NotContains(t, string(public), html)
}

func TestPelicanAssessmentValidationAndRedirects(t *testing.T) {
	svc := NewPelicanAssessmentService(&assessmentMemoryRepo{})
	transport := svc.client.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy)
	require.Nil(t, svc.client.Jar)
	calls := 0
	svc.client.Transport = assessmentTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		response := assessmentResponse(302, "SHOULD_NOT_BE_EXPOSED")
		response.Header.Set("Location", "https://elsewhere.example/collect")
		return response, nil
	})
	for _, html := range []string{"", "21", "<html>" + strings.Repeat("x", PelicanAssessmentMaxHTMLBytes), "<html>\xff"} {
		_, err := svc.Start(context.Background(), html)
		require.Error(t, err)
	}
	require.Zero(t, calls)
	_, err := svc.Start(context.Background(), "<html><svg></svg></html>")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SHOULD_NOT_BE_EXPOSED")
	require.Equal(t, 1, calls)
}

func TestPelicanAssessmentUnknownFailuresAndRetryAfter(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
		quality    string
		failed     bool
	}{
		{"unknown", `{"id":"task","benchmark":"pelican","status":"succeeded","assessment":{"quality":"unknown","reason":"unclassified","source":"local"}}`, 200, "unknown", false},
		{"missing assessment", `{"id":"task","benchmark":"pelican","status":"succeeded"}`, 200, "", true},
		{"wrong benchmark", `{"id":"task","benchmark":"candy","status":"succeeded"}`, 200, "", true},
		{"wrong task", `{"id":"other","benchmark":"pelican","status":"succeeded"}`, 200, "", true},
		{"bad quality", `{"id":"task","benchmark":"pelican","status":"succeeded","assessment":{"quality":"perfect"}}`, 200, "", true},
		{"failed", `{"id":"task","benchmark":"pelican","status":"failed","error":"private upstream message"}`, 200, "", true},
		{"busy", `{}`, 429, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			hash, _ := pelicanSourceHash("<html></html>")
			repo := &assessmentMemoryRepo{record: &PelicanAssessmentRecord{Hash: hash, Status: "running", TaskID: "task", Deadline: time.Now().Add(time.Minute)}}
			svc := NewPelicanAssessmentService(repo)
			svc.client.Transport = assessmentTransport(func(*http.Request) (*http.Response, error) {
				response := assessmentResponse(test.code, test.body)
				response.Header.Set("Retry-After", "17")
				return response, nil
			})
			record, err := svc.Poll(context.Background(), hash)
			require.NoError(t, err)
			if test.failed {
				require.Equal(t, "failed", record.Status)
				require.Nil(t, record.Assessment)
				require.NotContains(t, record.Error, "private upstream message")
			}
			if test.quality != "" {
				require.Equal(t, test.quality, record.Assessment.Quality)
			}
			if test.code == 429 {
				require.Equal(t, "running", record.Status)
				require.Equal(t, 17, record.RetryAfterSeconds)
			}
		})
	}
}

func TestPelicanAssessmentLiveHTML(t *testing.T) {
	if os.Getenv("SUB2API_TEST_MANXUE_HTML") != "1" {
		t.Skip("explicit HTML-only live check")
	}
	svc := NewPelicanAssessmentService(&assessmentMemoryRepo{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	html := `<!doctype html><html><body><svg viewBox="0 0 400 300"><circle cx="150" cy="210" r="45" fill="none" stroke="black"/><circle cx="280" cy="210" r="45" fill="none" stroke="black"/><path d="M150 210L205 140L280 210Z" fill="none" stroke="green"/><ellipse cx="200" cy="100" rx="45" ry="30" fill="white" stroke="black"/><path d="M230 90L290 100L230 110Z" fill="gold"/></svg></body></html>`
	record, err := svc.Start(ctx, html)
	require.NoError(t, err)
	for record.Status == "running" {
		select {
		case <-ctx.Done():
			t.Fatal("HTML-only evaluation timed out")
		case <-time.After(3 * time.Second):
		}
		record, err = svc.Poll(ctx, record.Hash)
		require.NoError(t, err)
	}
	require.Equal(t, "succeeded", record.Status, record.Error)
	require.NotNil(t, record.Assessment)
	t.Logf("HTML-only evaluation: quality=%s source=%s", record.Assessment.Quality, record.Assessment.Source)
}
