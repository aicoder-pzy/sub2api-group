package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/google/uuid"
)

type bpsTicketRuntime struct {
	replay        basispoints.ReplayCache
	catalog       basispoints.CatalogCache
	mu            sync.Mutex
	settings      BPSTicketSettings
	settingsAt    time.Time
	tickets       map[string]*codexTicketCredential
	bpsCooldown   map[string]time.Time
	busy          map[int64]bool
	lastHarvest   map[string]time.Time
	harvestStatus map[string]CodexTicketStatus
	proxyCursor   uint64
	cancel        context.CancelFunc
	done          chan struct{}
}

type codexTicketCredential struct {
	state       string
	cookies     []string
	expires     time.Time
	proxyID     int64
	proxyDigest [32]byte
	node        string
	revision    string
	model       string
}

type CodexTicketStatus struct {
	AccountID int64     `json:"account_id"`
	Model     string    `json:"model"`
	Length    int       `json:"length"`
	Ready     bool      `json:"ready"`
	ExpiresAt time.Time `json:"expires_at"`
	CheckedAt time.Time `json:"checked_at"`
	Attempts  int       `json:"attempts"`
	Reason    string    `json:"reason"`
	ProxyID   int64     `json:"proxy_id,omitempty"`
	Node      string    `json:"node,omitempty"`
}

func codexTicketKey(a *Account, model string) string {
	return fmt.Sprintf("%d:%s:%s", a.ID, bpsTicketAccountRevision(a), model)
}

func parseCodexTicketIssuedAt(value string, expected int, now time.Time) (time.Time, error) {
	if len(value) != expected || strings.ContainsAny(value, "\r\n\t ") {
		return time.Time{}, errors.New("ticket_length_mismatch")
	}
	core := strings.TrimRight(value, "=")
	raw, err := base64.RawURLEncoding.Strict().DecodeString(core)
	if len(value)-len(core) > 2 || err != nil || len(raw) < 73 || raw[0] != 0x80 || (len(raw)-57)%16 != 0 {
		return time.Time{}, errors.New("ticket_envelope_invalid")
	}
	issued := binary.BigEndian.Uint64(raw[1:9])
	if issued < 1577836800 || issued >= 4102444800 {
		return time.Time{}, errors.New("ticket_timestamp_invalid")
	}
	at := time.Unix(int64(issued), 0)
	if at.After(now.Add(30*time.Second)) || !now.Before(at.Add(240*time.Second)) {
		return time.Time{}, errors.New("ticket_expired")
	}
	return at, nil
}

func codexTicketExpectedLength(a *Account, cfg BPSTicketSettings) int {
	if cfg.TargetLength == 780 {
		return 780
	}
	plan := strings.ToLower(a.GetCredential("plan_type"))
	for _, m := range []string{"team", "business", "enterprise"} {
		if strings.Contains(plan, m) {
			return 332
		}
	}
	return 292
}

func (s *OpenAIGatewayService) beginBPSTicketJob(id int64) (func(), bool) {
	r := &s.bpsTickets
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy == nil {
		r.busy = map[int64]bool{}
	}
	if r.busy[id] {
		return func() {}, false
	}
	r.busy[id] = true
	return func() { r.mu.Lock(); delete(r.busy, id); r.mu.Unlock() }, true
}

func (s *OpenAIGatewayService) ProbeBPSTicketAccount(ctx context.Context, id int64, model string) (*OpenAICodexStateProbeResult, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("model is required")
	}
	release, ok := s.beginBPSTicketJob(id)
	if !ok {
		return nil, ErrOpenAICodexStateProbeBusy
	}
	defer release()
	return s.probeOpenAICodexState(ctx, a, model, true), nil
}

