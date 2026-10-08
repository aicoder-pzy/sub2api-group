package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const maxSchedulingProxyFallbackHops = 4

type schedulingProxyEgress struct {
	url  string
	id   int64
	name string
}

type schedulingProxyEgressKey struct{}
type schedulingProxyEgressError struct {
	error
	egress schedulingProxyEgress
}

func (e *schedulingProxyEgressError) Unwrap() error { return e.error }

func schedulingProxyRepository(settings *SettingService) ProxyRepository {
	if settings == nil {
		return nil
	}
	return settings.proxyRepo
}

type schedulingProxyChain struct {
	current *Proxy
	visited map[int64]bool
	urls    map[string]bool
	hops    int
}

func newSchedulingProxyChain(primary *Proxy) *schedulingProxyChain {
	return &schedulingProxyChain{current: primary, visited: map[int64]bool{primary.ID: true}, urls: map[string]bool{primary.URL(): true}}
}

func (chain *schedulingProxyChain) next(ctx context.Context, repo ProxyRepository, blocked func(int64) bool) (schedulingProxyEgress, bool) {
	for chain.current != nil && ctx.Err() == nil {
		current := chain.current
		chain.current = nil
		switch current.FallbackMode {
		case FallbackModeDirect:
			return schedulingProxyEgress{}, true
		case FallbackModeProxy:
			id := current.BackupProxyID
			if repo == nil || id == nil || *id <= 0 || chain.visited[*id] || chain.hops >= maxSchedulingProxyFallbackHops {
				return schedulingProxyEgress{}, false
			}
			chain.visited[*id], chain.hops = true, chain.hops+1
			proxy, err := repo.GetByID(ctx, *id)
			if err != nil || proxy == nil || proxy.ID != *id {
				return schedulingProxyEgress{}, false
			}
			chain.current = proxy
			url := proxy.URL()
			duplicate := chain.urls[url]
			chain.urls[url] = true
			if !duplicate && proxy.IsActive() && !proxy.IsExpired(time.Now()) && (blocked == nil || !blocked(proxy.ID)) {
				return schedulingProxyEgress{url, proxy.ID, proxy.Name}, true
			}
		default:
			return schedulingProxyEgress{}, false
		}
	}
	return schedulingProxyEgress{}, false
}

// Each hop independently proves that HTTP never received a connection.
// No trace evidence, a response, or any possible write vetoes egress replay.
func schedulingProxyAttempt(req *http.Request, target schedulingProxyEgress, send func(*http.Request, string) (*http.Response, error)) (*http.Response, error, bool) {
	var mutex sync.Mutex
	var connecting, submitted bool
	markSubmitted := func() { mutex.Lock(); submitted = true; mutex.Unlock() }
	trace := &httptrace.ClientTrace{
		GetConn:              func(string) { mutex.Lock(); connecting = true; mutex.Unlock() },
		GotConn:              func(httptrace.GotConnInfo) { markSubmitted() },
		WroteHeaderField:     func(string, []string) { markSubmitted() },
		WroteHeaders:         markSubmitted,
		WroteRequest:         func(httptrace.WroteRequestInfo) { markSubmitted() },
		GotFirstResponseByte: markSubmitted,
	}
	ctx := context.WithValue(req.Context(), schedulingProxyEgressKey{}, target)
	attemptReq := req.Clone(httptrace.WithClientTrace(ctx, trace))
	resp, err := send(attemptReq, target.url)
	if resp != nil {
		resp.Request = attemptReq
	}
	if err != nil {
		err = &schedulingProxyEgressError{err, target}
	}
	mutex.Lock()
	unsent := connecting && !submitted
	mutex.Unlock()
	return resp, err, unsent
}

func doSchedulingProxyFallback(req *http.Request, account *Account, primaryURL string, repo ProxyRepository, blocked func(int64) bool, send func(*http.Request, string) (*http.Response, error)) (*http.Response, error) {
	group, _ := req.Context().Value(ctxkey.Group).(*Group)
	if group == nil || !fastestFailoverEnabled(req.Context(), &group.ID) || account == nil || account.Proxy == nil || primaryURL == "" || primaryURL != account.Proxy.URL() {
		return send(req, primaryURL)
	}
	chain := newSchedulingProxyChain(account.Proxy)
	target := schedulingProxyEgress{primaryURL, account.Proxy.ID, account.Proxy.Name}
	if blocked != nil && blocked(target.id) {
		if alternate, ok := chain.next(req.Context(), repo, blocked); ok {
			target = alternate
		}
	}
	attemptReq := req
	for {
		resp, err, unsent := schedulingProxyAttempt(attemptReq, target, send)
		if err == nil || resp != nil || !unsent || target.id == 0 || req.Context().Err() != nil || req.GetBody == nil || !classifyUpstreamTransportError(err).Persistent {
			return resp, err
		}
		alternate, ok := chain.next(req.Context(), repo, blocked)
		if !ok {
			return resp, err
		}
		body, cloneErr := req.GetBody()
		if cloneErr != nil {
			return resp, err
		}
		attemptReq = req.Clone(req.Context())
		attemptReq.Body = body
		target = alternate
	}
}

func schedulingProxyErrorAttribution(account *Account, err error) (*int64, string) {
	var egressErr *schedulingProxyEgressError
	if !errors.As(err, &egressErr) {
		return opsUpstreamProxyAttribution(account)
	}
	return schedulingProxyAttribution(egressErr.egress)
}

func schedulingProxyAttribution(egress schedulingProxyEgress) (*int64, string) {
	if egress.id == 0 {
		return nil, opsProxyNameDirect
	}
	id, name := egress.id, strings.TrimSpace(egress.name)
	if name == "" {
		name = opsProxyNameUnnamed
	}
	return &id, name
}

func (s *GatewayService) doGatewayUpstream(req *http.Request, proxyURL string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return doFastestFailoverUpstream(s.httpUpstream, req, proxyURL, account.ID, account.Concurrency, profile,
		func(request *http.Request) (*http.Response, error) {
			return doSchedulingProxyFallback(request, account, proxyURL, schedulingProxyRepository(s.settingService), nil,
				func(attempt *http.Request, url string) (*http.Response, error) {
					return s.httpUpstream.DoWithTLS(attempt, url, account.ID, account.Concurrency, profile)
				})
		})
}

func (s *GeminiMessagesCompatService) doGeminiUpstream(req *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	return doFastestFailoverUpstream(s.httpUpstream, req, proxyURL, account.ID, account.Concurrency, nil,
		func(request *http.Request) (*http.Response, error) {
			return doSchedulingProxyFallback(request, account, proxyURL, schedulingProxyRepository(s.schedulingSettingsService()), nil,
				func(attempt *http.Request, url string) (*http.Response, error) {
					return s.httpUpstream.Do(attempt, url, account.ID, account.Concurrency)
				})
		})
}
