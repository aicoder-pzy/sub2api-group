package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func siteAuthJWT(userID int64, marker string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"user_id":%d}`, userID))) + "." + marker
}

func TestUpstreamSiteSavesCompleteAuthorizationWithoutReturningSecrets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldAccess, newAccess := siteAuthJWT(7, "copied"), siteAuthJWT(7, "enrolled")
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v1/auth/refresh" {
			return siteImportResponse(fmt.Sprintf(`{"code":0,"data":{"access_token":%q,"refresh_token":"enrolled-refresh-secret"}}`, newAccess)), nil
		}
		require.Equal(t, "Bearer "+newAccess, req.Header.Get("Authorization"))
		return siteImportResponse(`{"code":0,"data":{"id":7}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db, svc.newAPIFixedKey, svc.newAPIEncryptor = db, true, &siteImportEncryptor{}
	mock.ExpectQuery("INSERT INTO upstream_sites").WithArgs(sqlmock.AnyArg(), "test", "sub2api", "https://example.com", int64(7), "encrypted:"+newAccess, nil, "https://example.com", "", "encrypted:enrolled-refresh-secret").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(1))
	site, err := svc.SaveUpstreamSite(context.Background(), UpstreamSite{Name: "test", Kind: "sub2api", BaseURL: "https://example.com", AccessToken: fmt.Sprintf(`{"data":{"access_token":%q,"refresh_token":"refresh-secret"}}`, oldAccess)})
	require.NoError(t, err)
	require.True(t, site.CanRefresh)
	encoded, err := json.Marshal(site)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret")
	require.NotContains(t, string(encoded), "encrypted:")
	require.NotContains(t, string(encoded), newAccess)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteAuthorizationRefreshSerializesAndPersistsRotation(t *testing.T) {
	for _, scenario := range []string{"rotate", "already rotated", "refresh rejected", "wrong user", "no refresh token"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			oldAccess, newAccess := siteAuthJWT(7, "old"), siteAuthJWT(7, "new")
			if scenario == "wrong user" {
				newAccess = siteAuthJWT(8, "other")
			}
			refreshes, reads := 0, 0
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/api/v1/auth/refresh" {
					refreshes++
					require.Equal(t, http.MethodPost, req.Method)
					require.Empty(t, req.Header.Get("Authorization"))
					body, readErr := io.ReadAll(req.Body)
					require.NoError(t, readErr)
					require.JSONEq(t, `{"refresh_token":"refresh-secret"}`, string(body))
					if scenario == "refresh rejected" {
						response := siteImportResponse(`{"code":"REFRESH_TOKEN_INVALID","message":"refresh-secret"}`)
						response.StatusCode = 401
						return response, nil
					}
					return siteImportResponse(fmt.Sprintf(`{"code":0,"data":{"access_token":%q,"refresh_token":"rotated-secret"}}`, newAccess)), nil
				}
				reads++
				if req.Header.Get("Authorization") == "Bearer "+oldAccess {
					response := siteImportResponse(`{"code":"TOKEN_EXPIRED"}`)
					response.StatusCode = 401
					return response, nil
				}
				require.Equal(t, "Bearer "+newAccess, req.Header.Get("Authorization"))
				return siteImportResponse(`{"code":0,"data":{"id":7}}`), nil
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			svc.db, svc.newAPIEncryptor = db, &siteImportEncryptor{}
			site := &UpstreamSite{ID: "site", Kind: "sub2api", BaseURL: "https://example.com", Revision: 3, cipher: "encrypted:" + oldAccess}
			current, refresh := oldAccess, "encrypted:refresh-secret"
			if scenario == "already rotated" {
				current = newAccess
			}
			if scenario == "no refresh token" {
				refresh = ""
			}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT token_cipher,refresh_token_cipher,revision FROM upstream_sites WHERE id=\\$1 FOR UPDATE").WithArgs("site").WillReturnRows(sqlmock.NewRows([]string{"token_cipher", "refresh_token_cipher", "revision"}).AddRow("encrypted:"+current, refresh, 3))
			success := scenario == "rotate" || scenario == "already rotated"
			if scenario == "rotate" {
				mock.ExpectExec("UPDATE upstream_sites SET token_cipher").WithArgs("site", "encrypted:"+newAccess, "encrypted:rotated-secret").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if success {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			_, err = svc.upstreamSiteRequest(context.Background(), site, http.MethodGet, "/api/v1/auth/me", nil)
			if success {
				require.NoError(t, err)
				require.Equal(t, "encrypted:"+newAccess, site.cipher)
				require.Equal(t, 2, reads)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
				require.Equal(t, "encrypted:"+oldAccess, site.cipher)
				require.Equal(t, 1, reads)
			}
			if scenario == "already rotated" || scenario == "no refresh token" {
				require.Zero(t, refreshes)
			} else {
				require.Equal(t, 1, refreshes)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpstreamSiteRefreshDoesNotRetryPermissionFailuresOrInferenceAuthorization(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/me", "/v1/models"} {
		calls := 0
		upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
			calls++
			response := siteImportResponse(`{"message":"secret"}`)
			response.StatusCode = 403
			if strings.HasPrefix(req.URL.Path, "/v1/") {
				response.StatusCode = 401
			}
			return response, nil
		}}
		svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
		_, err := svc.upstreamSiteRequest(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "access-secret", RefreshToken: "refresh-secret"}, http.MethodGet, path, nil)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		require.Equal(t, 1, calls)
	}
}

