package service

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// Evidence stays separate from legacy failover statistics and never labels
// unknown tests as passing. Latencies contain only the requested effort.
type AccountQualityEvidence struct {
	Successes7D       int64      `json:"successes_7d"`
	Failures7D        int64      `json:"failures_7d"`
	Successes1H       int64      `json:"successes_1h"`
	Failures1H        int64      `json:"failures_1h"`
	Normal            int64      `json:"normal"`
	Degraded          int64      `json:"degraded"`
	Unknown           int64      `json:"unknown"`
	FailedTests       int64      `json:"failed_tests"`
	NormalStreak      int64      `json:"normal_streak"`
	AnswerNormal      int64      `json:"answer_normal"`
	AnswerDegraded    int64      `json:"answer_degraded"`
	LatestVerdict     string     `json:"latest_verdict"`
	LatestReason      string     `json:"latest_reason"`
	TestLatencyMS     float64    `json:"test_latency_ms"`
	LatestAt          *time.Time `json:"latest_at"`
	LastTestAt        *time.Time `json:"last_test_at"`
	Effort            string     `json:"effort"`
	TestEffort        string     `json:"test_effort"`
	LatencyP50MS      float64    `json:"latency_p50_ms"`
	LatencyP95MS      float64    `json:"latency_p95_ms"`
	LatencySamples    int64      `json:"latency_samples"`
	SuccessLowerBound float64    `json:"success_lower_bound"`
	QualityLowerBound float64    `json:"quality_lower_bound"`
	Tier              string     `json:"tier"`
}

type qualityPreferredRepository interface {
	GetQualityPreferredAccountQuality(context.Context, int64, string, string) (map[int64]GroupModelAccountQuality, error)
}

type qualitySchedulingProbeKey struct{}
type qualitySchedulingReadOnlyKey struct{}
type qualitySessionBindingKey struct{}
type qualityProbeReservations struct {
	sync.Mutex
	accounts map[int64]bool
}

func qualityPreferredEnabled(ctx context.Context) bool {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	return group != nil && group.AccountSchedulingMode == AccountSchedulingModeQualityPreferred
}

func qualityProbe(ctx context.Context) bool { return ctx.Value(qualitySchedulingProbeKey{}) != nil }

// Wilson's 95% lower bound keeps a handful of successes from beating a channel
// with hundreds of verified requests just because its displayed rate is 100%.
func QualitySuccessLowerBound(successes, failures int64) float64 {
	n := float64(successes + failures)
	if n <= 0 {
		return 0
	}
	p, z := float64(successes)/n, 1.96
	return (p + z*z/(2*n) - z*math.Sqrt((p*(1-p)+z*z/(4*n))/n)) / (1 + z*z/n)
}

func (e *AccountQualityEvidence) tier(now time.Time) int {
	if e == nil {
		return 2
	}
	if e.LatestAt == nil || now.Sub(*e.LatestAt) > 24*time.Hour {
		return 2
	}
	if e.LatestVerdict == "degraded" {
		return 3
	}
	if e.LatestVerdict == "normal" && e.NormalStreak >= 2 {
		return 0
	}
	if e.LatestVerdict == "normal" {
		return 1
	}
	return 2
}

func QualityEvidenceTier(e *AccountQualityEvidence) string {
	switch e.tier(time.Now()) {
	case 0:
		return "verified"
	case 1:
		return "provisional"
	case 3:
		return "degraded"
	default:
		return "unknown"
	}
}

func orderQualityCandidates(candidates []*Account, samples map[int64]GroupModelAccountQuality, minimum int64) []*Account {
	if minimum < 1 {
		minimum = 10
	}
	ordered := append([]*Account(nil), candidates...)
	now := time.Now()
	allDegraded := len(ordered) > 0
	for _, account := range ordered {
		allDegraded = allDegraded && samples[account.ID].qualityDegraded()
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		x, y := samples[a.ID], samples[b.ID]
		if allDegraded {
			return qualityLatencyLess(x, y, a.ID, b.ID)
		}
		if x.qualityDegraded() != y.qualityDegraded() {
			return !x.qualityDegraded()
		}
		if (x.reliabilityTier(minimum) == 3) != (y.reliabilityTier(minimum) == 3) {
			return x.reliabilityTier(minimum) != 3
		}
		if accountSchedulingPreferred(a) != accountSchedulingPreferred(b) {
			return accountSchedulingPreferred(a)
		}
		if accountSchedulingPreferred(a) && a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if x.Quality.tier(now) != y.Quality.tier(now) {
			return x.Quality.tier(now) < y.Quality.tier(now)
		}
		if x.Quality == nil {
			x.Quality = &AccountQualityEvidence{}
		}
		if y.Quality == nil {
			y.Quality = &AccountQualityEvidence{}
		}
		if x.reliabilityTier(minimum) != y.reliabilityTier(minimum) {
			return x.reliabilityTier(minimum) < y.reliabilityTier(minimum)
		}
		if x.RecentFailures != y.RecentFailures {
			return x.RecentFailures < y.RecentFailures
		}
		if x.Quality != nil && y.Quality != nil {
			if qualityBucket(x.Quality.QualityLowerBound) != qualityBucket(y.Quality.QualityLowerBound) {
				return x.Quality.QualityLowerBound > y.Quality.QualityLowerBound
			}
			// A fixed key keeps comparisons transitive even near a threshold.
			xHour, yHour := qualityRecentFailureTier(x.Quality, minimum), qualityRecentFailureTier(y.Quality, minimum)
			if xHour != yHour {
				return xHour < yHour
			}
		}
		xRate, yRate := QualitySuccessLowerBound(x.Successes, x.Failures), QualitySuccessLowerBound(y.Successes, y.Failures)
		if qualityBucket(xRate) != qualityBucket(yRate) {
			return xRate > yRate
		}
		if x.Quality != nil && y.Quality != nil && qualityBucket(x.Quality.SuccessLowerBound) != qualityBucket(y.Quality.SuccessLowerBound) {
			return x.Quality.SuccessLowerBound > y.Quality.SuccessLowerBound
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return qualityLatencyLess(x, y, a.ID, b.ID)
	})
	return ordered
}

