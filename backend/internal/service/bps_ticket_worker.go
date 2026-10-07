package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *OpenAIGatewayService) StartBPSTicketWorker() {
	if s == nil || s.accountRepo == nil || s.settingService == nil {
		return
	}
	s.bpsTickets.mu.Lock()
	defer s.bpsTickets.mu.Unlock()
	if s.bpsTickets.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.bpsTickets.cancel = cancel
	s.bpsTickets.done = make(chan struct{})
	go func() {
		defer close(s.bpsTickets.done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runBPSTicketRound(ctx)
			}
		}
	}()
}

func (s *OpenAIGatewayService) StopBPSTicketWorker() {
	if s == nil {
		return
	}
	s.bpsTickets.mu.Lock()
	cancel, done := s.bpsTickets.cancel, s.bpsTickets.done
	s.bpsTickets.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *OpenAIGatewayService) runBPSTicketRound(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cfg, err := s.GetBPSTicketSettings(ctx)
	if err != nil {
		logger.LegacyPrintf("service.bps_ticket", "settings unavailable; skipping background round")
		return
	}
	s.bpsTickets.mu.Lock()
	s.bpsTickets.settings = cfg
	s.bpsTickets.settingsAt = time.Now()
	for key, ticket := range s.bpsTickets.tickets {
		if !time.Now().Before(ticket.expires) {
			delete(s.bpsTickets.tickets, key)
		}
	}
	for key, until := range s.bpsTickets.bpsCooldown {
		if !time.Now().Before(until) {
			delete(s.bpsTickets.bpsCooldown, key)
		}
	}
	for key, at := range s.bpsTickets.lastHarvest {
		if time.Since(at) > 24*time.Hour {
			delete(s.bpsTickets.lastHarvest, key)
			delete(s.bpsTickets.harvestStatus, key)
		}
	}
	s.bpsTickets.mu.Unlock()
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return
	}
	managedTarget, staticTarget := 0, 0
	for i := range accounts {
		a := &accounts[i]
		ac := a.BPSTicketConfig()
		if !bpsEligible(a) || a.Status != StatusActive || !a.Schedulable || (!ac.BPS && !ac.AutoSwitch) {
			continue
		}
		if ac.ProxySource == "mihomo" {
			managedTarget += a.Concurrency
		}
		if ac.ProxySource == "static" {
			staticTarget += a.Concurrency
		}
	}
	urls := []string{}
	if len(cfg.ProxyIDs) > 0 && s.settingService.proxyRepo != nil {
		proxies, e := s.settingService.proxyRepo.ListByIDs(ctx, cfg.ProxyIDs)
		if e != nil {
			return
		}
		for _, p := range proxies {
			if p.IsActive() && !p.IsExpired(time.Now()) {
				urls = append(urls, p.URL())
			}
		}
	}
	mihomo.SetBPSStaticProxies(urls)
	mihomo.WarmBPSPools(ctx, managedTarget, staticTarget)
	// Bound parallel background jobs. Every account also has a shared manual/
	// scheduled lock, preventing probes and mint rounds from overlapping.
	sem := make(chan struct{}, 2)
	var jobs sync.WaitGroup
	defer jobs.Wait()
	for i := range accounts {
		if ctx.Err() != nil {
			break
		}
		a := accounts[i]
		if !bpsTicketEligible(&a) || a.Status != StatusActive || !a.Schedulable {
			continue
		}
		ac := a.BPSTicketConfig()
		if (!cfg.HarvestEnabled || !ac.Tickets) && (!cfg.AutoProbeEnabled || !ac.AutoProbe) {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		jobs.Add(1)
		go func() { defer jobs.Done(); defer func() { <-sem }(); s.runBPSTicketAccount(ctx, &a, cfg) }()
	}
}

func (s *OpenAIGatewayService) runBPSTicketAccount(ctx context.Context, a *Account, cfg BPSTicketSettings) {
	release, ok := s.beginBPSTicketJob(a.ID)
	if !ok {
		return
	}
	defer release()
	ac := a.BPSTicketConfig()
	state := a.BPSTicketState()
	changed := false
	for _, requested := range ac.Models {
		if ctx.Err() != nil {
			break
		}
		model := normalizeOpenAIModelForUpstream(a, a.GetMappedModel(requested))
		key := codexTicketKey(a, model)
		if cfg.AutoProbeEnabled && ac.AutoProbe {
			previous := state.Models[model]
			if previous.Probe == nil || time.Since(previous.Probe.FinishedAt) >= time.Duration(cfg.ProbeIntervalSeconds)*time.Second {
				result := s.probeOpenAICodexState(ctx, a, requested, true)
				state.Models[model] = advanceBPSTicketState(previous, result, cfg, ac.AutoSwitch && bpsEligible(a))
				changed = true
			}
		}
		if cfg.HarvestEnabled && ac.Tickets && !ac.BPS && !state.Models[model].AutoBPS {
			s.bpsTickets.mu.Lock()
			last := s.bpsTickets.lastHarvest[key]
			ticket := s.bpsTickets.tickets[key]
			s.bpsTickets.mu.Unlock()
			needs := ticket == nil || time.Until(ticket.expires) <= time.Duration(cfg.RefreshBeforeSeconds)*time.Second
			if needs && time.Since(last) >= time.Duration(cfg.HarvestIntervalSeconds)*time.Second {
				s.harvestBPSTicket(ctx, a, model, cfg)
			}
		}
	}
	if changed && ctx.Err() == nil {
		// A concurrent admin/credential update invalidates this snapshot.
		latest, err := s.accountRepo.GetByID(ctx, a.ID)
		if err == nil && bpsTicketAccountRevision(latest) == state.Revision {
			if err = s.accountRepo.UpdateExtra(ctx, a.ID, map[string]any{bpsTicketStateKey: state}); err != nil {
				logger.LegacyPrintf("service.bps_ticket", "could not persist account probe state: account_id=%d", a.ID)
			}
		}
	}
}
