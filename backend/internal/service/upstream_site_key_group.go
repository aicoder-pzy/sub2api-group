package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/tidwall/gjson"
)

type UpstreamSiteKeyGroups struct {
	Key    UpstreamSiteKey     `json:"key"`
	Groups []UpstreamSiteGroup `json:"groups"`
}

// The upstream user endpoint verifies ownership. Only the sanitized key is exposed.
func (s *UpstreamBillingProbeService) upstreamSiteKeyDetail(ctx context.Context, site *UpstreamSite, keyID string) ([]byte, error) {
	id, err := strconv.ParseInt(keyID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != keyID {
		return nil, upstreamSiteError("Invalid upstream key ID")
	}
	path := "/api/v1/keys/" + keyID
	if site.Kind == "newapi" {
		path = "/api/token/" + keyID
	}
	data, err := s.upstreamSiteRequest(ctx, site, http.MethodGet, path, nil)
	if err == nil && gjson.GetBytes(data, "id").String() != keyID {
		return nil, upstreamSiteError("Upstream returned a different key")
	}
	return data, err
}

func (s *UpstreamBillingProbeService) UpstreamSiteKeyGroupOptions(ctx context.Context, site *UpstreamSite, keyID string) (*UpstreamSiteKeyGroups, error) {
	data, err := s.upstreamSiteKeyDetail(ctx, site, keyID)
	if err != nil {
		return nil, err
	}
	groups, err := s.UpstreamSiteGroups(ctx, site)
	if err != nil {
		return nil, err
	}
	key := parseUpstreamSiteKey(gjson.ParseBytes(data), site.Kind, time.Now())
	return &UpstreamSiteKeyGroups{Key: key, Groups: groups}, nil
}

// Re-read the key before writing and verify the saved group afterwards. An
// ambiguous write is never replayed automatically, even if verification fails.
func (s *UpstreamBillingProbeService) ChangeUpstreamSiteKeyGroup(ctx context.Context, site *UpstreamSite, keyID, expectedGroup, groupID string) (*UpstreamSiteKey, error) {
	data, err := s.upstreamSiteKeyDetail(ctx, site, keyID)
	if err != nil {
		return nil, err
	}
	key := parseUpstreamSiteKey(gjson.ParseBytes(data), site.Kind, time.Now())
	if key.GroupID != expectedGroup {
		return nil, upstreamSiteError("The upstream key group changed. Reopen the group selector before saving")
	}
	groups, err := s.UpstreamSiteGroups(ctx, site)
	if err != nil {
		return nil, err
	}
	found := false
	for _, group := range groups {
		found = found || group.ID == groupID
	}
	if groupID == "" || !found {
		return nil, upstreamSiteError("Select an available upstream group")
	}
	if key.GroupID == groupID {
		return &key, nil
	}
	path := "/api/v1/keys/" + keyID
	groupNumber, parseErr := strconv.ParseInt(groupID, 10, 64)
	var body any
	if site.Kind == "sub2api" {
		if parseErr != nil || groupNumber <= 0 {
			return nil, upstreamSiteError("Invalid upstream group ID")
		}
		body = map[string]any{"group_id": groupNumber}
	} else {
		// Newer sites support group_only. Older versions ignore that flag and
		// expect the complete edit form, so preserve every editable setting.
		var values map[string]json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return nil, upstreamSiteError("Cannot read upstream key settings")
		}
		for _, field := range []string{"name", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits", "allow_ips"} {
			if _, ok := values[field]; !ok {
				return nil, upstreamSiteError("Upstream did not return complete key settings; change the group on the upstream site")
			}
		}
		id, _ := strconv.ParseInt(key.ID, 10, 64)
		update := map[string]any{"id": id, "group": groupID, "cross_group_retry": groupID == "auto", "auto_groups": nil}
		for _, field := range []string{"name", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits", "allow_ips", "model_mapping", "prompt_policy"} {
			if value, ok := values[field]; ok {
				update[field] = value
			}
		}
		body, path = update, "/api/token/?group_only=true"
	}
	if s.db != nil {
		var revision int64
		if err = s.db.QueryRowContext(ctx, "SELECT revision FROM upstream_sites WHERE id=$1", site.ID).Scan(&revision); err != nil {
			return nil, err
		}
		if revision != site.Revision {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
	}
	_, writeErr := s.upstreamSiteRequest(ctx, site, http.MethodPut, path, body)
	// Finish read-back and cache invalidation even when the browser closes.
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer cancel()
	saved, readErr := s.upstreamSiteKeyDetail(checkCtx, site, keyID)
	savedKey := parseUpstreamSiteKey(gjson.ParseBytes(saved), site.Kind, time.Now())
	if readErr != nil || savedKey.GroupID != groupID {
		if writeErr != nil {
			return nil, writeErr
		}
		return nil, upstreamSiteError("Group change was not confirmed. Refresh the upstream key before retrying; the upstream may have saved it")
	}
	// A transport failure followed by a matching read-back is a confirmed save.
	key = savedKey
	for _, group := range groups {
		if group.ID == key.GroupID {
			key.GroupName = group.Name
			break
		}
	}
	if s.db != nil {
		// Empty catalog with a new timestamp blocks an older in-flight model read.
		catalog, marshalErr := json.Marshal(UpstreamSiteCatalog{Models: []string{}, AttemptAt: time.Now().UTC()})
		if marshalErr != nil {
			return nil, marshalErr
		}
		result, err := s.db.ExecContext(checkCtx, `UPDATE upstream_sites SET inventory='{}',inventory_at=NOW(),model_catalogs=jsonb_set(model_catalogs,ARRAY[$3::text],$4::jsonb)
			WHERE id=$1 AND revision=$2`, site.ID, site.Revision, keyID, string(catalog))
		if err != nil {
			return nil, upstreamSiteError("Upstream group saved, but local refresh failed. Sync the site")
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return nil, upstreamSiteError("Upstream group saved, but the site connection changed. Sync the site")
		}
	}
	return &key, nil
}
