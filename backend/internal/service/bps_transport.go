package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func jsonString(value string) string                      { raw, _ := json.Marshal(value); return string(raw) }
func normalizeOpenAICodexTicketModel(model string) string { return strings.TrimSpace(model) }
func openAIAccountProxyURL(a *Account) string {
	if a != nil && a.ProxyID != nil && a.Proxy != nil {
		return a.Proxy.URL()
	}
	return ""
}

func applyOpenAICodexTicketHarvestIdentity(h http.Header, model string) {
	ensureCodexIdentityHeaders(h)
	enforceCodexIdentityHeaders(h)
	model = strings.ToLower(model)
	if (strings.Contains(model, "gpt-6") || strings.Contains(model, "astra")) && CompareVersions(h.Get("version"), "0.153.4") < 0 {
		h.Set("version", "0.153.4")
		h.Set("user-agent", buildCodexCLIUserAgent("0.153.4"))
	}
}

// releaseBody keeps a leased proxy pinned until the HTTP response is closed.
type bpsTicketReleaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *bpsTicketReleaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func bpsLocalResponse(req *http.Request, status int, code, message string) *http.Response {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"type": "invalid_request_error", "code": code, "message": message}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), Request: req}
}

func readBPSTicketRequest(req *http.Request) ([]byte, error) {
	if req.GetBody == nil {
		return nil, errors.New("request body is not replayable")
	}
	r, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(io.LimitReader(r, 64<<20+1))
	if len(raw) > 64<<20 {
		return nil, errors.New("request body exceeds 64 MiB")
	}
	return raw, err
}

func (s *OpenAIGatewayService) acquireBPSProxy(ctx context.Context, a *Account) (string, func(), func(), error) {
	noop := func() {}
	switch a.BPSTicketConfig().ProxySource {
	case "mihomo", "static":
		var lease *mihomo.BPSLease
		var err error
		scope := fmt.Sprintf("%d:%s", a.ID, bpsTicketAccountRevision(a))
		if a.BPSTicketConfig().ProxySource == "mihomo" {
			lease, err = mihomo.AcquireBPSLease(ctx, scope)
		} else {
			lease, err = mihomo.AcquireBPSStaticLease(ctx, scope)
		}
		if err != nil {
			return "", noop, noop, errors.New("BPS proxy pool has no ready exit")
		}
		return lease.ProxyURL, lease.Release, lease.ReportFailure, nil
	default:
		return openAIAccountProxyURL(a), noop, noop, nil
	}
}