func (s *OpenAIGatewayService) HarvestBPSTicketAccount(ctx context.Context, id int64, model string) (*CodexTicketStatus, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !bpsTicketEligible(a) || strings.TrimSpace(model) == "" || !a.bpsTicketModelSelected(model) {
		return nil, errors.New("select a configured model on an eligible OAuth account")
	}
	cfg, err := s.GetBPSTicketSettings(ctx)
	if err != nil {
		return nil, err
	}
	release, ok := s.beginBPSTicketJob(id)
	if !ok {
		return nil, ErrOpenAICodexStateProbeBusy
	}
	defer release()
	mapped, _ := a.selectedBPSTicketModel(model)
	status := s.harvestBPSTicket(ctx, a, mapped, cfg)
	return &status, nil
}

func (s *OpenAIGatewayService) BPSTicketStatuses(a *Account) []CodexTicketStatus {
	out := []CodexTicketStatus{}
	s.bpsTickets.mu.Lock()
	defer s.bpsTickets.mu.Unlock()
	for _, model := range a.BPSTicketConfig().Models {
		model = normalizeOpenAIModelForUpstream(a, a.GetMappedModel(model))
		key := codexTicketKey(a, model)
		status := s.bpsTickets.harvestStatus[key]
		status.AccountID = a.ID
		status.Model = model
		status.Ready = false
		if ticket := s.bpsTickets.tickets[key]; ticket != nil {
			status.Ready = time.Now().Before(ticket.expires)
			status.ExpiresAt = ticket.expires
			status.Length = len(ticket.state)
		}
		out = append(out, status)
	}
	return out
}

func (s *OpenAIGatewayService) harvestBPSTicket(ctx context.Context, a *Account, model string, cfg BPSTicketSettings) (status CodexTicketStatus) {
	key := codexTicketKey(a, model)
	status = CodexTicketStatus{AccountID: a.ID, Model: model, CheckedAt: time.Now()}
	defer func() {
		s.bpsTickets.mu.Lock()
		defer s.bpsTickets.mu.Unlock()
		if s.bpsTickets.lastHarvest == nil {
			s.bpsTickets.lastHarvest = map[string]time.Time{}
			s.bpsTickets.harvestStatus = map[string]CodexTicketStatus{}
		}
		s.bpsTickets.lastHarvest[key] = time.Now()
		s.bpsTickets.harvestStatus[key] = status
	}()
	token, _, err := s.GetAccessToken(ctx, a)
	if err != nil || token == "" {
		status.Reason = "account_authentication_failed"
		return
	}
	var collection *mihomo.Collection
	if cfg.HarvestProxySource == "mihomo" {
		collection, err = mihomo.BeginCollection(ctx, mihomo.Endpoint)
		if err != nil {
			status.Reason = "proxy_pool_unavailable"
			return
		}
		defer func() { _ = collection.Close() }()
	}
	proxies := []Proxy{}
	if cfg.HarvestProxySource == "static" {
		if s.settingService == nil || s.settingService.proxyRepo == nil {
			status.Reason = "proxy_pool_unavailable"
			return
		}
		proxies, err = s.settingService.proxyRepo.ListByIDs(ctx, cfg.ProxyIDs)
		if err != nil {
			status.Reason = "proxy_pool_unavailable"
			return
		}
		proxies = slices.DeleteFunc(proxies, func(p Proxy) bool { return !p.IsActive() || p.IsExpired(time.Now()) })
		if len(proxies) == 0 {
			status.Reason = "proxy_pool_empty"
			return
		}
	}
	for n := 0; n < cfg.MaxAttempts; n++ {
		if ctx.Err() != nil {
			status.Reason = "cancelled"
			return
		}
		proxy := openAIAccountProxyURL(a)
		node := ""
		proxyID := int64(0)
		if a.ProxyID != nil {
			proxyID = *a.ProxyID
		}
		if collection != nil {
			node, proxy, err = collection.Next(ctx, 0)
			if err != nil || node == "" {
				status.Reason = "proxy_pool_exhausted"
				return
			}
			proxyID = 0
		} else if len(proxies) > 0 {
			s.bpsTickets.mu.Lock()
			cursor := s.bpsTickets.proxyCursor
			s.bpsTickets.proxyCursor++
			s.bpsTickets.mu.Unlock()
			p := proxies[cursor%uint64(len(proxies))]
			proxy = p.URL()
			proxyID = p.ID
		}
		status.Attempts++
		status.ProxyID = proxyID
		status.Node = mihomo.NodeDisplayName(node)
		ticket, code, err := s.mintBPSTicket(ctx, a, token, model, proxy, cfg)
		if err != nil {
			status.Reason = err.Error()
			if code == 401 || code == 403 || code == 429 || code == 400 {
				return
			}
			continue
		}
		ticket.proxyID = proxyID
		ticket.proxyDigest = sha256.Sum256([]byte(proxy))
		ticket.node = node
		ticket.revision = bpsTicketAccountRevision(a)
		ticket.model = model
		// Persist only a credential-scoped in-memory ticket. It is never returned
		// by an admin endpoint or logged, and expiry clears it from memory.
		s.bpsTickets.mu.Lock()
		if s.bpsTickets.tickets == nil {
			s.bpsTickets.tickets = map[string]*codexTicketCredential{}
		}
		s.bpsTickets.tickets[key] = ticket
		s.bpsTickets.mu.Unlock()
		status.Ready = true
		status.Length = len(ticket.state)
		status.ExpiresAt = ticket.expires
		status.Reason = "ready"
		return
	}
	return
}

