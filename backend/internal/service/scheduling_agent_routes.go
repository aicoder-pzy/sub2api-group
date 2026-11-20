package service

import (
	"context"
	"slices"
	"strings"
	"time"
)

const (
	schedulingTrialRoute  = "trial_route"
	schedulingPreferRoute = "prefer_route"
	schedulingTrialPeriod = time.Hour
	schedulingRouteHold   = time.Hour
)

type SchedulingAgentSpeedWindow struct {
	ModelAutoSpeedBucket
	Effort string `json:"effort"`
	Age    int    `json:"age"` // 0: latest 30 minutes; 1: preceding 30 minutes.
}

type schedulingAgentSpeedReader interface {
	GetSchedulingAgentSpeedWindows(context.Context, int64, string, []int64) (map[int64][]SchedulingAgentSpeedWindow, error)
}

type SchedulingAgentRoute struct {
	AccountID   int64     `json:"account_id,omitempty"`
	SelectedAt  time.Time `json:"selected_at"`
	Reason      string    `json:"reason,omitempty"`
	TrialID     int64     `json:"trial_id,omitempty"`
	TrialUntil  time.Time `json:"trial_until"`
	TrialReason string    `json:"trial_reason,omitempty"`
	TrialEffort string    `json:"trial_effort,omitempty"`
}

func schedulingWindowUsable(w SchedulingAgentSpeedWindow) bool {
	return (w.Age == 0 || w.Age == 1) && w.Samples >= 3 && w.P50MS > 0 && w.P95MS >= w.P50MS
}

func schedulingSameCohort(a, b SchedulingAgentSpeedWindow) bool {
	return a.Effort == b.Effort && a.ContextBucket == b.ContextBucket
}

// Compare disjoint windows only within one effort and context cohort. The guard
// rejects noise; deciding whether the experience warrants intervention is the agent's job.
func schedulingRouteEvidence(current, candidate schedulingAgentEvidence) (string, bool, bool) {
	var effort string
	var sustained, comparable bool
	for _, recent := range current.Windows {
		if recent.Age != 0 || !schedulingWindowUsable(recent) || !strings.EqualFold(candidate.QualityEffort, recent.Effort) {
			continue
		}
		for _, past := range current.Windows {
			if past.Age != 1 || !schedulingWindowUsable(past) || !schedulingSameCohort(recent, past) {
				continue
			}
			effort, sustained = recent.Effort, true
			var fresh, previous *SchedulingAgentSpeedWindow
			for _, alternate := range candidate.Windows {
				if !schedulingWindowUsable(alternate) || !schedulingSameCohort(recent, alternate) {
					continue
				}
				if alternate.Age == 0 {
					fresh = &alternate
				} else {
					previous = &alternate
				}
			}
			if fresh != nil && previous != nil {
				comparable = true
				if schedulingMaterialImprovement(recent, *fresh) && schedulingMaterialImprovement(past, *previous) {
					return recent.Effort, true, true
				}
			}
		}
	}
	return effort, sustained && !comparable, false
}

func schedulingMaterialImprovement(current, candidate SchedulingAgentSpeedWindow) bool {
	outputKnown := current.OutputSamples >= 3 && candidate.OutputSamples >= 3 && current.OutputTPS > 0 && candidate.OutputTPS > 0
	latencyBetter := candidate.P95MS <= current.P95MS*0.85 && current.P95MS-candidate.P95MS >= 1000 && candidate.P50MS <= current.P50MS*1.10
	outputBetter := outputKnown && candidate.OutputTPS >= current.OutputTPS*1.20 && candidate.P95MS <= current.P95MS*1.05 && candidate.P50MS <= current.P50MS*1.05
	return (latencyBetter && (!outputKnown || candidate.OutputTPS >= current.OutputTPS*0.90)) || outputBetter
}

