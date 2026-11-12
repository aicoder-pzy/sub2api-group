package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	upstreamGzipMinBytes = 256 << 10
	// Larger coded bodies hit ingress limits on otherwise gzip-capable relays.
	upstreamGzipMaxBytes         = 60 << 20
	upstreamGzipBufferedMaxBytes = 16 << 20
	upstreamGzipMaxPeers         = 512
	upstreamGzipProbeBody        = `{"input":[],"messages":[],"stream":true}`
)

type upstreamGzipCapability struct {
	supported bool
	expires   time.Time
}

// Compatibility is learned per endpoint, credential, and transport route. It is
// a transport property, with no account/group opt-in or database migration.
type upstreamRequestGzip struct {
	mu    sync.Mutex
	peers map[string]upstreamGzipCapability
	probe chan struct{}
}

func newUpstreamRequestGzip() *upstreamRequestGzip {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SUB2API_UPSTREAM_GZIP")), "off") {
		return nil
	}
	return &upstreamRequestGzip{peers: make(map[string]upstreamGzipCapability), probe: make(chan struct{}, 2)}
}

func (s *httpUpstreamService) doWithRequestGzip(client *http.Client, req *http.Request, accountID int64, route string) (*http.Response, error) {
	forwarded := req
	key := ""
	closeOriginal := false
	defer func() {
		if closeOriginal {
			_ = req.Body.Close()
		}
	}()
	if s.requestGzip != nil && upstreamGzipEligible(req) {
		key = upstreamGzipKey(req, route)
		if s.requestGzip.ready(client, req, key) {
			started := time.Now()
			forwarded = gzipUpstreamRequest(req)
			if forwarded != req {
				// The transport owns forwarded.Body. Close the original as Client.Do
				// would, including on cancellation, without consuming it for a probe.
				closeOriginal = true
				if forwarded.ContentLength < 0 {
					slog.InfoContext(req.Context(), "upstream_request_gzip", "account_id", accountID,
						"original_bytes", req.ContentLength, "encoding_mode", "streaming")
				} else {
					slog.InfoContext(req.Context(), "upstream_request_gzip", "account_id", accountID,
						"original_bytes", req.ContentLength, "encoded_bytes", forwarded.ContentLength,
						"compression_us", time.Since(started).Microseconds())
				}
			}
		}
	}
	resp, err := doObservedUpstreamRequest(client, forwarded, accountID, s.upstreamDrainTimeout())
	if err == nil && resp.StatusCode == http.StatusUnsupportedMediaType && forwarded != req {
		// 415 rejects the HTTP representation before model execution. Do not
		// extend this to 400/403/5xx, EOF, or other ambiguous failures.
		s.requestGzip.remember(key, false, 10*time.Minute)
		_ = resp.Body.Close()
		slog.WarnContext(req.Context(), "upstream_request_gzip_rejected", "account_id", accountID)
		closeOriginal = false
		return doObservedUpstreamRequest(client, req, accountID, s.upstreamDrainTimeout())
	}
	return resp, err
}

func upstreamGzipEligible(req *http.Request) bool {
	if req == nil || req.URL == nil || req.URL.User != nil || req.Body == nil || req.GetBody == nil || req.Method != http.MethodPost {
		return false
	}
	if req.ContentLength < upstreamGzipMinBytes || req.ContentLength > upstreamGzipMaxBytes {
		return false
	}
	if len(req.Trailer) > 0 {
		return false
	}
	path := req.URL.Path
	if !strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/chat/completions") && !strings.HasSuffix(path, "/messages") {
		return false
	}
	mediaType, _, _ := strings.Cut(upstreamGzipHeader(req.Header, "Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mediaType), "application/json") {
		return false
	}
	for _, header := range []string{"Content-Encoding", "Content-MD5", "Digest", "Signature", "Signature-Input", "DPoP", "X-Amz-Content-Sha256", "X-Goog-Content-Sha256", "X-Signature", "X-Api-Signature", "Upgrade", "Range"} {
		if upstreamGzipHeader(req.Header, header) != "" {
			return false
		}
	}
	return !strings.Contains(strings.ToLower(upstreamGzipHeader(req.Header, "Connection")), "upgrade") &&
		!strings.HasPrefix(strings.ToUpper(strings.TrimSpace(upstreamGzipHeader(req.Header, "Authorization"))), "AWS4-")
}

func upstreamGzipKey(req *http.Request, route string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, route+"\x00"+req.URL.String()+"\x00"+req.Host)
	for _, header := range upstreamGzipProbeHeaders {
		_, _ = io.WriteString(h, "\x00"+upstreamGzipHeader(req.Header, header))
	}
	return hex.EncodeToString(h.Sum(nil))
}

var upstreamGzipProbeHeaders = []string{
	"Authorization", "X-Api-Key", "Anthropic-Version", "Anthropic-Beta", "User-Agent",
	"OpenAI-Organization", "OpenAI-Project", "OpenAI-Beta", "ChatGPT-Account-Id",
}

