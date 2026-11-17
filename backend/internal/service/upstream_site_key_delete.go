package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Verify ownership with the user endpoint and confirm absence after deletion.
// Never replay an ambiguous delete. Local account records remain for the admin.
func (s *UpstreamBillingProbeService) DeleteUpstreamSiteKey(ctx context.Context, site *UpstreamSite, keyID string) error {
	if _, err := s.upstreamSiteKeyDetail(ctx, site, keyID); err != nil {
		return err
	}
	if s.db != nil {
		var revision int64
		if err := s.db.QueryRowContext(ctx, "SELECT revision FROM upstream_sites WHERE id=$1", site.ID).Scan(&revision); err != nil {
			return err
		}
		if revision != site.Revision {
			return ErrUpstreamBillingProbeIdentityChanged
		}
	}
	path := "/api/v1/keys/" + keyID
	if site.Kind == "newapi" {
		path = "/api/token/" + keyID
	}
	_, writeErr := s.upstreamSiteRequest(ctx, site, http.MethodDelete, path, nil)
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	keys, readErr := s.UpstreamSiteKeys(checkCtx, site)
	if readErr != nil {
		return upstreamSiteError("Deletion could not be confirmed. Sync the upstream key list before retrying")
	}
	for _, key := range keys {
		if key.ID == keyID {
			if writeErr != nil {
				return writeErr
			}
			return upstreamSiteError("Upstream key still exists. Sync the key list before retrying deletion")
		}
	}
	if s.db != nil {
		// Timestamp both caches so older in-flight reads cannot restore the key.
		catalog, err := json.Marshal(UpstreamSiteCatalog{Models: []string{}, AttemptAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		result, err := s.db.ExecContext(checkCtx, `UPDATE upstream_sites SET inventory='{}',inventory_at=NOW(),model_catalogs=jsonb_set(model_catalogs,ARRAY[$3::text],$4::jsonb)
			WHERE id=$1 AND revision=$2`, site.ID, site.Revision, keyID, string(catalog))
		if err != nil {
			return upstreamSiteError("Upstream key deleted, but local refresh failed. Sync the site")
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return upstreamSiteError("Upstream key deleted, but the site connection changed. Sync the site")
		}
	}
	return nil
}
