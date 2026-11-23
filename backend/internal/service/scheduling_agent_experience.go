package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
)

// The repository sends aggregate evidence and pseudonyms, never client IDs.
type SchedulingAgentCacheIncident struct {
	Model      string                        `json:"model"`
	At         time.Time                     `json:"window_end"`
	AccountID  int64                         `json:"account_id"`
	Affected   int                           `json:"affected_sessions"`
	Comparable int                           `json:"comparable_sessions"`
	Low        []SchedulingAgentCacheSession `json:"low_sessions"`
}

type SchedulingAgentCacheSession struct {
	Session          string  `json:"session"`
	Effort           string  `json:"effort"`
	Endpoint         string  `json:"endpoint"`
	Protocol         int     `json:"protocol"`
	Bucket           int     `json:"context_bucket"`
	Baseline         float64 `json:"baseline"`
	Previous         float64 `json:"previous"`
	Latest           float64 `json:"latest"`
	PreviousRequests int     `json:"previous_requests"`
	LatestRequests   int     `json:"latest_requests"`
	Compacted        bool    `json:"compacted"`
	ChangedAccount   bool    `json:"changed_account"`
}

func (i *SchedulingAgentCacheIncident) channelWide() bool {
	return i != nil && i.Affected >= 2
}

func cacheEvidenceSupportsTrial(incident *SchedulingAgentCacheIncident, candidate schedulingAgentEvidence, effort string, now time.Time) bool {
	if !incident.channelWide() || (effort != "" && !strings.EqualFold(effort, candidate.QualityEffort)) {
		return false
	}
	monitorHealthy := hasHealthySchedulingMonitor(candidate.Monitors, now)
	comparable := 0
	for _, session := range incident.Low {
		if session.Compacted || session.ChangedAccount || session.Effort != candidate.QualityEffort {
			continue
		}
		if monitorHealthy || (candidate.CacheRatio != nil && *candidate.CacheRatio >= math.Max(session.Latest, session.Previous)+0.10) {
			comparable++
		}
	}
	return comparable >= 2
}

type SchedulingAgentTrialCohort struct {
	Effort          string                      `json:"effort"`
	Endpoint        string                      `json:"endpoint"`
	Protocol        int                         `json:"protocol"`
	Bucket          int                         `json:"context_bucket"`
	TrialSessions   int                         `json:"trial_sessions"`
	ControlSessions int                         `json:"control_sessions"`
	TrialRequests   int                         `json:"trial_warm_requests"`
	ControlRequests int                         `json:"control_warm_requests"`
	TrialCache      float64                     `json:"trial_cache"`
	ControlCache    float64                     `json:"control_cache"`
	TrialP95        float64                     `json:"trial_first_event_p95_ms"`
	ControlP95      float64                     `json:"control_first_event_p95_ms"`
	Stages          []SchedulingAgentTrialStage `json:"matched_turn_stages,omitempty"`
}

type SchedulingAgentTrialStage struct {
	Stage           string  `json:"stage"`
	TrialSessions   int     `json:"trial_sessions"`
	ControlSessions int     `json:"control_sessions"`
	TrialCache      float64 `json:"trial_cache"`
	ControlCache    float64 `json:"control_cache"`
	TrialP95        float64 `json:"trial_first_event_p95_ms"`
	ControlP95      float64 `json:"control_first_event_p95_ms"`
}

type SchedulingAgentTrialResult struct {
	AccountID        int64                        `json:"account_id"`
	ControlID        int64                        `json:"control_id"`
	StartedAt        time.Time                    `json:"started_at"`
	Cohorts          []SchedulingAgentTrialCohort `json:"matched_cohorts"`
	TrialAssigned    int                          `json:"trial_assigned_sessions"`
	ControlAssigned  int                          `json:"control_assigned_sessions"`
	TrialCompleted   int                          `json:"trial_sessions_with_completion_on_assigned_account"`
	ControlCompleted int                          `json:"control_sessions_with_completion_on_assigned_account"`
}

func (t *SchedulingAgentTrialResult) samples() int {
	n := 0
	for _, c := range t.Cohorts {
		if c.TrialSessions >= 2 && c.ControlSessions >= 2 && min(c.TrialRequests, c.ControlRequests) >= 3 {
			n += min(c.TrialRequests, c.ControlRequests)
		}
	}
	return n
}

func (t *SchedulingAgentTrialResult) improves(effort string, requireCache bool) bool {
	if t.TrialAssigned >= 3 && t.ControlAssigned >= 3 &&
		float64(t.TrialCompleted)/float64(t.TrialAssigned)+0.15 < float64(t.ControlCompleted)/float64(t.ControlAssigned) {
		return false
	}
	better := false
	for _, c := range t.Cohorts {
		if c.Effort != effort {
			continue
		}
		for _, stage := range c.Stages {
			if stage.Stage == "first" && stage.TrialSessions >= 3 && stage.ControlSessions >= 3 && stage.TrialP95 > stage.ControlP95*1.5+1000 {
				return false
			}
		}
		if c.TrialSessions < 2 || c.ControlSessions < 2 || min(c.TrialRequests, c.ControlRequests) < 3 {
			continue
		}
		if c.TrialCache+0.05 < c.ControlCache || c.TrialP95 > c.ControlP95*1.15+500 {
			return false
		}
		cacheBetter := c.TrialCache >= c.ControlCache+0.10
		speedBetter := c.TrialP95 <= c.ControlP95*0.85 && c.ControlP95-c.TrialP95 >= 1000
		better = better || cacheBetter || (!requireCache && speedBetter)
	}
	return better
}