func (g *upstreamRequestGzip) ready(client *http.Client, req *http.Request, key string) bool {
	now := time.Now()
	g.mu.Lock()
	if peer, ok := g.peers[key]; ok && now.Before(peer.expires) {
		g.mu.Unlock()
		return peer.supported
	}
	if len(g.peers) >= upstreamGzipMaxPeers {
		for k, peer := range g.peers {
			if !now.Before(peer.expires) {
				delete(g.peers, k)
			}
		}
	}
	if len(g.peers) >= upstreamGzipMaxPeers || client == nil {
		g.mu.Unlock()
		return false
	}
	select {
	case g.probe <- struct{}{}:
		// Reserve this key before starting work. Business requests keep using
		// identity while an eight-second, concurrency-bounded probe runs.
		g.peers[key] = upstreamGzipCapability{expires: now.Add(time.Minute)}
		g.mu.Unlock()
	default:
		g.mu.Unlock()
		return false
	}
	template := req.Clone(context.Background())
	template.Body, template.GetBody = nil, nil
	template.Header = make(http.Header)
	for _, header := range upstreamGzipProbeHeaders {
		if value := upstreamGzipHeader(req.Header, header); value != "" {
			template.Header.Set(header, value)
		}
	}
	go func() {
		defer func() { <-g.probe }()
		supported := probeUpstreamGzip(client, template)
		ttl := 10 * time.Minute
		if supported {
			ttl = 24 * time.Hour
		}
		g.remember(key, supported, ttl)
		slog.Info("upstream_request_gzip_capability", "host", template.URL.Hostname(),
			"supported", supported, "validation", "valid_and_truncated_json_controls")
	}()
	return false
}

func (g *upstreamRequestGzip) remember(key string, supported bool, ttl time.Duration) {
	g.mu.Lock()
	g.peers[key] = upstreamGzipCapability{supported: supported, expires: time.Now().Add(ttl)}
	g.mu.Unlock()
}

// Neither probe contains a model ID or user input. Missing-model alone is not
// proof: some relays report it for unread gzip too. Require a distinct parse
// error for truncated JSON in BOTH representations before sending business gzip.
func probeUpstreamGzip(client *http.Client, template *http.Request) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	probeClient := *client
	probeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, encoded := range []bool{false, true} {
		for _, valid := range []bool{true, false} {
			body := []byte(upstreamGzipProbeBody)
			if !valid {
				body = body[:len(body)-1]
			}
			if encoded {
				body = gzipUpstreamBytes(body)
			}
			req := template.Clone(ctx)
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			req.GetBody = nil
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Encoding", "identity")
			if encoded {
				req.Header.Set("Content-Encoding", "gzip")
				// Large requests stream with unknown wire length. Learn support
				// using that representation too, including HTTP/1.1 chunking.
				req.ContentLength = -1
			}
			resp, err := probeClient.Do(req) //nolint:gosec // G704: validated upstream URL, redirects disabled; no user body.
			if err != nil {
				return false
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, (8<<10)+1))
			_ = resp.Body.Close()
			if readErr != nil || len(data) > 8<<10 || !upstreamGzipValidation(resp.StatusCode, data, valid) {
				return false
			}
		}
	}
	return true
}

func upstreamGzipValidation(status int, data []byte, valid bool) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	text := strings.ToLower(string(data))
	parseError := false
	for _, phrase := range []string{"invalid json", "invalid_json", "parse request body", "unexpected end", "malformed json", "invalid character"} {
		parseError = parseError || strings.Contains(text, phrase)
	}
	if !valid {
		// A streaming-only relay may reject truncated JSON at its stream gate
		// before reporting the parse failure. It must still differ from the
		// missing-model result for the complete, stream=true control.
		return parseError || strings.Contains(text, "stream_required")
	}
	return !parseError && strings.Contains(text, "model") &&
		(strings.Contains(text, "required") || strings.Contains(text, "missing") || strings.Contains(text, "empty"))
}

var upstreamGzipWriters = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return writer
}}

func gzipUpstreamBytes(body []byte) []byte {
	var out bytes.Buffer
	writer, _ := upstreamGzipWriters.Get().(*gzip.Writer)
	writer.Reset(&out)
	_, _ = writer.Write(body)
	_ = writer.Close()
	writer.Reset(io.Discard)
	upstreamGzipWriters.Put(writer)
	return out.Bytes()
}

var upstreamGzipSlots = make(chan struct{}, 2)

func gzipUpstreamRequest(req *http.Request) *http.Request {
	if req.ContentLength > upstreamGzipBufferedMaxBytes {
		return gzipStreamingUpstreamRequest(req)
	}
	// Bound transient replay/encoding memory independently of request volume.
	// Saturation keeps the original request rather than queueing model calls.
	select {
	case upstreamGzipSlots <- struct{}{}:
		defer func() { <-upstreamGzipSlots }()
	default:
		return req
	}
	body, err := req.GetBody()
	if err != nil || body == nil {
		return req
	}
	defer func() { _ = body.Close() }()
	original, err := io.ReadAll(io.LimitReader(body, upstreamGzipMaxBytes+1))
	if err != nil || int64(len(original)) != req.ContentLength {
		return req
	}
	encoded := gzipUpstreamBytes(original)
	if len(encoded)*10 > len(original)*9 {
		return req
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(encoded))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	clone.ContentLength = int64(len(encoded))
	clone.TransferEncoding = nil
	for header := range clone.Header {
		if strings.EqualFold(header, "Content-Length") {
			delete(clone.Header, header)
		}
	}
	clone.Header.Set("Content-Encoding", "gzip")
	return clone
}

func upstreamGzipHeader(header http.Header, name string) string {
	var all []string
	for key, values := range header {
		if strings.EqualFold(key, name) {
			all = append(all, values...)
		}
	}
	return strings.Join(all, ",")
}
