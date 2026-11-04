package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type siteImportHTTP struct {
	HTTPUpstream
	call func(*http.Request) (*http.Response, error)
}

func (s *siteImportHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.call(req)
}
func siteImportResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

type siteImportEncryptor struct{}

func (*siteImportEncryptor) Encrypt(s string) (string, error) { return "encrypted:" + s, nil }
func (*siteImportEncryptor) Decrypt(s string) (string, error) {
	return strings.TrimPrefix(s, "encrypted:"), nil
}

func TestUpstreamSiteSaveEncryptsAndDoesNotReturnSecrets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/tenant/api/user/self", req.URL.Path)
		require.Equal(t, "Bearer nap_management-secret", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("New-Api-User"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		return siteImportResponse(`{"success":true,"data":{"id":7}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db = db
	svc.newAPIFixedKey = true
	svc.newAPIEncryptor = &siteImportEncryptor{}
	mock.ExpectQuery("INSERT INTO upstream_sites").WithArgs(sqlmock.AnyArg(), "test", "newapi", "https://example.com/tenant", int64(7), "encrypted:nap_management-secret", nil).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(1))
	site, err := svc.SaveUpstreamSite(context.Background(), UpstreamSite{Name: "test", Kind: "newapi", BaseURL: "https://example.com/tenant/v1/", AccessToken: "nap_management-secret"})
	require.NoError(t, err)
	encoded, err := json.Marshal(site)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "management-secret")
	require.NotContains(t, string(encoded), "encrypted:")
	require.Equal(t, int64(7), site.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamSiteInventoryKeepsSecretsServerSideAndRevealsSelectedKey(t *testing.T) {
	paths := []string{}
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.Method+" "+req.URL.RequestURI())
		switch req.Method + " " + req.URL.RequestURI() {
		case "GET /api/token/?p=0&page_size=100":
			return siteImportResponse(`{"success":true,"data":{"items":[{"id":3,"name":"mine","key":"abcd********wxyz","status":1,"group":"vip","unlimited_quota":true}],"total":1}}`), nil
		case "POST /api/token/3/key":
			return siteImportResponse(`{"success":true,"data":"sk-relay-secret"}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	site := &UpstreamSite{Kind: "newapi", BaseURL: "https://example.com", AccessToken: "nap_management"}
	keys, err := svc.UpstreamSiteKeys(context.Background(), site)
	require.NoError(t, err)
	encoded, err := json.Marshal(keys)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "abcd")
	require.Len(t, paths, 1)
	key, err := svc.ResolveUpstreamSiteKey(context.Background(), site, "3")
	require.NoError(t, err)
	require.Equal(t, "sk-relay-secret", key.secret)
	require.Equal(t, "POST /api/token/3/key", paths[len(paths)-1])
}

func TestUpstreamSitePaginationCompatibilityAndCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pages     []string
		queries   []string
		wantError bool
	}{
		{"legacy zero-based", []string{`[{"id":1}]`, `[{"id":2}]`, `[]`}, []string{"0", "1", "2"}, false},
		{"modern one-based", []string{`{"items":[{"id":1}],"total":2,"p":1}`, `{"items":[{"id":2}],"total":2,"p":2}`}, []string{"0", "2"}, false},
		{"duplicate page", []string{`{"items":[{"id":1}],"total":2}`, `{"items":[{"id":1}],"total":2}`}, []string{"0", "1"}, true},
		{"missing rows", []string{`{"items":[{"id":1}],"total":2}`, `{"items":[],"total":2}`}, []string{"0", "1"}, true},
		{"changed total", []string{`{"items":[{"id":1}],"total":2}`, `{"items":[{"id":2}],"total":3}`}, []string{"0", "1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				require.Less(t, calls, len(tc.pages))
				require.Equal(t, tc.queries[calls], req.URL.Query().Get("p"))
				body := tc.pages[calls]
				calls++
				return siteImportResponse(`{"success":true,"data":` + body + `}`), nil
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			keys, err := svc.UpstreamSiteKeys(context.Background(), &UpstreamSite{Kind: "newapi", BaseURL: "https://example.com", UserID: 7, AccessToken: "management"})
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Len(t, keys, 2)
			}
			require.Equal(t, len(tc.pages), calls)
		})
	}
}

func TestUpstreamSiteCreationIsNeverRetriedAfterAmbiguousFailure(t *testing.T) {
	posts := 0
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return siteImportResponse(`{"success":true,"data":{"vip":0.5}}`), nil
		}
		posts++
		return nil, errors.New("transport lost after committing management-secret")
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	site := &UpstreamSite{Kind: "newapi", BaseURL: "https://example.com", AccessToken: "nap_management-secret"}
	err := svc.CreateUpstreamSiteKey(context.Background(), site, "test", "vip")
	require.Error(t, err)
	require.Equal(t, 1, posts)
	require.Contains(t, err.Error(), "Refresh")
	require.NotContains(t, err.Error(), "management-secret")
	err = svc.CreateUpstreamSiteKey(context.Background(), site, "test", "invalid-group")
	require.Error(t, err)
	require.Equal(t, 1, posts)
}

func TestUpstreamSiteRejectsUnavailableKeysAndUsesRelayKeyForModels(t *testing.T) {
	now := time.Now()
	for _, row := range []string{`{"id":1,"status":"disabled"}`, `{"id":1,"status":"active","expires_at":"2020-01-01T00:00:00Z"}`, `{"id":1,"status":"active","quota":10,"quota_used":10}`} {
		key := parseUpstreamSiteKey(gjson.Parse(row), "sub2api", now)
		require.False(t, key.Available)
	}
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/models", req.URL.Path)
		require.Equal(t, "Bearer relay-key", req.Header.Get("Authorization"))
		return siteImportResponse(`{"data":[{"id":"model-1"}]}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	models, err := svc.upstreamSiteModels(context.Background(), &UpstreamSite{Kind: "newapi", BaseURL: "https://example.com", AccessToken: "management"}, &UpstreamSiteKey{secret: "relay-key"})
	require.NoError(t, err)
	require.Equal(t, []string{"model-1"}, models)
}