func (s *OpenAIGatewayService) mintBPSTicket(ctx context.Context, a *Account, token, model, proxy string, cfg BPSTicketSettings) (*codexTicketCredential, int, error) {
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), time.Duration(cfg.AttemptTimeoutSeconds)*time.Second)
	defer cancel()
	payload := []byte(`{"model":` + jsonString(model) + `,"instructions":"Reply with OK.","input":[{"role":"user","content":[{"type":"input_text","text":"OK"}]}],"stream":true,"store":false,"reasoning":{"effort":"low"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, errors.New("invalid_mint_request")
	}
	req.Close = true
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("session_id", uuid.NewString())
	applyOpenAICodexTicketHarvestIdentity(req.Header, model)
	if err = resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, a); err != nil {
		return nil, 0, errors.New("account_identity_missing")
	}
	if cfg.TargetLength == 292 {
		req.Header.Set(responsesLiteHeaderKey, "true")
	} else {
		// Preserve the donor's full-request mint identity, separately from the
		// two-shot state probe. Lite tickets must never inherit this shape.
		req.Header.Set("session-id", req.Header.Get("session_id"))
		req.Header.Del("session_id")
		req.Header.Set("User-Agent", "codex-tui/0.154.0 (Ubuntu 24.04; x86_64) OVH (codex-tui; 0.154.0)")
		req.Header.Set("originator", "codex-tui")
		req.Header.Del("version")
	}
	resp, err := s.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, 0, errors.New("mint_transport_failed")
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, errors.New("mint_response_missing")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode, fmt.Errorf("mint_http_%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, 200, errors.New("mint_stream_incomplete")
	}
	if err = validateCodexProbeResponse(data, model); err != nil {
		return nil, 200, err
	}
	state := extractOpenAICodexTurnState(resp.Header)
	now := time.Now()
	issued, err := parseCodexTicketIssuedAt(state, codexTicketExpectedLength(a, cfg), now)
	if err != nil {
		return nil, 200, err
	}
	expires := issued.Add(time.Duration(cfg.TTLSeconds) * time.Second)
	cookies := openAICodexStateRouteCookies(resp)
	if cfg.TargetLength == 780 {
		var routeExpiry time.Time
		cookies, routeExpiry, err = codex780Route(cookies, cfg.TargetGateway, now)
		if err != nil {
			return nil, 200, err
		}
		if routeExpiry.Before(expires) {
			expires = routeExpiry
		}
	}
	if !expires.After(now.Add(time.Duration(cfg.RefreshBeforeSeconds) * time.Second)) {
		return nil, 200, errors.New("ticket_too_close_to_expiry")
	}
	return &codexTicketCredential{state: state, cookies: cookies, expires: expires}, 200, nil
}

func (s *OpenAIGatewayService) doTicketUpstream(req *http.Request, a *Account, model string, cfg BPSTicketSettings) (*http.Response, bool, error) {
	key := codexTicketKey(a, model)
	s.bpsTickets.mu.Lock()
	ticket := s.bpsTickets.tickets[key]
	s.bpsTickets.mu.Unlock()
	unavailable := func(reason string) (*http.Response, bool, error) {
		if cfg.FailClosed {
			return bpsLocalResponse(req, 503, "codex_ticket_unavailable", reason), true, nil
		}
		return nil, false, nil
	}
	if ticket == nil || !time.Now().Before(ticket.expires) {
		return unavailable("No fresh ticket for this account and model")
	}
	if len(ticket.state) == 780 && isOpenAIResponsesLiteHeader(req.Header.Get(responsesLiteHeaderKey)) {
		return unavailable("780 tickets require full Responses requests")
	}
	if _, err := parseCodexTicketIssuedAt(ticket.state, codexTicketExpectedLength(a, cfg), time.Now()); err != nil {
		return unavailable("Ticket validation failed")
	}
	if len(ticket.state) == 780 {
		if _, _, err := codex780Route(ticket.cookies, cfg.TargetGateway, time.Now()); err != nil {
			return unavailable("Ticket route expired or target changed")
		}
	}
	proxy := ""
	release := func() {}
	if ticket.node != "" {
		var err error
		proxy, release, err = mihomo.PinNode(req.Context(), ticket.node)
		if err != nil {
			return unavailable("Ticket proxy node is unavailable")
		}
	} else if ticket.proxyID > 0 {
		p, err := s.settingService.proxyRepo.GetByID(req.Context(), ticket.proxyID)
		if err != nil || !p.IsActive() || p.IsExpired(time.Now()) {
			return unavailable("Ticket proxy is disabled or expired")
		}
		proxy = p.URL()
		if sha256.Sum256([]byte(proxy)) != ticket.proxyDigest {
			return unavailable("Ticket proxy configuration changed; mint a fresh ticket")
		}
	}
	req = req.Clone(WithHTTPUpstreamRedirectsDisabled(req.Context()))
	req.Close = true
	req.Header.Set(openAICodexTurnStateHeader, ticket.state)
	req.Header.Del("Cookie")
	if len(ticket.cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(ticket.cookies, "; "))
	}
	resp, err := s.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil || resp == nil || resp.Body == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		release()
		return nil, true, bpsSafeTransportError(req.Context(), err, "ticket upstream transport failed")
	}
	resp.Body = &bpsTicketReleaseBody{ReadCloser: resp.Body, release: release}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		s.bpsTickets.mu.Lock()
		if s.bpsTickets.tickets[key] == ticket {
			delete(s.bpsTickets.tickets, key)
		}
		s.bpsTickets.mu.Unlock()
	}
	return resp, true, nil
}

// Updating a result never overwrites the user's BPS toggle. Automatic routing
// is a separate per-model override tied to the exact account configuration.
func advanceBPSTicketState(state BPSTicketModelState, result *OpenAICodexStateProbeResult, cfg BPSTicketSettings, switchEnabled bool) BPSTicketModelState {
	state.Probe = result
	switch result.Verdict {
	case OpenAICodexStateDegraded:
		state.DegradedCount++
		state.HealthyCount = 0
		if switchEnabled && state.DegradedCount >= cfg.DegradedThreshold {
			state.AutoBPS = true
		}
	case OpenAICodexStateHealthy:
		state.HealthyCount++
		state.DegradedCount = 0
		if switchEnabled && state.HealthyCount >= cfg.HealthyThreshold {
			state.AutoBPS = false
		}
	default:
		// Unknown/timeouts never enable, disable or restore a route.
		state.DegradedCount = 0
		state.HealthyCount = 0
	}
	return state
}