func schedulingRouteChange(p SchedulingPolicy, last SchedulingAgentRoute, decision schedulingAgentDecision, evidence []schedulingAgentEvidence, now time.Time) (SchedulingAgentRoute, bool) {
	if strings.TrimSpace(decision.Reason) == "" || !slices.Contains(p.analysisModels(), decision.Model) || !slices.Contains(p.order(decision.Model), decision.AccountID) {
		return last, false
	}
	var current, candidate schedulingAgentEvidence
	for _, item := range evidence {
		if item.Model != decision.Model {
			continue
		}
		if item.Current {
			current = item
		}
		if item.AccountID == decision.AccountID {
			candidate = item
		}
	}
	if current.AccountID == 0 || current.AccountID == decision.AccountID || !candidate.QualityVerified || !schedulingEvidenceHealthy(candidate) {
		return last, false
	}
	if last.AccountID != 0 && current.AccountID == last.AccountID && now.Sub(last.SelectedAt) < schedulingRouteHold {
		return last, false
	}
	effort, sustained, better := schedulingRouteEvidence(current, candidate)
	if !sustained {
		return last, false
	}
	switch decision.Action {
	case schedulingTrialRoute:
		if better || now.Before(last.TrialUntil) {
			return last, false
		}
		last.TrialID, last.TrialUntil, last.TrialReason, last.TrialEffort = decision.AccountID, now.Add(schedulingTrialPeriod), decision.Reason, effort
	case schedulingPreferRoute:
		incumbent := GroupModelAccountQuality{Successes: current.Successes, Failures: current.Failures}
		alternate := GroupModelAccountQuality{Successes: candidate.Successes, Failures: candidate.Failures}
		if !better || alternate.failureRate() > incumbent.failureRate()+0.01 {
			return last, false
		}
		last = SchedulingAgentRoute{AccountID: decision.AccountID, SelectedAt: now, Reason: decision.Reason}
	default:
		return last, false
	}
	return last, true
}

func schedulingRouteCandidate(sample GroupModelAccountQuality) bool {
	return sample.Successes >= 5 && sample.RecentFailures == 0 && sample.Failures <= sample.Successes/10 && !sample.qualityDegraded() && !modelAutoPending(sample)
}

// The order below only applies to new sessions. The caller has already filtered
// the requested model's members and the request's protocol and account eligibility.
func orderSchedulingAgentRoute(ctx context.Context, scope fixedSchedulingScope, snapshot fixedSessionSnapshot, candidates []*Account, samples map[int64]GroupModelAccountQuality, route SchedulingAgentRoute) []*Account {
	if route.AccountID > 0 && schedulingRouteCandidate(samples[route.AccountID]) {
		candidates = moveModelAutoFirst(candidates, route.AccountID)
	}
	if route.TrialID <= 0 || route.TrialID == candidates[0].ID || !time.Now().Before(route.TrialUntil) || ctx.Value(qualitySchedulingReadOnlyKey{}) != nil {
		return candidates
	}
	requested := RequestedReasoningEffortFromContext(ctx)
	if (requested == nil && route.TrialEffort != "") || (requested != nil && !strings.EqualFold(*requested, route.TrialEffort)) {
		return candidates
	}
	trial := samples[route.TrialID]
	if !schedulingRouteCandidate(trial) || !smartChannelTrialEligible(WithRequestedReasoningEffort(ctx, route.TrialEffort), trial) ||
		!slices.ContainsFunc(candidates, func(a *Account) bool { return a.ID == route.TrialID }) {
		return candidates
	}
	cache, ok := snapshot.cache.(ModelAutoSchedulingCache)
	if !ok {
		return candidates
	}
	primary := candidates[0].ID
	claim, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	// Give agent trials their own Redis namespace; share the existing cooldown
	// and one-in-ten new session limit across replicas and across trial decisions.
	selected, _, err := cache.AssignModelAutoSession(claim, scope.groupID, "\x00scheduling-agent:"+scope.model, snapshot.key, primary, []int64{primary, route.TrialID}, []int64{route.TrialID}, time.Minute)
	if err == nil && selected == route.TrialID {
		return moveModelAutoFirst(candidates, selected)
	}
	return candidates
}