type schedulingAgentExperienceReader interface {
	GetSchedulingAgentCacheIncidents(context.Context, int64, []string) ([]SchedulingAgentCacheIncident, error)
	GetSchedulingAgentTrialCohorts(context.Context, int64, string, SchedulingAgentRoute, map[string]int64) (SchedulingAgentTrialResult, error)
}

type schedulingAgentTrialReader interface {
	GetSchedulingAgentTrialAssignments(context.Context, int64, string, time.Time) (map[string]int64, error)
}

type schedulingAgentSessionIdentityKey struct{}
type schedulingAgentTrialAssignmentKey struct{}

// Use the same identity as usage logs. The key ID scopes caller-provided IDs.
func WithSchedulingAgentSessionIdentity(ctx context.Context, key int64, session string) context.Context {
	if key <= 0 || session == "" {
		return ctx
	}
	return context.WithValue(ctx, schedulingAgentSessionIdentityKey{}, strconv.FormatInt(key, 10)+":"+session)
}

func SchedulingAgentTrialAssignment(ctx context.Context) (time.Time, string) {
	started, _ := ctx.Value(schedulingAgentTrialAssignmentKey{}).(time.Time)
	session, _ := ctx.Value(schedulingAgentSessionIdentityKey{}).(string)
	if started.IsZero() || session == "" {
		return started, ""
	}
	digest := sha256.Sum256([]byte(session))
	return started, hex.EncodeToString(digest[:])
}

func (r *schedulingAgentRuntime) nextExperienceActivity(ctx context.Context, group int64, models []string, run SchedulingAgentRun) (SchedulingAgentActivity, error) {
	reader, ok := r.observations.(schedulingAgentExperienceReader)
	if !ok || len(models) == 0 {
		return SchedulingAgentActivity{}, nil
	}
	now := time.Now()
	if r.trials != nil {
		for _, model := range models {
			route := run.Routes[model]
			if route.TrialID == 0 || route.TrialStartedAt.IsZero() || now.Before(route.TrialStartedAt.Add(10*time.Minute)) || now.After(route.TrialUntil.Add(10*time.Minute)) {
				continue
			}
			read, cancel := context.WithTimeout(ctx, 2*time.Second)
			assignments, err := r.trials.GetSchedulingAgentTrialAssignments(read, group, model, route.TrialStartedAt)
			result := SchedulingAgentTrialResult{AccountID: route.TrialID, ControlID: route.TrialControlID, StartedAt: route.TrialStartedAt}
			if err == nil && len(assignments) >= 4 {
				result, err = reader.GetSchedulingAgentTrialCohorts(read, group, model, route, assignments)
			}
			cancel()
			if err == nil && (result.samples() >= route.TrialReviewedSamples+3 ||
				min(result.TrialAssigned, result.ControlAssigned) >= route.TrialReviewedAssignments+3) {
				return SchedulingAgentActivity{Model: model, At: now, Trial: &result}, nil
			}
		}
	}
	read, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	window := now.Add(-time.Minute).Truncate(10 * time.Minute)
	if scan := r.cacheScans[group]; scan.window.Equal(window) && (len(scan.incidents) == 0 || scan.incidents[0].At.Equal(window)) {
		return cacheIncidentActivity(scan.incidents, run.Reviews), nil
	}
	incidents, err := reader.GetSchedulingAgentCacheIncidents(read, group, models)
	if err != nil {
		return SchedulingAgentActivity{}, err
	}
	if r.cacheScans == nil {
		r.cacheScans = make(map[int64]schedulingAgentCacheScan)
	}
	if len(r.cacheScans) >= 1024 {
		clear(r.cacheScans)
	}
	r.cacheScans[group] = schedulingAgentCacheScan{window: window, incidents: incidents}
	return cacheIncidentActivity(incidents, run.Reviews), nil
}

type schedulingAgentCacheScan struct {
	window    time.Time
	incidents []SchedulingAgentCacheIncident
}

func cacheIncidentActivity(incidents []SchedulingAgentCacheIncident, reviews map[string]schedulingAgentReview) SchedulingAgentActivity {
	for _, incident := range incidents {
		review := reviews[incident.Model]
		escalated := incident.channelWide() && (review.CacheAffected < 2 || review.CacheAccount != incident.AccountID)
		if incident.At.After(review.CacheThrough) && (escalated || incident.At.After(review.CacheThrough.Add(20*time.Minute))) {
			return SchedulingAgentActivity{Model: incident.Model, At: incident.At, Cache: &incident}
		}
	}
	return SchedulingAgentActivity{}
}
