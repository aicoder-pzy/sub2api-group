//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The upstream remains open until the client observes a flushed text delta.
// A fully buffered fixture cannot detect a regression in streaming latency.
func TestChatAPIKeyStreamsBeforeUpstreamCompletion(t *testing.T) {
	for _, responses := range []bool{true, false} {
		name := "chat_completions"
		if responses {
			name = "responses"
		}
		t.Run(name, func(t *testing.T) {
			reader, upstream := io.Pipe()
			t.Cleanup(func() { _ = upstream.Close(); _ = reader.Close() })
			transport := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       reader,
			}}
			svc := &OpenAIGatewayService{cfg: structuredInputRetryTestConfig(), httpUpstream: transport}
			account := structuredInputRetryTestAccount()
			account.Extra[openai_compat.ExtraKeyResponsesSupported] = responses
			writer := &ccPingResponseWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan string, 32)}
			c, _ := gin.CreateTestContext(writer)
			body := []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}],"stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			finished := make(chan error, 1)
			go func() {
				_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				finished <- err
			}()
			lead := `{"choices":[{"index":0,"delta":{"content":"First text"},"finish_reason":null}]}`
			terminal := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
			if responses {
				lead = `{"type":"response.output_text.delta","delta":"First text"}`
				terminal = `{"type":"response.completed","response":{"id":"resp_live","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`
			}
			_, err := io.WriteString(upstream, "data: "+lead+"\n\n")
			require.NoError(t, err)
			select {
			case partial := <-writer.flushed:
				require.Contains(t, partial, `"content":"First text"`)
				require.NotContains(t, partial, "[DONE]")
			case <-time.After(5 * time.Second):
				t.Fatal("first text was buffered until completion")
			}
			require.Equal(t, "text/event-stream", transport.lastReq.Header.Get("Accept"))
			require.Equal(t, "identity", transport.lastReq.Header.Get("Accept-Encoding"))
			require.Contains(t, transport.lastReq.Header.Get("Cache-Control"), "no-transform")
			require.Contains(t, writer.Header().Get("Cache-Control"), "no-transform")
			_, err = io.WriteString(upstream, "data: "+terminal+"\n\ndata: [DONE]\n\n")
			require.NoError(t, err)
			require.NoError(t, upstream.Close())
			select {
			case err := <-finished:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not finish")
			}
			require.Len(t, transport.requests, 1)
		})
	}
}
