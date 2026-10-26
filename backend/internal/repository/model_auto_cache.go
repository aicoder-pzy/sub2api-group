package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

func modelAutoReviewKey(groupID int64, model, effort string) string {
	return fmt.Sprintf("model_auto:review:%d:%s:%s", groupID, base64.RawURLEncoding.EncodeToString([]byte(model)), base64.RawURLEncoding.EncodeToString([]byte(effort)))
}

var reviewModelAutoScript = redis.NewScript(`
local primary = tonumber(redis.call('GET',KEYS[2]) or '0')
local revision = tonumber(redis.call('GET',KEYS[3]) or '0')
if primary ~= tonumber(ARGV[1]) or revision ~= tonumber(ARGV[2]) then return '' end
local now = tonumber(ARGV[5])
local raw = redis.call('GET',KEYS[1])
local state = raw and cjson.decode(raw) or nil
if not state or state.primary_id ~= primary or state.revision ~= revision then
  local changed = tonumber(ARGV[4]); if changed <= 0 then changed=now end
  state={primary_id=primary,revision=revision,candidate_id=0,evaluations=0,last_evaluated=0,next_evaluation=0,hold_until=changed+1800000,ready=false,reason=''}
end
local candidate = tonumber(ARGV[3])
-- Loss of the measured advantage invalidates readiness even between ticks.
if candidate == 0 or candidate ~= state.candidate_id then
  state.candidate_id=candidate; state.evaluations=0; state.ready=false
  state.last_evaluated=0; state.next_evaluation=0
end
if candidate > 0 and now >= state.next_evaluation then
  if state.last_evaluated > 0 and now-state.last_evaluated > 900000 then state.evaluations=0 end
  state.evaluations=math.min(3,state.evaluations+1)
  state.last_evaluated=now; state.next_evaluation=now+300000
end
state.reason=ARGV[6]
state.ready=candidate>0 and state.evaluations>=3 and now>=state.hold_until
local result=cjson.encode(state)
redis.call('SET',KEYS[1],result,'EX',604800)
return result
`)

func (c *gatewayCache) ReviewModelAutoCandidate(ctx context.Context, groupID int64, model, effort string, input service.ModelAutoReviewInput) (*service.ModelAutoReview, error) {
	key := buildSessionKey(groupID, service.GroupModelSchedulingKeyPrefix+base64.RawURLEncoding.EncodeToString([]byte(model)))
	var changed int64
	if !input.ChangedAt.IsZero() {
		changed = input.ChangedAt.UnixMilli()
	}
	raw, err := reviewModelAutoScript.Run(ctx, c.rdb, []string{modelAutoReviewKey(groupID, model, effort), key, key + ":revision"}, input.PrimaryID, input.Revision, input.CandidateID, changed, input.Now.UnixMilli(), input.Reason).Text()
	if err != nil || raw == "" {
		return nil, err
	}
	var result service.ModelAutoReview
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *gatewayCache) GetModelAutoReview(ctx context.Context, groupID int64, model, effort string) (*service.ModelAutoReview, error) {
	raw, err := c.rdb.Get(ctx, modelAutoReviewKey(groupID, model, effort)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result service.ModelAutoReview
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

var assignModelAutoSessionScript = redis.NewScript(`
local valid=cjson.decode(ARGV[2])
local previous=tonumber(redis.call('GET',KEYS[1]) or '0')
for _,id in ipairs(valid) do
  if id == previous then redis.call('EXPIRE',KEYS[1],3600); return {previous,0} end
end
local selected=tonumber(ARGV[1])
local backups=cjson.decode(ARGV[3])
local sequence=redis.call('INCR',KEYS[2])
redis.call('EXPIRE',KEYS[2],604800)
if #backups>0 then
  local cooldown=tonumber(ARGV[4])
  if cooldown>0 then
    local last=tonumber(redis.call('GET',KEYS[3]) or '0')
    -- Start on the first eligible new session, then require ten new sessions
    -- AND a shared cooldown. Existing bindings never advance either limit.
    if (last==0 or sequence-last>=10) and redis.call('SET',KEYS[5],1,'NX','EX',cooldown) then
      local trial=redis.call('INCR',KEYS[4])
      redis.call('EXPIRE',KEYS[4],604800)
      redis.call('SET',KEYS[3],sequence,'EX',604800)
      selected=backups[(trial-1)%#backups+1]
    end
  elseif sequence%10==0 then
    selected=backups[(math.floor(sequence/10)-1)%#backups+1]
  end
end
redis.call('SET',KEYS[1],selected,'EX',3600)
return {selected,1}
`)

func (c *gatewayCache) AssignModelAutoSession(ctx context.Context, groupID int64, model, session string, primary int64, valid, backups []int64, trialCooldown time.Duration) (int64, bool, error) {
	if valid == nil {
		valid = []int64{}
	}
	if backups == nil {
		backups = []int64{}
	}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		return 0, false, err
	}
	backupJSON, err := json.Marshal(backups)
	if err != nil {
		return 0, false, err
	}
	prefix := fmt.Sprintf("model_auto:session:%d:%s:", groupID, base64.RawURLEncoding.EncodeToString([]byte(model)))
	digest := sha256.Sum256([]byte(session))
	values, err := assignModelAutoSessionScript.Run(ctx, c.rdb, []string{prefix + hex.EncodeToString(digest[:]), prefix + "counter", prefix + "trial-sequence", prefix + "trial-count", prefix + "trial-cooldown"}, primary, validJSON, backupJSON, int64(trialCooldown/time.Second)).Int64Slice()
	if err != nil {
		return 0, false, err
	}
	if len(values) != 2 {
		return 0, false, errors.New("invalid model auto session assignment")
	}
	return values[0], values[1] == 1, nil
}
