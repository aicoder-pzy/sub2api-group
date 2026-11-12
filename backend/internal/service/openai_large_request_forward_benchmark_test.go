package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

type largeRequestBenchmarkUpstream struct{ HTTPUpstream }

func (largeRequestBenchmarkUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	_, err := io.Copy(io.Discard, req.Body)
	_ = req.Body.Close()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6.1-sol\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"))}, err
}

func BenchmarkOpenAILargeAPIKeyForward(b *testing.B) {
	benchmarkOpenAIAPIKeyForward(b, 90<<20)
}

func BenchmarkOpenAIAPIKeyForwardSizes(b *testing.B) {
	for _, mib := range []int{1, 10, 60, 90} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			benchmarkOpenAIAPIKeyForward(b, mib<<20)
		})
	}
}

func benchmarkOpenAIAPIKeyForward(b *testing.B, size int) {
	gin.SetMode(gin.TestMode)
	body := bytes.Replace(largeNativeResponsesBody(size), []byte("gpt-6-astra"), []byte("gpt-6.1-sol"), 1)
	body = append(body[:len(body)-1], []byte(`,"instructions":"Keep compaction_trigger documentation and \u4e2d text unchanged","reasoning":{"effort":"xhigh"}}`)...)
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: largeRequestBenchmarkUpstream{}}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 500, Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example"}}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := svc.Forward(context.Background(), c, account, body)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAILargeNonCompaction(b *testing.B) {
	body := largeNativeResponsesBody(90 << 20)
	body = append(body[:len(body)-1], []byte(`,"instructions":"Keep compaction_trigger documentation and \u4e2d text unchanged"}`)...)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, changed, err := NormalizeCompactionTriggerInputOrder(body)
		if err != nil || changed || !bytes.Equal(out, body) {
			b.Fatalf("non-compaction request changed: %v", err)
		}
	}
}
