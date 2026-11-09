package repository

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gzipTestTransport func(*http.Request) (*http.Response, error)

func (f gzipTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func gzipTestRequest(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	body := []byte(`{"model":"same-model","input":"` + strings.Repeat("same full context; ", 20000) + `","stream":true}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-only")
	return req
}

func gzipTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestUpstreamGzipPreservesBodyAndReplay(t *testing.T) {
	req := gzipTestRequest(t, "https://relay.example/v1/responses")
	original, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	req.Body, err = req.GetBody()
	require.NoError(t, err)
	req.Header["content-length"] = []string{fmt.Sprint(req.ContentLength)}
	require.True(t, upstreamGzipEligible(req))
	encoded := gzipUpstreamRequest(req)
	require.NotSame(t, req, encoded)
	require.Equal(t, "gzip", encoded.Header.Get("Content-Encoding"))
	require.Empty(t, encoded.Header["content-length"])
	require.Less(t, encoded.ContentLength, req.ContentLength/2)
	for i := 0; i < 2; i++ {
		reader, getErr := encoded.GetBody()
		require.NoError(t, getErr)
		zr, zipErr := gzip.NewReader(reader)
		require.NoError(t, zipErr)
		decoded, readErr := io.ReadAll(zr)
		require.NoError(t, readErr)
		require.NoError(t, zr.Close())
		require.NoError(t, reader.Close())
		require.Equal(t, original, decoded)
	}
	require.Empty(t, req.Header.Get("Content-Encoding"))
	remaining, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, original, remaining)
}

func TestUpstreamGzipEligibilityRespectsProtocolAndSignatures(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages", "/custom/messages"} {
		require.True(t, upstreamGzipEligible(gzipTestRequest(t, "https://relay.example"+endpoint)))
	}
	for _, header := range []string{"content-encoding", "x-amz-content-sha256", "signature-input", "dpop", "upgrade"} {
		req := gzipTestRequest(t, "https://relay.example/v1/responses")
		req.Header[header] = []string{"preserve"}
		require.False(t, upstreamGzipEligible(req), header)
	}
	for _, endpoint := range []string{"/v1/responses/compact", "/v1/uploads", "/oauth/token"} {
		require.False(t, upstreamGzipEligible(gzipTestRequest(t, "https://relay.example"+endpoint)))
	}
	for _, size := range []int64{0, upstreamGzipMinBytes - 1, upstreamGzipMaxBytes + 1} {
		req := gzipTestRequest(t, "https://relay.example/v1/responses")
		req.ContentLength = size
		require.False(t, upstreamGzipEligible(req))
	}
	req := gzipTestRequest(t, "https://relay.example/v1/responses")
	req.Header.Set("Content-Type", "multipart/form-data")
	require.False(t, upstreamGzipEligible(req))
}

func TestUpstreamGzipCompatibilityRequiresTwoDistinctValidationControls(t *testing.T) {
	for _, mode := range []string{"supported", "ignored-gzip", "generic-missing-model", "redirect", "auth-error"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			client := &http.Client{Transport: gzipTestTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				if mode == "redirect" {
					response := gzipTestResponse(307, "")
					response.Header.Set("Location", "https://different.example/v1/responses")
					return response, nil
				}
				if mode == "auth-error" {
					return gzipTestResponse(401, `{"error":"authentication required"}`), nil
				}
				if req.Header.Get("Content-Encoding") == "gzip" && mode != "ignored-gzip" {
					zr, zipErr := gzip.NewReader(bytes.NewReader(data))
					require.NoError(t, zipErr)
					data, err = io.ReadAll(zr)
					require.NoError(t, err)
					require.NoError(t, zr.Close())
				}
				// Both request variants have NO model identifier or user input.
				require.NotContains(t, string(data), `"model":`)
				if mode != "generic-missing-model" && !bytes.HasSuffix(data, []byte("}")) {
					return gzipTestResponse(400, `{"error":"Invalid JSON"}`), nil
				}
				return gzipTestResponse(400, `{"error":"model is required"}`), nil
			})}
			template := gzipTestRequest(t, "https://relay.example/v1/responses")
			require.Equal(t, mode == "supported", probeUpstreamGzip(client, template))
			if mode == "supported" {
				require.EqualValues(t, 4, calls.Load())
			}
			if mode == "redirect" {
				require.EqualValues(t, 1, calls.Load(), "probe must not follow a redirect")
			}
		})
	}
}

func TestUpstreamGzipUnknownCompatibilityNeverDelaysBusinessRequest(t *testing.T) {
	g := &upstreamRequestGzip{peers: make(map[string]upstreamGzipCapability), probe: make(chan struct{}, 1)}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := &http.Client{Transport: gzipTestTransport(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-req.Context().Done():
		}
		close(done)
		return gzipTestResponse(401, "no probing authorization"), nil
	})}
	req := gzipTestRequest(t, "https://relay.example/v1/messages")
	key := upstreamGzipKey(req, "direct")
	require.False(t, g.ready(client, req, key))
	<-started
	require.False(t, g.ready(client, req, key), "one pending probe per key")
	other := gzipTestRequest(t, "https://other.example/v1/messages")
	require.False(t, g.ready(client, other, upstreamGzipKey(other, "direct")), "probe concurrency bounded")
	close(release)
	<-done
	require.Eventually(t, func() bool { return len(g.probe) == 0 }, time.Second, time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	changed := req.Clone(req.Context())
	changed.Header.Set("Authorization", "Bearer rotated")
	require.NotEqual(t, key, upstreamGzipKey(changed, "direct"))
	require.NotEqual(t, key, upstreamGzipKey(req, "different-proxy"))
}

func TestUpstreamGzipDoesNotReplayAmbiguousErrors(t *testing.T) {
	for _, status := range []int{400, 403, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			req := gzipTestRequest(t, "https://relay.example/v1/responses")
			g := &upstreamRequestGzip{peers: make(map[string]upstreamGzipCapability)}
			g.remember(upstreamGzipKey(req, "route"), true, time.Hour)
			s := &httpUpstreamService{requestGzip: g}
			calls := 0
			client := &http.Client{Transport: gzipTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
				return gzipTestResponse(status, "upstream error"), nil
			})}
			resp, err := s.doWithRequestGzip(client, req, 42, "route")
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, 1, calls)
		})
	}
}

func TestUpstreamGzipOnly415FallsBackAndDisablesFutureCoding(t *testing.T) {
	req := gzipTestRequest(t, "https://relay.example/v1/responses")
	g := &upstreamRequestGzip{peers: make(map[string]upstreamGzipCapability)}
	key := upstreamGzipKey(req, "route")
	g.remember(key, true, time.Hour)
	s := &httpUpstreamService{requestGzip: g}
	calls := 0
	client := &http.Client{Transport: gzipTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Content-Encoding") != "" {
			return gzipTestResponse(415, "Unsupported content encoding"), nil
		}
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.EqualValues(t, req.ContentLength, len(data))
		return gzipTestResponse(200, "ok"), nil
	})}
	resp, err := s.doWithRequestGzip(client, req, 42, "route")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 2, calls)
	require.False(t, g.ready(client, req, key))
}

func TestHTTPUpstreamGzipPreservesIncrementalSSEForNormalAndTLS(t *testing.T) {
	for _, withTLS := range []bool{false, true} {
		t.Run(fmt.Sprint(withTLS), func(t *testing.T) {
			release := make(chan struct{})
			observed := make(chan []byte, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Encoding") != "gzip" {
					http.Error(w, "expected gzip", 400)
					return
				}
				zr, err := gzip.NewReader(r.Body)
				if err != nil {
					http.Error(w, "invalid gzip", 400)
					return
				}
				body, err := io.ReadAll(zr)
				_ = zr.Close()
				if err != nil {
					http.Error(w, "invalid body", 400)
					return
				}
				observed <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: first\n\n")
				_ = http.NewResponseController(w).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "data: terminal\n\n")
			}))
			if withTLS {
				srv.StartTLS()
			} else {
				srv.Start()
			}
			t.Cleanup(srv.Close)
			s, send := lifecycleClient(t, srv, withTLS)
			req := gzipTestRequest(t, srv.URL+"/v1/responses")
			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			defer cancel()
			req = req.WithContext(ctx)
			s.mu.RLock()
			for _, entry := range s.clients {
				s.requestGzip.remember(upstreamGzipKey(req, entry.proxyKey+"|"+entry.protocolMode), true, time.Hour)
			}
			s.mu.RUnlock()
			resp, err := send(req)
			require.NoError(t, err)
			reader := bufio.NewReader(resp.Body)
			first, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "data: first\n", first, "first chunk before terminal is permitted")
			original, err := req.GetBody()
			require.NoError(t, err)
			want, err := io.ReadAll(original)
			require.NoError(t, err)
			require.NoError(t, original.Close())
			require.Equal(t, want, <-observed)
			close(release)
			rest, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Contains(t, string(rest), "data: terminal")
			require.NoError(t, resp.Body.Close())
			requireNoUpstreamInFlight(t, s)
		})
	}
}
