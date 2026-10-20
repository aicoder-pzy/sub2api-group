package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const openAIWhitespaceOnlyPrelude = `data: {"type":"response.created","response":{"id":"resp_whitespace"}}

data: {"type":"response.output_item.added","item":{"type":"message","content":[{"type":"output_text","text":" "}]}}

data: {"type":"response.content_part.added","part":{"type":"output_text","text":"\t"}}

data: {"type":"response.output_text.delta","delta":" \n"}

data: {"type":"response.output_text.done","text":" \n\t"}

data: {"type":"response.content_part.done","part":{"type":"output_text","text":" "}}

data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":" "}]}}

`

const openAIWhitespaceOverload = `data: {"type":"response.failed","response":{"id":"resp_whitespace","status":"failed","error":{"code":"server_error","message":"Our servers are currently overloaded. Please try again later."},"output":[{"type":"message","content":[{"type":"output_text","text":" "}]}]}}

`

func TestOpenAIStreamWhitespaceDoesNotStartClientOutput(t *testing.T) {
	for _, kind := range []string{"output_text", "reasoning_text", "reasoning_summary_text", "refusal"} {
		for _, text := range []string{"", " ", "\\t\\r\\n", "\\u3000"} {
			event := "response." + kind + ".delta"
			require.False(t, openAIStreamDataStartsClientOutput(`{"type":"`+event+`","delta":"`+text+`"}`, event))
			event = "response." + kind + ".done"
			field := "text"
			if kind == "refusal" {
				field = "refusal"
			}
			require.False(t, openAIStreamDataStartsClientOutput(`{"type":"`+event+`","`+field+`":"`+text+`"}`, event))
		}
		event := "response." + kind + ".delta"
		require.True(t, openAIStreamDataStartsClientOutput(`{"delta":" x "}`, event))
	}
	for _, phase := range []string{"added", "done"} {
		for _, text := range []string{"", " ", "\\t\\n"} {
			for _, kind := range []string{"output_text", "refusal"} {
				field := "text"
				if kind == "refusal" {
					field = "refusal"
				}
				part := `{"type":"` + kind + `","` + field + `":"` + text + `"}`
				require.False(t, openAIStreamDataStartsClientOutput(`{"part":`+part+`}`, "response.content_part."+phase))
				require.False(t, openAIStreamDataStartsClientOutput(`{"item":{"type":"message","content":[`+part+`]}}`, "response.output_item."+phase))
			}
			part := `{"type":"summary_text","text":"` + text + `"}`
			require.False(t, openAIStreamDataStartsClientOutput(`{"part":`+part+`}`, "response.reasoning_summary_part."+phase))
			require.False(t, openAIStreamDataStartsClientOutput(`{"item":{"type":"reasoning","summary":[`+part+`]}}`, "response.output_item."+phase))
		}
	}
	// Tool payloads and unknown events retain the conservative replay barrier.
	require.True(t, openAIStreamDataStartsClientOutput(`{"delta":" "}`, "response.function_call_arguments.delta"))
	require.True(t, openAIStreamDataStartsClientOutput(`{"item":{"type":"function_call","arguments":" "}}`, "response.output_item.added"))
	require.True(t, openAIStreamDataStartsClientOutput(`{"type":"unknown_event"}`, "unknown_event"))
	require.True(t, openAIStreamDataStartsClientOutput(`invalid`, "response.output_text.delta"))
}

func TestOpenAIStreamLeadingWhitespaceReplaySafety(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, terminal := range []string{"overload", "eof", "success", "tool_then_overload"} {
			t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough]+"/"+terminal, func(t *testing.T) {
				stream := openAIWhitespaceOnlyPrelude
				switch terminal {
				case "overload":
					stream += openAIWhitespaceOverload
				case "success":
					stream += `data: {"type":"response.output_text.delta","delta":"hello"}` + "\n\n"
					stream += `data: {"type":"response.output_text.delta","delta":" "}` + "\n\n"
					stream += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_whitespace\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"
				case "tool_then_overload":
					stream += "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\"}\n\n" + openAIWhitespaceOverload
				}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				attempt := &fastestFailoverAttempt{idleTimeout: time.Hour}
				ctx := context.WithValue(context.Background(), fastestFailoverAttemptKey{}, attempt)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(stream))}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "model", "model")
				} else {
					_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "model", "model")
				}
				var failover *UpstreamFailoverError
				switch terminal {
				case "overload", "eof":
					require.ErrorAs(t, err, &failover)
					require.False(t, c.Writer.Written())
					require.Empty(t, rec.Body.String(), "discard the failed attempt's whitespace and lifecycle events")
					require.False(t, attempt.outputStarted, "whitespace must not disarm the first-output deadline")
				case "success":
					require.NoError(t, err)
					require.Contains(t, rec.Body.String(), openAIWhitespaceOnlyPrelude, "preserve all staged whitespace once real output arrives")
					require.Contains(t, rec.Body.String(), `"delta":"hello"`)
					require.True(t, attempt.outputStarted)
				case "tool_then_overload":
					require.Error(t, err)
					require.False(t, errors.As(err, &failover), "do not replay a tool call already delivered to the client")
					require.Contains(t, rec.Body.String(), "response.function_call_arguments.delta")
					require.True(t, attempt.outputStarted)
				}
			})
		}
	}
}

func TestOpenAIWSHTTPBridgeWhitespaceBeforeOverloadCanFailOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(openAIWhitespaceOnlyPrelude + openAIWhitespaceOverload)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"model","input":"hi"}`)
	var writes [][]byte
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c,
		&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1},
		"test-key", payload, len(payload), "model", "", "", "", "", 1,
		func(message []byte) error { writes = append(writes, message); return nil })
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, writes)
}
