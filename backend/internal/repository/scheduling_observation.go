package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type schedulingObservation struct {
	samples map[int64]service.GroupModelAccountQuality
	at      time.Time
	running bool
}

type schedulingObservationCache struct {
	mu      sync.Mutex
	entries map[string]*schedulingObservation
	active  int
	next    time.Time
}

func observationKey(ctx context.Context, groupID int64, model, effort string) string {
	public, endpoint, _ := service.SchedulingObservationScope(ctx, model)
	return fmt.Sprintf("%d:%q:%q:%q:%q", groupID, model, public, endpoint, effort)
}

// Existing synchronous readers remain available for explicit admin diagnostics.
// Live smart scheduling reads cached natural traffic; no request starts a probe.
// Cold/low-sample scopes retry after 5s, established evidence after 30s. Entries
// older than 60s become unknown; failures cannot extend an evidence timestamp.
func (r *usageLogRepository) GetSmartChannelAccountQuality(ctx context.Context, groupID int64, model, effort string) (map[int64]service.GroupModelAccountQuality, error) {
	r.observationOnce.Do(func() { r.observation.entries = make(map[string]*schedulingObservation) })
	cache, now, key := &r.observation, time.Now(), observationKey(ctx, groupID, model, effort)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.entries[key]
	if entry == nil {
		if len(cache.entries) >= 32 {
			oldest := ""
			for k, candidate := range cache.entries {
				if !candidate.running && (oldest == "" || candidate.at.Before(cache.entries[oldest].at)) {
					oldest = k
				}
			}
			if oldest == "" {
				return nil, nil
			}
			delete(cache.entries, oldest)
		}
		entry = &schedulingObservation{}
		cache.entries[key] = entry
	}
	var result map[int64]service.GroupModelAccountQuality
	if now.Sub(entry.at) <= time.Minute {
		result = entry.samples
	}
	var count int64
	for _, sample := range result {
		count += sample.Successes
	}
	ttl := 5 * time.Second
	if count >= 100 {
		ttl = 30 * time.Second
	}
	if !entry.running && now.Sub(entry.at) >= ttl && cache.active < 2 && !now.Before(cache.next) {
		entry.running = true
		cache.active++
		cache.next = now.Add(500 * time.Millisecond)
		go func() {
			queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			samples, err := r.GetModelAutoAccountQuality(queryCtx, groupID, model, effort)
			cache.mu.Lock()
			defer cache.mu.Unlock()
			entry.running = false
			cache.active--
			if err == nil && len(samples) <= 2000 {
				entry.samples, entry.at = samples, time.Now()
			}
		}()
	}
	return result, nil
}

// Markers are inert SQL comments for legacy readers and direct query tests.
// Only smart-mode readers replace the business-model parameter and exact
// endpoint predicates; $2 remains the routing/test model elsewhere in the SQL.
func scopedSchedulingQuery(ctx context.Context, query string, args []any, model string) (string, []any) {
	public, endpoint, smart := service.SchedulingObservationScope(ctx, model)
	if !smart {
		return query, args
	}
	args = append(args, public, endpoint)
	modelParam, endpointParam := len(args)-1, len(args)
	query = strings.ReplaceAll(query, "/*business_model*/$2", fmt.Sprintf("$%d", modelParam))
	for _, alias := range []string{"u", "usage", "errors", "e", ""} {
		column := "inbound_endpoint"
		if alias != "" {
			column = alias + "." + column
		}
		predicate := fmt.Sprintf("AND ($%d='' OR %s=$%d)", endpointParam, column, endpointParam)
		query = strings.ReplaceAll(query, "/*ingress:"+alias+"*/", predicate)
	}
	return query, args
}
