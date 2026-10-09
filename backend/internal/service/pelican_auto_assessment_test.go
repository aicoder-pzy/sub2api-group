//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPelicanAutoAssessmentDecoratesOnlyAfterEvaluation(t *testing.T) {
	for _, quality := range []string{"normal", "degraded", "unknown"} {
		t.Run(quality, func(t *testing.T) {
			svc := NewPelicanAssessmentService(&assessmentMemoryRepo{})
			source := "<html><body><svg></svg></body></html>"
			calls := 0
			svc.client.Transport = assessmentTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Empty(t, req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("Cookie"))
				if req.Method == http.MethodPost {
					var payload map[string]any
					require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
					require.Equal(t, map[string]any{"benchmark": "pelican", "html": source}, payload)
					return assessmentResponse(202, `{"id":"auto-test","benchmark":"pelican","status":"running"}`), nil
				}
				return assessmentResponse(200, `{"id":"auto-test","benchmark":"pelican","status":"succeeded","assessment":{"quality":"`+quality+`","reason":"<script>unsafe</script>\"","source":"classifier:test"}}`), nil
			})
			output := svc.AssessOutput(context.Background(), "Answer:\n```html\n"+source+"\n```")
			require.Equal(t, 2, calls)
			require.Contains(t, output, `data-sub2api-quality="`+quality+`"`)
			require.NotContains(t, output, "<script>unsafe</script>")
			require.Equal(t, source, extractPelicanSource(output))
			require.Equal(t, output, svc.AssessOutput(context.Background(), output))
			require.Equal(t, 2, calls, "the original source hash reuses its saved evaluation")
			require.Equal(t, 1, strings.Count(output, "data-sub2api-quality"))
		})
	}
}

func TestPelicanAutoAssessmentUnknownAndNonHTML(t *testing.T) {
	svc := NewPelicanAssessmentService(&assessmentMemoryRepo{})
	calls := 0
	svc.client.Transport = assessmentTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return assessmentResponse(403, "PRIVATE_UPSTREAM_ERROR"), nil
	})
	require.Equal(t, "21", svc.AssessOutput(context.Background(), "21"))
	require.Zero(t, calls)
	output := svc.AssessOutput(context.Background(), "<svg viewBox=\"0 0 400 300\"></svg>")
	require.Contains(t, output, `data-sub2api-quality="unknown"`)
	require.Contains(t, output, "无法判定")
	require.NotContains(t, output, "PRIVATE_UPSTREAM_ERROR")
	require.Contains(t, output, "<html>")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Contains(t, svc.AssessOutput(ctx, "<html><body>saved</body></html>"), `data-sub2api-quality="unknown"`)
	require.Equal(t, 1, calls)
}

func TestPelicanGroupAssessmentReleasesChannelBeforeEvaluating(t *testing.T) {
	plan := groupTestPlan(1)
	router := &groupTestRouterFake{steps: []routeStep{{account: &Account{ID: 42, Name: "channel"}}}}
	svc := &PelicanGroupTestService{router: router, now: func() time.Time { return groupTestNow }}
	svc.runAccount = func(ctx context.Context, _ int64, _ string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		require.Equal(t, true, ctx.Value(pelicanDeferAssessmentKey{}))
		return &ScheduledTestResult{Status: "success", ResponseText: "<html><body>drawing</body></html>", LatencyMs: 1234}, nil
	}
	svc.assessOutput = func(ctx context.Context, source string) string {
		require.Equal(t, []int64{42}, router.released)
		return (*PelicanAssessmentService)(nil).AssessOutput(ctx, source)
	}
	result := svc.runSample(context.Background(), plan, &Group{ID: plan.GroupID})
	require.Equal(t, "success", result.Status)
	require.Equal(t, int64(1234), result.LatencyMs)
	require.Equal(t, int64(42), result.AccountID)
	require.Empty(t, result.Attempts)
	require.Contains(t, result.ResponseText, `data-sub2api-quality="unknown"`)
}

func TestPelicanAutomaticManualAndScheduledPaths(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			account := pelicanClaudeOAuthAccount()
			svc := pelicanClaudeTestService(account, &claudeThinkingUpstream{answerTokens: 100})
			assessment := NewPelicanAssessmentService(&assessmentMemoryRepo{})
			svc.pelicanAssessment = assessment
			assessment.client.Transport = assessmentTransport(func(req *http.Request) (*http.Response, error) {
				require.Empty(t, req.Header.Get("Authorization"), "account OAuth credentials must never reach assessment")
				if req.Method == http.MethodPost {
					return assessmentResponse(202, `{"id":"flow-test","benchmark":"pelican","status":"running"}`), nil
				}
				return assessmentResponse(200, `{"id":"flow-test","benchmark":"pelican","status":"succeeded","assessment":{"quality":"normal","reason":"drawing","source":"classifier:test"}}`), nil
			})
			if background {
				result, err := svc.RunPelicanBackground(context.Background(), account.ID, "claude-opus-5-5", &PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw", ReasoningEffort: "medium"})
				require.NoError(t, err)
				require.Equal(t, "success", result.Status)
				require.Contains(t, result.ResponseText, `data-sub2api-quality="normal"`)
			} else {
				c, recorder := newTestContext()
				require.NoError(t, svc.TestPelicanAccountConnection(c, account.ID, "claude-opus-5-5", "draw", "medium"))
				body := recorder.Body.String()
				require.Less(t, strings.Index(body, `"type":"test_complete"`), strings.Index(body, `"type":"pelican_assessing"`))
				require.Contains(t, body, `"type":"pelican_assessment"`)
				require.Contains(t, body, `data-sub2api-quality=\"normal\"`)
			}
		})
	}
}
