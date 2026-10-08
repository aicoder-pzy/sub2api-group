package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const anthropicTimeoutPrelude = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_timeout\",\"model\":\"claude-sonnet-4-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":11}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}\n\n"

const anthropicTimeoutText = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
const anthropicTimeoutEnd = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

type anthropicSchedulingTimeoutUpstream struct {
	HTTPUpstream
	headers, output, success, eof bool
	bedrock                       bool
	stream                        *schedulingTimeoutBody
}

func (u *anthropicSchedulingTimeoutUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	attempt := fastestFailoverAttemptFromContext(req.Context())
	if u.headers {
		expireSchedulingAttempt(attempt)
		return nil, req.Context().Err()
	}
	payload := anthropicTimeoutPrelude
	if u.output {
		payload += anthropicTimeoutText
	}
	if u.success {
		payload += anthropicTimeoutEnd
	}
	if u.bedrock {
		payload = schedulingBedrockFrames(payload)
	}
	var body io.ReadCloser
	if u.success || u.eof {
		body = io.NopCloser(strings.NewReader(payload))
	} else {
		u.stream = &schedulingTimeoutBody{Reader: strings.NewReader(payload), attempt: attempt, waitForOutput: u.output}
		body = u.stream
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
}

func schedulingBedrockFrames(sse string) string {
	var frames strings.Builder
	for _, line := range strings.Split(sse, "\n") {
		data, ok := extractAnthropicSSEDataLine(line)
		if !ok {
			continue
		}
		payload := []byte(fmt.Sprintf(`{"bytes":%q}`, base64.StdEncoding.EncodeToString([]byte(data))))
		headers := []byte("\x0b:event-type\x07\x00\x05chunk")
		frame := make([]byte, 16+len(headers)+len(payload))
		binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)))
		binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
		binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
		copy(frame[12:], headers)
		copy(frame[12+len(headers):], payload)
		binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
		_, _ = frames.Write(frame)
	}
	return frames.String()
}

func TestFastestFailoverAnthropicTimeoutAcrossGroupsAndEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, groupID := range []int64{7, 42} {
		for _, endpoint := range []string{"messages", "passthrough", "bedrock", "chat", "responses", "chat_buffered", "responses_buffered"} {
			for _, scenario := range []string{"headers", "prelude", "output", "success", "eof"} {
				// Buffered responses stay under the first-output deadline throughout.
				if strings.HasSuffix(endpoint, "buffered") && (scenario == "output" || scenario == "eof") {
					continue
				}
				t.Run(fmt.Sprintf("group_%d/%s/%s", groupID, endpoint, scenario), func(t *testing.T) {
					group := &Group{ID: groupID, Name: "arbitrary-group", Platform: PlatformAnthropic, AccountSchedulingMode: AccountSchedulingModeFastestFailover}
					ctx := context.WithValue(context.Background(), ctxkey.Group, group)
					account := newAnthropicAPIKeyAccountForTest()
					account.Extra["anthropic_passthrough"] = endpoint == "passthrough"
					account.Extra["scheduling_preferred"] = true
					account.Credentials["model_mapping"] = map[string]any{"requested-model": "claude-sonnet-4-5", "other-model": "claude-haiku-4-5"}
					if endpoint == "bedrock" {
						account.Type = AccountTypeBedrock
						account.Credentials["auth_mode"] = "apikey"
						account.Credentials["aws_region"] = "us-east-1"
					}
					repo := &schedulingTimeoutRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}}
					upstream := &anthropicSchedulingTimeoutUpstream{headers: scenario == "headers", output: scenario == "output" || scenario == "success", success: scenario == "success", eof: scenario == "eof"}
					upstream.bedrock = endpoint == "bedrock"
					svc := newForwardPartialUsageServiceForTest(nil)
					svc.httpUpstream, svc.accountRepo = upstream, repo
					cache := &accountSchedulingCacheStub{bindings: make(map[string]int64)}
					svc.cache = cache
					rememberGroupModelSchedulingAccount(ctx, cache, &groupID, "requested-model", 999)
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
					stream := !strings.HasSuffix(endpoint, "buffered")
					body := []byte(fmt.Sprintf(`{"model":"requested-model","stream":%t,"max_tokens":32,"messages":[{"role":"user","content":"test"}],"input":"test"}`, stream))
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					forward := func() (*ForwardResult, error) {
						attemptParsed, err := parsed.CloneForBody(body)
						require.NoError(t, err)
						switch endpoint {
						case "chat", "chat_buffered":
							return svc.ForwardAsChatCompletions(ctx, c, account, body, attemptParsed)
						case "responses", "responses_buffered":
							return svc.ForwardAsResponses(ctx, c, account, body, attemptParsed)
						default:
							return svc.Forward(ctx, c, account, attemptParsed)
						}
					}
					result, err := forward()
					if scenario == "success" {
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, 11, result.Usage.InputTokens)
						require.Equal(t, 5, result.Usage.OutputTokens)
						require.Empty(t, repo.cooledModel)
						require.Contains(t, rec.Body.String(), "hello")
						require.EqualValues(t, account.ID, groupModelSchedulingActiveAccount(ctx, cache, &groupID, "requested-model"))
						return
					}
					var failover *UpstreamFailoverError
					require.EqualValues(t, 999, groupModelSchedulingActiveAccount(ctx, cache, &groupID, "requested-model"))
					require.ErrorAs(t, err, &failover)
					if scenario == "eof" {
						require.Equal(t, http.StatusBadGateway, failover.StatusCode)
						require.Empty(t, repo.cooledModel)
					} else {
						require.Equal(t, http.StatusGatewayTimeout, failover.StatusCode)
						require.False(t, failover.RetryableOnSameAccount)
						require.Equal(t, "claude-sonnet-4-5", repo.cooledModel)
						latest, err := repo.GetByID(ctx, account.ID)
						require.NoError(t, err)
						require.False(t, latest.IsSchedulableForModelWithContext(ctx, "requested-model"))
						require.True(t, latest.IsSchedulableForModelWithContext(ctx, "other-model"))
					}
					if scenario == "output" {
						require.NotNil(t, result, "do not lose observed usage after output")
						require.Equal(t, 11, result.Usage.InputTokens)
						require.Contains(t, rec.Body.String(), "hello")
						require.True(t, c.Writer.Written(), "handler must reject replay")
					} else {
						require.Nil(t, result, "pre-output retries bill only the serving attempt")
						require.Empty(t, rec.Body.String())
						require.Equal(t, -1, c.Writer.Size(), "preludes must not block handler failover")
						// The next account can reuse the downstream connection without
						// a second message_start or a synthetic completion from the failure.
						upstream.headers, upstream.success, upstream.output, upstream.eof = false, true, true, false
						account.ID++
						result, err = forward()
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Contains(t, rec.Body.String(), "hello")
						require.EqualValues(t, account.ID, groupModelSchedulingActiveAccount(ctx, cache, &groupID, "requested-model"))
						if endpoint == "messages" || endpoint == "passthrough" {
							require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_start"))
						}
					}
				})
			}
		}
	}
}

