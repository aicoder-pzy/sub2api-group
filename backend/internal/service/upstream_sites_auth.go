package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type upstreamSiteTokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Accept the copied login response as well as legacy access-token-only input.
func parseUpstreamSiteAuthorization(site *UpstreamSite) error {
	site.AccessToken = strings.TrimSpace(site.AccessToken)
	site.RefreshToken = strings.TrimSpace(site.RefreshToken)
	if len(site.AccessToken) > 40*1024 {
		return upstreamSiteError("Management authorization is too large")
	}
	if strings.HasPrefix(site.AccessToken, "{") {
		var pair upstreamSiteTokenPair
		data := []byte(site.AccessToken)
		if wrapped := gjson.GetBytes(data, "data"); wrapped.IsObject() {
			data = []byte(wrapped.Raw)
		}
		if json.Unmarshal(data, &pair) != nil || strings.TrimSpace(pair.AccessToken) == "" || strings.TrimSpace(pair.RefreshToken) == "" {
			return upstreamSiteError("Paste an access token or a JSON authorization containing access_token and refresh_token")
		}
		site.AccessToken, site.RefreshToken = strings.TrimSpace(pair.AccessToken), strings.TrimSpace(pair.RefreshToken)
	}
	for _, token := range []string{site.AccessToken, site.RefreshToken} {
		if len(token) > 16384 || strings.ContainsAny(token, "\r\n") {
			return upstreamSiteError("Invalid management authorization")
		}
	}
	if site.RefreshToken != "" && site.Kind != "sub2api" {
		return upstreamSiteError("Automatic token refresh is supported for Sub2API sites only")
	}
	return nil
}

func upstreamSiteReauthorizationRequired() error {
	return upstreamSiteError("Management authorization expired or was revoked. Reauthorize with access_token and refresh_token from a dedicated upstream login session")
}

// Database row locking serializes single-use refresh tokens across instances.
func (s *UpstreamBillingProbeService) refreshUpstreamSiteAuthorization(ctx context.Context, site *UpstreamSite, failedToken string) error {
	if site.AccessToken != "" {
		if site.RefreshToken == "" {
			return upstreamSiteReauthorizationRequired()
		}
		pair, err := s.rotateUpstreamSiteAuthorization(ctx, site, failedToken, site.RefreshToken)
		if err == nil {
			site.AccessToken, site.RefreshToken = pair.AccessToken, pair.RefreshToken
		}
		return err
	}
	if s.db == nil || s.newAPIEncryptor == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	// Once a single-use refresh starts, a closing browser must not cancel the
	// database write and discard the replacement credentials.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var accessCipher, refreshCipher string
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT token_cipher,refresh_token_cipher,revision FROM upstream_sites WHERE id=$1 FOR UPDATE", site.ID).Scan(&accessCipher, &refreshCipher, &revision)
	if errors.Is(err, sql.ErrNoRows) || err == nil && revision != site.Revision {
		return ErrUpstreamBillingProbeIdentityChanged
	}
	if err != nil {
		return err
	}
	current, err := s.newAPIEncryptor.Decrypt(accessCipher)
	if err != nil {
		return ErrNewAPIEncryptionKey
	}
	if current == failedToken {
		if refreshCipher == "" {
			return upstreamSiteReauthorizationRequired()
		}
		refreshToken, decryptErr := s.newAPIEncryptor.Decrypt(refreshCipher)
		if decryptErr != nil {
			return ErrNewAPIEncryptionKey
		}
		pair, refreshErr := s.rotateUpstreamSiteAuthorization(ctx, site, failedToken, refreshToken)
		if refreshErr != nil {
			return refreshErr
		}
		accessCipher, err = s.newAPIEncryptor.Encrypt(pair.AccessToken)
		if err != nil {
			return ErrNewAPIEncryptionKey
		}
		refreshCipher, err = s.newAPIEncryptor.Encrypt(pair.RefreshToken)
		if err != nil {
			return ErrNewAPIEncryptionKey
		}
		if _, err = tx.ExecContext(ctx, "UPDATE upstream_sites SET token_cipher=$2,refresh_token_cipher=$3,updated_at=NOW() WHERE id=$1", site.ID, accessCipher, refreshCipher); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	site.cipher, site.refreshCipher = accessCipher, refreshCipher
	site.CanRefresh = refreshCipher != ""
	return nil
}

func (s *UpstreamBillingProbeService) rotateUpstreamSiteAuthorization(ctx context.Context, site *UpstreamSite, oldAccess, refreshToken string) (*upstreamSiteTokenPair, error) {
	data, err := s.upstreamSiteRequestWithToken(ctx, site, http.MethodPost, "/api/v1/auth/refresh", map[string]string{"refresh_token": refreshToken}, "")
	if err != nil {
		var status *upstreamSiteHTTPError
		if errors.As(err, &status) && (status.code == 400 || status.code == 401) {
			return nil, upstreamSiteReauthorizationRequired()
		}
		return nil, upstreamSiteError("Automatic authorization refresh failed; retry the sync when the upstream is available")
	}
	var pair upstreamSiteTokenPair
	if json.Unmarshal(data, &pair) != nil || pair.AccessToken == "" || pair.RefreshToken == "" {
		return nil, upstreamSiteError("Upstream refresh did not return a complete token pair")
	}
	validation := UpstreamSite{Kind: "sub2api", AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}
	if err = parseUpstreamSiteAuthorization(&validation); err != nil {
		return nil, err
	}
	pair.AccessToken, pair.RefreshToken = validation.AccessToken, validation.RefreshToken
	// The trusted refresh endpoint issues Sub2API JWTs. Check their identity
	// without another network call that could lose a newly rotated refresh token.
	expectedID := site.UserID
	if expectedID <= 0 {
		expectedID = upstreamSiteTokenUserID(oldAccess)
	}
	if userID := upstreamSiteTokenUserID(pair.AccessToken); expectedID <= 0 || userID != expectedID {
		return nil, upstreamSiteError("Refreshed management token belongs to another user")
	}
	return &pair, nil
}

func upstreamSiteTokenUserID(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if claims, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			return gjson.GetBytes(claims, "user_id").Int()
		}
	}
	return 0
}