// BPS has its own credentials/headers. Native Codex tickets, cookies and plugins
// must never be sent to the BPS host. All downstream parsing stays in the gateway.
func (s *OpenAIGatewayService) doBPSUpstream(req *http.Request, a *Account, raw []byte) (*http.Response, error) {
	key := codexTicketKey(a, gjson.GetBytes(raw, "model").String())
	s.bpsTickets.mu.Lock()
	until := s.bpsTickets.bpsCooldown[key]
	s.bpsTickets.mu.Unlock()
	if time.Now().Before(until) {
		return bpsLocalResponse(req, 503, "bps_rate_limited", "BPS is temporarily rate limited; native account quota is unchanged"), nil
	}
	var replay *basispoints.ReplayCache
	var catalog *basispoints.CatalogCache
	scope, _ := req.Context().Value(bpsRequestScopeKey{}).(string)
	if scope != "" {
		scope = fmt.Sprintf("%d:%s:%s", a.ID, bpsTicketAccountRevision(a), scope)
		replay = &s.bpsTickets.replay
		catalog = &s.bpsTickets.catalog
	}
	images, err := basispoints.PrepareNativeImagesWithLimit(raw, 12)
	if err != nil {
		return bpsLocalResponse(req, 400, "bps_invalid_image", err.Error()), nil
	}
	body, bridge, err := images.PrepareWithCatalog(scope, replay, catalog)
	if err != nil {
		return bpsLocalResponse(req, 400, "bps_invalid_request", err.Error()), nil
	}
	if len(bridge.Warnings) > 0 {
		return bpsLocalResponse(req, 400, "bps_unsupported_tool", strings.Join(bridge.Warnings, "; ")), nil
	}
	ctx := WithHTTPUpstreamRedirectsDisabled(req.Context())
	bpsReq, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	bpsReq.Header = http.Header{
		"Authorization":           {req.Header.Get("Authorization")},
		"Chatgpt-Account-Id":      {req.Header.Get("Chatgpt-Account-Id")},
		"X-Openai-Account-Id":     {req.Header.Get("Chatgpt-Account-Id")},
		"X-Basispoints-Auth-Mode": {"chatgpt"}, "Content-Type": {"application/json"}, "Accept": {"text/event-stream"},
		"Origin": {"https://bps.openai.com"}, "User-Agent": {"Mozilla/5.0"},
		"X-Openai-Internal-Basispoints-Client-Product":       {"basispoints-excel-plugin"},
		"X-Openai-Internal-Basispoints-Client-Agent-Profile": {"excel"},
	}
	if bpsReq.Header.Get("Chatgpt-Account-Id") == "" {
		return bpsLocalResponse(req, 400, "bps_account_id_missing", "BPS requires a ChatGPT account ID"), nil
	}
	proxy, release, failed, err := s.acquireBPSProxy(ctx, a)
	if err != nil {
		return bpsLocalResponse(req, 503, "bps_proxy_unavailable", err.Error()), nil
	}
	if images.HasImages() {
		// Upload and generation share one leased exit. The attachment cache is
		// request-local, so no file references cross account/client boundaries.
		raw, err = images.Upload(ctx, &basispoints.AttachmentCache{}, scope, func(ctx context.Context, img basispoints.InlineAttachment) (string, error) {
			return s.uploadBPSImage(ctx, a, bpsReq.Header, proxy, img)
		})
		if err != nil {
			release()
			return bpsLocalResponse(req, 502, "bps_attachment_failed", err.Error()), nil
		}
		body, bridge, err = bridge.Reprepare(raw)
		if err != nil {
			release()
			return bpsLocalResponse(req, 400, "bps_invalid_request", err.Error()), nil
		}
		bpsReq.Body = io.NopCloser(bytes.NewReader(body))
		bpsReq.ContentLength = int64(len(body))
	}
	// A fresh CONNECT prevents a reallocated local listener from retaining an old exit.
	bpsReq.Close = true
	resp, err := s.httpUpstream.Do(bpsReq, proxy, a.ID, a.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		release()
		failed()
		return nil, bpsSafeTransportError(ctx, err, "BPS upstream transport failed")
	}
	if resp == nil || resp.Body == nil {
		release()
		return nil, errors.New("BPS upstream response missing")
	}
	resp.Body = &bpsTicketReleaseBody{ReadCloser: resp.Body, release: release}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		status := resp.StatusCode
		// BPS entitlement failures are not native Codex bans. Never persist an
		// arbitrary upstream body (it can echo prompts or credentials).
		if status == 429 {
			delay := 120 * time.Second
			if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 && seconds <= 3600 {
				delay = time.Duration(seconds) * time.Second
			}
			s.bpsTickets.mu.Lock()
			if s.bpsTickets.bpsCooldown == nil {
				s.bpsTickets.bpsCooldown = map[string]time.Time{}
			}
			s.bpsTickets.bpsCooldown[key] = time.Now().Add(delay)
			s.bpsTickets.mu.Unlock()
			status = 503
		}
		if status == 403 {
			status = 503
		}
		if resp.StatusCode >= 500 {
			failed()
		}
		return bpsLocalResponse(req, status, "bps_upstream_rejected", fmt.Sprintf("BPS upstream returned HTTP %d", resp.StatusCode)), nil
	}
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Body = bridge.StreamWithToolRepair(ctx, resp.Body, nil)
	return resp, nil
}

func bpsSafeTransportError(ctx context.Context, err error, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New(message)
}

type bpsRequestScopeKey struct{}

func stampBPSForwardResult(c *gin.Context, a *Account, raw []byte, result *OpenAIForwardResult) {
	if !a.IsExcelBPSEnabledForModel(gjson.GetBytes(raw, "model").String()) {
		return
	}
	SetActualOpenAIUpstreamEndpoint(c, "/basispoints/api/responses")
	if result != nil {
		result.UpstreamEndpoint = "/basispoints/api/responses"
		if result.ReasoningEffort != nil {
			if effort, err := basispoints.NormalizeEffort(*result.ReasoningEffort); err == nil {
				result.ReasoningEffort = &effort
			}
		}
	}
}

