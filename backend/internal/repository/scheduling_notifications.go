package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const schedulingNotificationPending = "scheduling_notifications:pending"
const schedulingNotificationHistory = "scheduling_notifications:history"
const schedulingNotificationPrefix = "scheduling_notifications:event:"
const schedulingNotificationTTL = 30 * 24 * time.Hour

// Appended inside the binding Lua transaction: failed CAS and unchanged/initial
// bindings never produce notifications. Payloads contain no request bodies or keys.
const queueSchedulingNotificationLua = `
if transition.previous_account_id > 0 and transition.previous_account_id ~= transition.account_id then
  local event = cjson.decode(ARGV[#ARGV])
  event.previous_account_id = transition.previous_account_id
  redis.call('SET', KEYS[4], cjson.encode(event), 'EX', 2592000)
  redis.call('ZADD', KEYS[5], ARGV[#ARGV-1], event.id)
  redis.call('ZADD', KEYS[6], ARGV[#ARGV-1], event.id)
  redis.call('ZREMRANGEBYSCORE', KEYS[6], '-inf', tonumber(ARGV[#ARGV-1])-2592000000)
end
return 1
`

func schedulingNotificationArgs(ctx context.Context, groupID int64, sessionHash string, transition service.GroupModelSchedulingTransition) ([]string, []any, error) {
	model, _ := service.GroupModelSchedulingModelFromKey(sessionHash)
	event := service.NewSchedulingNotification(ctx, groupID, model, transition)
	data, err := json.Marshal(event)
	return []string{schedulingNotificationPrefix + event.ID, schedulingNotificationPending, schedulingNotificationHistory}, []any{transition.ChangedAt.UnixMilli(), string(data)}, err
}

func (c *gatewayCache) NextSchedulingNotification(ctx context.Context, now time.Time) (*service.SchedulingNotification, error) {
	ids, err := c.rdb.ZRangeByScore(ctx, schedulingNotificationPending, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10), Count: 1}).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	raw, err := c.rdb.Get(ctx, schedulingNotificationPrefix+ids[0]).Bytes()
	if err == redis.Nil {
		return nil, c.rdb.ZRem(ctx, schedulingNotificationPending, ids[0]).Err()
	}
	if err != nil {
		return nil, err
	}
	var event service.SchedulingNotification
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *gatewayCache) SaveSchedulingNotification(ctx context.Context, event *service.SchedulingNotification, next *time.Time) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = c.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, schedulingNotificationPrefix+event.ID, data, schedulingNotificationTTL)
		if next == nil {
			pipe.ZRem(ctx, schedulingNotificationPending, event.ID)
		} else {
			pipe.ZAdd(ctx, schedulingNotificationPending, redis.Z{Score: float64(next.UnixMilli()), Member: event.ID})
		}
		return nil
	})
	return err
}

func (c *gatewayCache) SchedulingNotificationHistory(ctx context.Context, limit int) ([]service.SchedulingNotification, error) {
	ids, err := c.rdb.ZRevRange(ctx, schedulingNotificationHistory, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	events := make([]service.SchedulingNotification, 0, len(ids))
	for _, id := range ids {
		raw, err := c.rdb.Get(ctx, schedulingNotificationPrefix+id).Bytes()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		var event service.SchedulingNotification
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}