func TestUpstreamSiteAuthorizationRejectsIncompleteOrInvalidPairs(t *testing.T) {
	for _, input := range []string{`{"access_token":"access"}`, `{"access_token":"access","refresh_token":null}`, `{"access_token":"access","refresh_token":"\nsecret\ninside"}`, `{"data":{}}`} {
		site := UpstreamSite{Kind: "sub2api", AccessToken: input}
		require.Error(t, parseUpstreamSiteAuthorization(&site))
	}
	legacy := UpstreamSite{Kind: "sub2api", AccessToken: " legacy-access "}
	require.NoError(t, parseUpstreamSiteAuthorization(&legacy))
	require.Equal(t, "legacy-access", legacy.AccessToken)
}

func TestUpstreamSiteExpiredCompleteAuthorizationCanBeSaved(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldAccess, newAccess := siteAuthJWT(7, "expired"), siteAuthJWT(7, "new")
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/api/v1/auth/refresh" {
			return siteImportResponse(fmt.Sprintf(`{"code":0,"data":{"access_token":%q,"refresh_token":"new-refresh"}}`, newAccess)), nil
		}
		if req.Header.Get("Authorization") == "Bearer "+oldAccess {
			response := siteImportResponse(`{"code":"TOKEN_EXPIRED"}`)
			response.StatusCode = 401
			return response, nil
		}
		require.Equal(t, "Bearer "+newAccess, req.Header.Get("Authorization"))
		return siteImportResponse(`{"code":0,"data":{"id":7}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db, svc.newAPIFixedKey, svc.newAPIEncryptor = db, true, &siteImportEncryptor{}
	mock.ExpectQuery("INSERT INTO upstream_sites").WithArgs(sqlmock.AnyArg(), "test", "sub2api", "https://example.com", int64(7), "encrypted:"+newAccess, nil, "https://example.com", "", "encrypted:new-refresh").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(1))
	site, err := svc.SaveUpstreamSite(context.Background(), UpstreamSite{Name: "test", Kind: "sub2api", BaseURL: "https://example.com", AccessToken: oldAccess, RefreshToken: "refresh-secret"})
	require.NoError(t, err)
	require.True(t, site.CanRefresh)
	require.Empty(t, site.AccessToken)
	require.Empty(t, site.RefreshToken)
	require.NoError(t, mock.ExpectationsWereMet())
}