func qualityBucket(value float64) int { return int(math.Floor(value / 0.02)) }
func qualityRecentFailureTier(e *AccountQualityEvidence, minimum int64) int {
	if e == nil || e.Successes1H+e.Failures1H < minimum {
		return 1
	}
	if float64(e.Failures1H)/float64(e.Successes1H+e.Failures1H) > 0.05 {
		return 2
	}
	return 0
}

func qualityLatencyLess(a, b GroupModelAccountQuality, aID, bID int64) bool {
	x, y := a.LatencyMS, b.LatencyMS
	if a.Quality != nil {
		x = a.Quality.LatencyP95MS
		if x <= 0 {
			x = a.Quality.TestLatencyMS
		}
	}
	if b.Quality != nil {
		y = b.Quality.LatencyP95MS
		if y <= 0 {
			y = b.Quality.TestLatencyMS
		}
	}
	if x != y {
		return x > 0 && (y <= 0 || x < y)
	}
	return aID < bID
}

func orderSchedulingCandidates(ctx context.Context, candidates []*Account, samples map[int64]GroupModelAccountQuality, activeID int64, settings FastestFailoverSettings, retainBinding bool) []*Account {
	if !qualityPreferredEnabled(ctx) {
		return orderFastestFailoverCandidates(candidates, samples, activeID, settings)
	}
	if settings.MinimumSamples < 1 {
		settings.MinimumSamples = 10
	}
	if qualityProbe(ctx) {
		ordered := append([]*Account(nil), candidates...)
		reservations, _ := ctx.Value(qualitySchedulingProbeKey{}).(*qualityProbeReservations)
		if reservations != nil {
			reservations.Lock()
			defer reservations.Unlock()
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			if reservations != nil && reservations.accounts[ordered[i].ID] != reservations.accounts[ordered[j].ID] {
				return !reservations.accounts[ordered[i].ID]
			}
			x, y := samples[ordered[i].ID].Quality, samples[ordered[j].ID].Quality
			var a, b *time.Time
			if x != nil {
				a = x.LastTestAt
			}
			if y != nil {
				b = y.LastTestAt
			}
			if a == nil || b == nil {
				if a != b {
					return a == nil
				}
				return ordered[i].ID < ordered[j].ID
			}
			if !a.Equal(*b) {
				return a.Before(*b)
			}
			return ordered[i].ID < ordered[j].ID
		})
		if reservations != nil && len(ordered) > 0 {
			reservations.accounts[ordered[0].ID] = true
		}
		return ordered
	}
	ordered := orderQualityCandidates(candidates, samples, int64(settings.MinimumSamples))
	if retainBinding {
		if sessionID, _ := ctx.Value(qualitySessionBindingKey{}).(int64); sessionID > 0 {
			sample := samples[sessionID]
			if !sample.qualityDegraded() && sample.reliabilityTier(int64(settings.MinimumSamples)) != 3 {
				for i, account := range ordered {
					if account.ID == sessionID {
						if state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState); state != nil {
							state.mu.Lock()
							state.preservedQualityAccountID = sessionID
							state.mu.Unlock()
						}
						copy(ordered[1:i+1], ordered[:i])
						ordered[0] = account
						return ordered
					}
				}
			}
		}
	}
	if !retainBinding || len(ordered) == 0 || activeID == 0 {
		return ordered
	}
	activeIndex := -1
	for i, a := range ordered {
		if a.ID == activeID {
			activeIndex = i
			break
		}
	}
	if activeIndex < 0 {
		recordSchedulingSelectionReason(ctx, activeID, "原账号不在本次可选范围")
		return ordered
	}
	current := samples[activeID]
	// Only a bad or unavailable current channel may yield to candidate ranking.
	if current.qualityDegraded() || current.reliabilityTier(int64(settings.MinimumSamples)) == 3 {
		reason := "原账号近期请求不稳定"
		if current.qualityDegraded() {
			reason = "原账号冷却 5 分钟后复测仍降智"
		}
		recordSchedulingSelectionReason(ctx, activeID, reason)
		return ordered
	}
	// Preferences and relative quality/speed rank replacements; they never
	// preempt a healthy primary, including when a former primary recovers.
	active := ordered[activeIndex]
	copy(ordered[1:activeIndex+1], ordered[:activeIndex])
	ordered[0] = active
	return ordered
}