type schedulingTimeoutHTTPClient struct{ client *http.Client }

func (u schedulingTimeoutHTTPClient) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}
func (u schedulingTimeoutHTTPClient) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestFastestFailoverAnthropicCancelsDetachedHTTP(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint("headers_sent_", headers), func(t *testing.T) {
			canceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headers {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, anthropicTimeoutPrelude)
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("flush upstream prelude: %v", err)
					}
				}
				<-r.Context().Done()
				close(canceled)
			}))
			defer server.Close()
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{AccountSchedulingMode: AccountSchedulingModeFastestFailover})
			account := newAnthropicAPIKeyAccountForTest()
			ctx, attempt := beginFastestFailoverAttempt(ctx, account)
			attempt.firstTimeout = 100 * time.Millisecond
			defer func() { _ = finishFastestFailoverAttempt(ctx, nil, nil, account, nil, attempt, nil) }()
			detached, release := detachStreamUpstreamContext(ctx, true)
			defer release()
			req, err := http.NewRequestWithContext(detached, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			resp, err := doFastestFailoverUpstream(schedulingTimeoutHTTPClient{server.Client()}, req, "", account.ID, 1, nil)
			if headers {
				require.NoError(t, err)
				_, err = io.ReadAll(resp.Body)
				require.NoError(t, resp.Body.Close())
			}
			require.Error(t, err)
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("detached upstream request was not canceled")
			}
		})
	}
}

func TestFastestFailoverAnthropicPreludeAndTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, data := range []string{
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reason"}}`,
			`{"type":"content_block_start","content_block":{"type":"tool_use","id":"tool_1","name":"test","input":{}}}`,
			`{"type":"message_stop"}`,
		} {
			t.Run(fmt.Sprintf("enabled_%t/%s", enabled, data), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				resp := &http.Response{}
				if enabled {
					ctx := context.WithValue(context.Background(), fastestFailoverAttemptKey{}, &fastestFailoverAttempt{idleTimeout: time.Minute})
					resp.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
				}
				w := newFastestFailoverStreamWriter(c.Writer, resp)
				w.observeAnthropic(`{"type":"message_start","message":{"content":[]}}`)
				_, err := io.WriteString(w, anthropicTimeoutPrelude)
				require.NoError(t, err)
				w.Flush()
				require.Equal(t, !enabled, c.Writer.Written())
				w.observeAnthropic(data)
				_, err = fmt.Fprintf(w, "data: %s\n\n", data)
				require.NoError(t, err)
				w.Flush()
				require.True(t, c.Writer.Written())
			})
		}
	}
}
