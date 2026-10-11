package service

import (
	"context"
	"math"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	accountRateLimitExceeded = "account_rate_limit_exceeded"
	accountRateLimitUnknown  = "account_rate_limit_unknown"
)

func ValidateAccountRateLimitConfig(platform string, enabled bool, limit float64) error {
	if !enabled {
		return nil
	}
	if !profitControlPlatformSupported(platform) || math.IsNaN(limit) || math.IsInf(limit, 0) ||
		limit < 0.0001 || limit > 999999.9999 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_RATE_LIMIT", "Choose a supported token platform and a multiplier limit between 0.0001 and 999999.9999")
	}
	return nil
}

func groupAccountRateLimit(group *Group) *float64 {
	if group == nil || !group.AccountRateLimitEnabled || !profitControlPlatformSupported(group.Platform) {
		return nil
	}
	limit := group.AccountRateLimitMultiplier
	return &limit
}

// A known upstream billing declaration must remain authoritative when a probe
// fails or expires. Expired evidence freezes eligibility until a fresh probe.
// Falling back to a cheaper manual rate would reopen a frozen
// account without evidence that its actual rate has recovered.
func accountRateForLimit(account *Account, now time.Time) (float64, bool) {
	if account == nil {
		return 0, false
	}
	if isUpstreamBillingProbeAccount(account) {
		snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
		if snapshot != nil && snapshot.Source == "newapi" && (snapshot.ReceivedAt == nil || len(snapshot.Data) == 0) {
			return 0, false
		}
		if snapshot != nil && snapshot.ReceivedAt != nil && len(snapshot.Data) > 0 {
			return openAIFreshUpstreamBillingRate(account, now)
		}
	}
	if account.RateMultiplier == nil || *account.RateMultiplier < 0 ||
		math.IsNaN(*account.RateMultiplier) || math.IsInf(*account.RateMultiplier, 0) {
		return 0, false
	}
	return *account.RateMultiplier, true
}

func accountRateLimitVeto(limit *float64, account *Account, now time.Time) (bool, string) {
	if limit == nil {
		return false, ""
	}
	rate, known := accountRateForLimit(account, now)
	if !known {
		return true, accountRateLimitUnknown
	}
	if math.IsNaN(*limit) || math.IsInf(*limit, 0) || rate > *limit {
		return true, accountRateLimitExceeded
	}
	return false, ""
}

func accountNeedsRateLimitProbe(account *Account) bool {
	if !isUpstreamBillingProbeAccount(account) {
		return false
	}
	for _, group := range account.Groups {
		if group != nil && group.IsActive() && groupAccountRateLimit(group) != nil {
			return true
		}
	}
	return false
}

func upstreamBillingProbeRequired(account *Account) bool {
	return upstreamBillingProbeEnabled(account) || accountNeedsRateLimitProbe(account)
}

type accountRateLimitReader func(context.Context, int64) (*Account, error)

func rateLimitAccountReader(snapshot *SchedulerSnapshotService, repo AccountRepository) accountRateLimitReader {
	if snapshot != nil {
		return snapshot.GetAccount
	}
	if repo != nil {
		return repo.GetByID
	}
	return nil
}

// Recheck immediately before each submission. A request already submitted may
// finish; the next request or WS turn must select an eligible account.
func checkAccountRateLimitLatest(ctx context.Context, accountID int64) error {
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	if gate == nil || gate.accountRateLimit == nil || gate.readAccount == nil {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	account, err := gate.readAccount(readCtx, accountID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || account == nil {
		return accountRateLimitFailoverError(accountRateLimitUnknown)
	}
	if vetoed, reason := accountRateLimitVeto(gate.accountRateLimit, account, time.Now()); vetoed {
		return accountRateLimitFailoverError(reason)
	}
	return nil
}

func accountRateLimitFailoverError(reason string) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable,
		Scope:      GatewayFailureScopeRequest, Reason: GatewayFailureReason(reason), RequestScopedTransient: true,
		ResponseBody: []byte(`{"error":{"type":"account_rate_limit","message":"Account multiplier exceeds this group's limit or is unavailable; retry on an eligible account"}}`),
	}
}