// Only an authenticated client and an explicit session can inherit a catalog.
// Requests without that identity use complete, request-local declarations.
func withBPSRequestScope(ctx context.Context, c *gin.Context, body []byte) context.Context {
	if c == nil || c.Request == nil {
		return ctx
	}
	keyID := getAPIKeyIDFromContext(c)
	session := extractClientSessionID(c.Request.Header)
	if session == "" {
		session = gjson.GetBytes(body, "prompt_cache_key").String()
	}
	if keyID <= 0 || session == "" {
		return ctx
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", keyID, session)))
	return context.WithValue(ctx, bpsRequestScopeKey{}, hex.EncodeToString(digest[:]))
}

func (s *OpenAIGatewayService) uploadBPSImage(ctx context.Context, a *Account, headers http.Header, proxy string, img basispoints.InlineAttachment) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body, contentType, length, err := img.Multipart()
	if err != nil {
		return "", errors.New("BPS image multipart validation failed")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", basispoints.AttachmentsURL, body)
	if err != nil {
		return "", err
	}
	req.Header = headers.Clone()
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.ContentLength = length
	req.Close = true
	resp, err := s.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return "", errors.New("BPS image upload transport failed")
	}
	if resp == nil || resp.Body == nil {
		return "", errors.New("BPS image upload response missing")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("BPS image upload returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return "", errors.New("BPS image upload response incomplete")
	}
	var result struct {
		FileID string `json:"openai_file_id"`
	}
	if json.Unmarshal(raw, &result) != nil || !basispoints.ValidAttachmentID(result.FileID) {
		return "", errors.New("BPS image upload returned an invalid file reference")
	}
	return result.FileID, nil
}

func (s *OpenAIGatewayService) routeBPSTicketHTTP(req *http.Request, a *Account) (*http.Response, bool, error) {
	if !bpsTicketEligible(a) || !a.bpsTicketNeedsHTTP() || req.URL == nil || !strings.EqualFold(req.URL.Hostname(), "chatgpt.com") || !strings.HasPrefix(req.URL.Path, "/backend-api/codex/responses") {
		return nil, false, nil
	}
	if IsOpenAICodexStateProbeRequest(req.Context()) {
		return nil, false, nil
	}
	raw, err := readBPSTicketRequest(req)
	if err != nil {
		return nil, true, err
	}
	model := gjson.GetBytes(raw, "model").String()
	if a.IsExcelBPSEnabledForModel(model) {
		if req.URL.Path != "/backend-api/codex/responses" {
			return bpsLocalResponse(req, 400, "bps_endpoint_unsupported", "This endpoint is unavailable on the selected BPS route"), true, nil
		}
		resp, err := s.doBPSUpstream(req, a, raw)
		return resp, true, err
	}
	if req.URL.Path != "/backend-api/codex/responses" {
		return nil, false, nil
	}
	if !a.BPSTicketConfig().Tickets || !a.bpsTicketModelSelected(model) {
		return nil, false, nil
	}
	cfg, err := s.bpsTicketRuntimeSettings(req.Context())
	if err != nil {
		return nil, true, err
	}
	if !cfg.HarvestEnabled {
		return nil, false, nil
	}
	return s.doTicketUpstream(req, a, model, cfg)
}

func (s *OpenAIGatewayService) bpsTicketRuntimeSettings(ctx context.Context) (BPSTicketSettings, error) {
	s.bpsTickets.mu.Lock()
	v, at := s.bpsTickets.settings, s.bpsTickets.settingsAt
	s.bpsTickets.mu.Unlock()
	if time.Since(at) < 10*time.Second {
		return v, nil
	}
	v, err := s.GetBPSTicketSettings(ctx)
	if err == nil {
		s.bpsTickets.mu.Lock()
		s.bpsTickets.settings = v
		s.bpsTickets.settingsAt = time.Now()
		s.bpsTickets.mu.Unlock()
	}
	return v, err
}
