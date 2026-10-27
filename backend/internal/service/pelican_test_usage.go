package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Collect only group-test requests. Each sample owns its collector, and each
// upstream stream owns a snapshot so cumulative usage is never added twice.
type pelicanTestUsageKey struct{}

type pelicanTestUsageCollector struct {
	model     string
	requests  []*pelicanTestUsage
	group     *Group
	channels  *ChannelService
	effort    string
	pricingAt time.Time
	details   []PelicanTestCostDetail
}

// Frozen estimates for every attempted upstream stream, including failures.
// No credentials or request/response content are retained here.
type PelicanTestCostDetail struct {
	AccountID       int64           `json:"account_id"`
	Model           string          `json:"model"`
	Protocol        string          `json:"protocol"`
	ServiceTier     string          `json:"service_tier"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Tokens          UsageTokens     `json:"tokens"`
	RateMultiplier  *float64        `json:"rate_multiplier"`
	RateSource      string          `json:"rate_source"`
	PricingAt       time.Time       `json:"pricing_at"`
	PricingVersion  string          `json:"pricing_version,omitempty"`
	PricingSnapshot json.RawMessage `json:"pricing_snapshot,omitempty"`
	BaseCost        *CostBreakdown  `json:"base_cost,omitempty"`
	AccountBaseCost *float64        `json:"account_base_cost"`
	CostUSD         *float64        `json:"cost_usd"`
	Incomplete      bool            `json:"incomplete"`
}

type pelicanTestUsage struct {
	protocol    string
	model       string
	serviceTier string
	tokens      UsageTokens
	input       int
	output      int
	thoughts    int
	seen        bool
	inputSeen   bool
	outputSeen  bool
	complete    bool
}

func startPelicanTestStream(c *gin.Context, protocol string) *pelicanTestUsage {
	if c.Request == nil {
		return nil
	}
	return startPelicanTestUsage(c.Request.Context(), protocol)
}

func recordPelicanTestSSE(ctx context.Context, protocol, model string, body []byte) {
	u := startPelicanTestUsage(ctx, protocol)
	if u == nil {
		return
	}
	u.model = model
	for _, line := range strings.Split(string(body), "\n") {
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			u.read(strings.TrimSpace(data))
		}
	}
}

func pelicanUsageFromContext(ctx context.Context) *pelicanTestUsageCollector {
	u, _ := ctx.Value(pelicanTestUsageKey{}).(*pelicanTestUsageCollector)
	return u
}

func startPelicanTestUsage(ctx context.Context, protocol string) *pelicanTestUsage {
	collector := pelicanUsageFromContext(ctx)
	if collector == nil {
		return nil
	}
	u := &pelicanTestUsage{protocol: protocol, model: collector.model}
	collector.requests = append(collector.requests, u)
	return u
}

func (u *pelicanTestUsage) read(raw string) {
	if u == nil {
		return
	}
	data := gjson.Parse(raw)
	eventType := data.Get("type").String()
	if eventType == "message_stop" || eventType == "response.completed" || eventType == "response.done" || eventType == "response.failed" || eventType == "response.incomplete" {
		u.complete = true
	}
	if response := data.Get("response"); response.IsObject() {
		data = response
	} else if message := data.Get("message"); message.IsObject() {
		data = message
	}
	if model := strings.TrimSpace(data.Get("model").String()); model != "" {
		u.model = model
	}
	if tier := data.Get("service_tier").String(); tier != "" {
		u.serviceTier = tier
	}
	usage := data.Get("usage")
	if u.protocol == "gemini" {
		usage = data.Get("usageMetadata")
		for _, candidate := range data.Get("candidates").Array() {
			if candidate.Get("finishReason").String() != "" {
				u.complete = true
			}
		}
	}
	for _, choice := range data.Get("choices").Array() {
		if choice.Get("finish_reason").String() != "" {
			u.complete = true
		}
	}
	if !usage.IsObject() {
		return
	}
	set := func(field string, value *int) {
		if n := usage.Get(field); n.Type == gjson.Number && n.Float() >= 0 {
			*value = int(n.Int())
			u.seen = true
			if value == &u.input {
				u.inputSeen = true
			}
			if value == &u.output {
				u.outputSeen = true
			}
		}
	}
	switch u.protocol {
	case "anthropic":
		set("input_tokens", &u.input)
		set("output_tokens", &u.output)
		set("cache_read_input_tokens", &u.tokens.CacheReadTokens)
		set("cache_creation_input_tokens", &u.tokens.CacheCreationTokens)
		set("cache_creation.ephemeral_5m_input_tokens", &u.tokens.CacheCreation5mTokens)
		set("cache_creation.ephemeral_1h_input_tokens", &u.tokens.CacheCreation1hTokens)
	case "gemini":
		set("promptTokenCount", &u.input)
		set("candidatesTokenCount", &u.output)
		set("thoughtsTokenCount", &u.thoughts)
		set("cachedContentTokenCount", &u.tokens.CacheReadTokens)
	case "chat":
		set("prompt_tokens", &u.input)
		set("completion_tokens", &u.output)
		set("prompt_tokens_details.cached_tokens", &u.tokens.CacheReadTokens)
		set("prompt_cache_hit_tokens", &u.tokens.CacheReadTokens)
	default:
		set("input_tokens", &u.input)
		set("output_tokens", &u.output)
		set("input_tokens_details.cached_tokens", &u.tokens.CacheReadTokens)
		set("cache_read_input_tokens", &u.tokens.CacheReadTokens)
		set("cache_creation_input_tokens", &u.tokens.CacheCreationTokens)
	}
	u.tokens.InputTokens = u.input
	if u.protocol != "anthropic" {
		// OpenAI/Chat/Gemini input totals already include cache hits.
		u.tokens.InputTokens = max(0, u.input-u.tokens.CacheReadTokens-u.tokens.CacheCreationTokens)
	}
	u.tokens.OutputTokens = u.output + u.thoughts
}

// Return a cost snapshot, not a debit. Use the account's upstream cost multiplier,
// never the customer/group selling multiplier. Missing usage/pricing remains
// unknown; a partially metered attempt must not discard other known costs.
func (c *pelicanTestUsageCollector) cost(ctx context.Context, billing *BillingService, account *Account) (*float64, bool) {
	var total float64
	priced, incomplete := false, len(c.requests) == 0
	c.details = nil
	pricingAt := c.pricingAt
	if pricingAt.IsZero() {
		pricingAt = time.Now()
	}
	var groupID *int64
	if c.group != nil {
		groupID = &c.group.ID
	}
	for _, u := range c.requests {
		detail := PelicanTestCostDetail{Model: u.model, Protocol: u.protocol, Tokens: u.tokens,
			ServiceTier: u.serviceTier, ReasoningEffort: c.effort, PricingAt: pricingAt,
			RateSource: "account_rate_multiplier", Incomplete: true}
		if account != nil {
			detail.AccountID = account.ID
		}
		if account == nil || account.RateMultiplier == nil {
			detail.RateSource = "default_1"
		}
		rate := account.BillingRateMultiplier()
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			c.details = append(c.details, detail)
			incomplete = true
			continue
		}
		detail.RateMultiplier = &rate
		if !u.seen || billing == nil {
			c.details = append(c.details, detail)
			incomplete = true
			continue
		}
		resolver := NewModelPricingResolver(c.channels, billing)
		resolved := resolver.Resolve(ctx, PricingInput{Model: u.model, GroupID: groupID, Group: c.group})
		cost, err := billing.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: u.model, GroupID: groupID, Group: c.group, Tokens: u.tokens,
			RequestCount: 1, RateMultiplier: 1, ServiceTier: u.serviceTier, ReasoningEffort: c.effort,
			PricingAt: pricingAt, Resolver: resolver, Resolved: resolved,
			LongContextBillingEnabled: openAILongContextBillingGate(account),
		})
		if err != nil || cost == nil {
			c.details = append(c.details, detail)
			incomplete = true
			continue
		}
		baseCost := cost.TotalCost
		var channel *Channel
		if c.channels != nil && groupID != nil {
			var channelErr error
			channel, channelErr = c.channels.GetChannelForGroup(ctx, *groupID)
			if channelErr != nil {
				c.details = append(c.details, detail)
				incomplete = true
				continue
			}
		}
		longContext := accountStatsLongContextPricingEnabled(openAILongContextBillingGate(account))
		if groupID != nil {
			if stats := resolveAccountStatsCost(ctx, c.channels, billing, detail.AccountID, *groupID,
				u.model, u.tokens, 1, cost.TotalCost, u.serviceTier, pricingAt, longContext, c.effort); stats != nil {
				baseCost = *stats
			}
		}
		// Snapshot numeric pricing before any future catalog or operator edits.
		pricingSnapshot := struct {
			Resolved            *ResolvedPricing          `json:"resolved"`
			ChannelPricing      *ChannelModelPricing      `json:"channel_pricing,omitempty"`
			AccountCatalog      *ResolvedPricing          `json:"account_catalog"`
			AccountRules        []AccountStatsPricingRule `json:"account_rules,omitempty"`
			ApplyChannelPricing bool                      `json:"apply_channel_pricing"`
			LongContext         bool                      `json:"account_long_context"`
		}{Resolved: resolved, ChannelPricing: resolved.channelPricing, LongContext: longContext,
			AccountCatalog: NewModelPricingResolver(nil, billing).Resolve(ctx, PricingInput{Model: u.model})}
		if channel != nil {
			pricingSnapshot.AccountRules = channel.AccountStatsPricingRules
			pricingSnapshot.ApplyChannelPricing = channel.ApplyPricingToAccountStats
		}
		snapshot, snapshotErr := json.Marshal(pricingSnapshot)
		amount := baseCost * rate
		if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
			c.details = append(c.details, detail)
			incomplete = true
			continue
		}
		detail.BaseCost, detail.AccountBaseCost, detail.CostUSD = cost, &baseCost, &amount
		detail.Incomplete = !u.complete || !u.inputSeen || !u.outputSeen || snapshotErr != nil
		if snapshotErr == nil {
			detail.PricingSnapshot = snapshot
			detail.PricingVersion = fmt.Sprintf("sha256:%x", sha256.Sum256(snapshot))
		}
		c.details = append(c.details, detail)
		total += amount
		priced = true
		incomplete = incomplete || detail.Incomplete
	}
	if !priced {
		return nil, true
	}
	return &total, incomplete
}
