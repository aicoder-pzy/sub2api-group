package repository

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
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

func largeGzipTestRequest(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	body := `{"model":"test-only","input":"` + strings.Repeat("same full context; ", (17<<20)/18) + `","stream":true}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	require.Greater(t, req.ContentLength, int64(upstreamGzipBufferedMaxBytes))
	return req
}

func gzipTestDigest(t *testing.T, r io.ReadCloser, encoded bool) []byte {
	t.Helper()
	defer func() { require.NoError(t, r.Close()) }()
	var source io.Reader = r
	if encoded {
		zr, err := gzip.NewReader(r)
		require.NoError(t, err)
		defer func() { require.NoError(t, zr.Close()) }()
		source = zr
	}
	h := sha256.New()
	_, err := io.Copy(h, source)
	require.NoError(t, err)
	return h.Sum(nil)
}

func TestUpstreamGzipStreamPreservesBodyReplayAndBounds(t *testing.T) {
	req := largeGzipTestRequest(t, "https://relay.example/v1/responses")
	require.True(t, upstreamGzipEligible(req))
	encoded := gzipUpstreamRequest(req)
	require.NotSame(t, req, encoded)
	require.EqualValues(t, -1, encoded.ContentLength)
	require.Equal(t, "gzip", encoded.Header.Get("Content-Encoding"))
	want := gzipTestDigest(t, req.Body, false)
	require.Equal(t, want, gzipTestDigest(t, encoded.Body, true))
	for i := 0; i < 2; i++ {
		r, err := encoded.GetBody()
		require.NoError(t, err)
		require.Equal(t, want, gzipTestDigest(t, r, true))
	}
	require.Eventually(t, func() bool { return len(upstreamGzipSlots) == 0 }, time.Second, time.Millisecond)
	// Saturation never adds a queue to the initial business request.
	for i := 0; i < cap(upstreamGzipSlots); i++ {
		upstreamGzipSlots <- struct{}{}
	}
	require.Same(t, req, gzipUpstreamRequest(req))
	for i := 0; i < cap(upstreamGzipSlots); i++ {
		<-upstreamGzipSlots
	}
	encoded = gzipUpstreamRequest(req)
	require.NotSame(t, req, encoded)
	require.NoError(t, encoded.Body.Close()) // No Read, no encoder goroutine.
	require.Zero(t, len(upstreamGzipSlots))
}

type gzipCloseCounter struct {
	io.Reader
	closed atomic.Int32
}

func (r *gzipCloseCounter) Close() error { r.closed.Add(1); return nil }

func TestUpstreamGzipStreamCancellationAndSourceErrors(t *testing.T) {
	for _, mode := range []string{"cancel", "early-close", "short", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			req := largeGzipTestRequest(t, "https://relay.example/v1/responses")
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			req = req.WithContext(ctx)
			var source *gzipCloseCounter
			req.GetBody = func() (io.ReadCloser, error) {
				source = &gzipCloseCounter{Reader: strings.NewReader("short")}
				if mode == "read-error" {
					source.Reader = &ioErrorReader{}
				}
				return source, nil
			}
			encoded := gzipUpstreamRequest(req)
			require.NotSame(t, req, encoded)
			switch mode {
			case "cancel", "early-close":
				_, err := encoded.Body.Read(make([]byte, 1)) // Encoder blocks on pipe output.
				require.NoError(t, err)
				if mode == "cancel" {
					cancel()
				} else {
					require.NoError(t, encoded.Body.Close())
				}
				_, err = io.Copy(io.Discard, encoded.Body)
				require.Error(t, err)
			default:
				_, err := io.Copy(io.Discard, encoded.Body)
				require.Error(t, err)
				if mode == "short" {
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				}
			}
			require.NoError(t, encoded.Body.Close())
			require.Eventually(t, func() bool { return len(upstreamGzipSlots) == 0 }, time.Second, time.Millisecond)
			require.EqualValues(t, 1, source.closed.Load())
		})
	}
}

func TestUpstreamGzipStreamReplayWaitIsCancellable(t *testing.T) {
	req := largeGzipTestRequest(t, "https://relay.example/v1/responses")
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	encoded := gzipUpstreamRequest(req.WithContext(ctx))
	defer func() { require.NoError(t, encoded.Body.Close()) }()
	second, err := encoded.GetBody()
	require.NoError(t, err)
	defer func() { require.NoError(t, second.Close()) }()
	done := make(chan error, 1)
	go func() { _, err := encoded.GetBody(); done <- err }()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("gzip replay ignored cancellation while encoders were occupied")
	}
	_ = encoded.Body.Close()
	_ = second.Close()
	require.Zero(t, len(upstreamGzipSlots))
}

func TestUpstreamGzipStreamReplaySourceFailureKeepsIdentity(t *testing.T) {
	req := largeGzipTestRequest(t, "https://relay.example/v1/responses")
	source := &gzipCloseCounter{Reader: strings.NewReader("unused")}
	req.GetBody = func() (io.ReadCloser, error) { return source, errors.New("cannot replay") }
	require.Same(t, req, gzipUpstreamRequest(req))
	require.EqualValues(t, 1, source.closed.Load())
	require.Zero(t, len(upstreamGzipSlots))
}

type ioErrorReader struct{}

func (*ioErrorReader) Read([]byte) (int, error) { return 0, errors.New("source read failed") }

func TestUpstreamGzipStreamOnly415ReplaysOriginal(t *testing.T) {
	req := largeGzipTestRequest(t, "https://relay.example/v1/responses")
	wantBody, err := req.GetBody()
	require.NoError(t, err)
	want := gzipTestDigest(t, wantBody, false)
	g := &upstreamRequestGzip{peers: make(map[string]upstreamGzipCapability)}
	key := upstreamGzipKey(req, "route")
	g.remember(key, true, time.Hour)
	s := &httpUpstreamService{requestGzip: g}
	calls := 0
	client := &http.Client{Transport: gzipTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		defer r.Body.Close()
		if r.Header.Get("Content-Encoding") == "gzip" {
			return gzipTestResponse(415, "unsupported encoding"), nil
		}
		require.Equal(t, want, gzipTestDigest(t, r.Body, false))
		return gzipTestResponse(200, "ok"), nil
	})}
	resp, err := s.doWithRequestGzip(client, req, 42, "route")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 2, calls)
	require.False(t, g.ready(client, req, key))
	require.Zero(t, len(upstreamGzipSlots))
}

func TestUpstreamGzipStreamHTTP1HTTP2IncrementalSSE(t *testing.T) {
	for _, withTLS := range []bool{false, true} {
		t.Run(fmt.Sprint(withTLS), func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			observed := make(chan []byte, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
				require.EqualValues(t, -1, r.ContentLength)
				if withTLS {
					require.Equal(t, 2, r.ProtoMajor)
				} else {
					require.Equal(t, []string{"chunked"}, r.TransferEncoding)
				}
				observed <- gzipTestDigest(t, r.Body, true)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: first\n\n")
				_ = http.NewResponseController(w).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			srv.EnableHTTP2 = withTLS
			if withTLS {
				srv.StartTLS()
			} else {
				srv.Start()
			}
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			req := largeGzipTestRequest(t, srv.URL+"/v1/responses").WithContext(ctx)
			r, err := req.GetBody()
			require.NoError(t, err)
			want := gzipTestDigest(t, r, false)
			encoded := gzipUpstreamRequest(req)
			resp, err := srv.Client().Do(encoded)
			require.NoError(t, err)
			defer func() { require.NoError(t, resp.Body.Close()) }()
			line, err := bufio.NewReader(resp.Body).ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "data: first\n", line, "SSE arrives before terminal response")
			require.Equal(t, want, <-observed)
		})
	}
}

func BenchmarkUpstreamGzipLargeRequest(b *testing.B) {
	for _, sizeMiB := range []int{10, 30, 60, 90} {
		b.Run(fmt.Sprint(sizeMiB), func(b *testing.B) {
			// Match the earlier transport probe: 74% base64-shaped high-entropy
			// input and 26% repetitive text, never customer content.
			body := make([]byte, sizeMiB<<20)
			const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
			var state uint32 = 1
			for i := range body {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				body[i] = alphabet[state&63]
			}
			for i := len(body) * 74 / 100; i < len(body); i++ {
				body[i] = "Synthetic context for transport measurement. "[i%44]
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req, _ := http.NewRequest(http.MethodPost, "https://relay.example/v1/responses", bytes.NewReader(body))
				encoded := gzipUpstreamRequest(req)
				if encoded == req {
					b.Fatal("gzip skipped")
				}
				written, err := io.Copy(io.Discard, encoded.Body)
				if err != nil {
					b.Fatal(err)
				}
				_ = encoded.Body.Close()
				b.ReportMetric(float64(written)/float64(len(body)), "wire/original")
			}
		})
	}
}
