package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSiteGroupOptionsIncludeDescriptionsAndNoSecrets(t *testing.T) {
	for _, kind := range []string{"sub2api", "newapi"} {
		t.Run(kind, func(t *testing.T) {
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				if strings.Contains(req.URL.Path, "groups") {
					if kind == "newapi" {
						return siteImportResponse(`{"success":true,"data":{"vip":{"ratio":0,"desc":"Detailed description\nSecond line"},"auto":{"ratio":"auto","desc":"Automatic"}}}`), nil
					}
					return siteImportResponse(`{"code":0,"data":[{"id":4,"name":"VIP","rate_multiplier":0,"description":"Detailed description\nSecond line"}]}`), nil
				}
				return siteImportResponse(`{"code":0,"data":{"id":7,"key":"hidden-relay-secret","status":"active","group_id":4,"group":"vip"}}`), nil
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			options, err := svc.UpstreamSiteKeyGroupOptions(context.Background(), &UpstreamSite{Kind: kind, BaseURL: "https://example.com", AccessToken: "nap_management"}, "7")
			require.NoError(t, err)
			var found bool
			for _, group := range options.Groups {
				if group.ID == "4" || group.ID == "vip" {
					found = true
					require.Equal(t, "Detailed description\nSecond line", group.Description)
					require.NotNil(t, group.Multiplier)
					require.Zero(t, *group.Multiplier)
				}
			}
			require.True(t, found)
			encoded, err := json.Marshal(options)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "hidden-relay-secret")
			require.NotContains(t, string(encoded), "nap_management")
		})
	}
}

func TestChangeUpstreamSiteKeyGroupWritesOnlyGroupAndVerifiesAmbiguousResult(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost write response"}[ambiguous], func(t *testing.T) {
			puts := 0
			upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "Bearer management", req.Header.Get("Authorization"))
				switch req.Method + " " + req.URL.Path {
				case "GET /api/v1/keys/7":
					group := "4"
					if puts > 0 {
						group = "5"
					}
					return siteImportResponse(`{"code":0,"data":{"id":7,"key":"relay-secret","name":"Existing","status":"disabled","group_id":` + group + `}}`), nil
				case "GET /api/v1/groups/available":
					return siteImportResponse(`{"code":0,"data":[{"id":5,"name":"New group","description":"Full details","rate_multiplier":0.2}]}`), nil
				case "PUT /api/v1/keys/7":
					puts++
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, `{"group_id":5}`, string(body))
					if ambiguous {
						return nil, errors.New("connection lost after save: relay-secret")
					}
					return siteImportResponse(`{"code":0,"data":{}}`), nil
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			}}
			svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
			key, err := svc.ChangeUpstreamSiteKeyGroup(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, "7", "4", "5")
			require.NoError(t, err)
			require.Equal(t, 1, puts)
			require.Equal(t, "5", key.GroupID)
			require.Equal(t, "New group", key.GroupName)
			require.False(t, key.Available, "switching must not enable a disabled key")
		})
	}
}

func TestChangeUpstreamSiteKeyGroupRejectsStaleUnavailableAndInvalidIDs(t *testing.T) {
	for _, tc := range []struct{ keyID, expected, target string }{{"7", "wrong", "5"}, {"7", "4", "missing"}, {"../7", "4", "5"}} {
		puts := 0
		upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				puts++
			}
			if strings.Contains(req.URL.Path, "groups") {
				return siteImportResponse(`{"code":0,"data":[{"id":5,"name":"Available"}]}`), nil
			}
			return siteImportResponse(`{"code":0,"data":{"id":7,"group_id":4}}`), nil
		}}
		svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
		_, err := svc.ChangeUpstreamSiteKeyGroup(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, tc.keyID, tc.expected, tc.target)
		require.Error(t, err)
		require.Zero(t, puts)
	}
}

func TestChangeUpstreamSiteKeyGroupNeverClaimsUnverifiedWriteOrRetriesIt(t *testing.T) {
	puts := 0
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut {
			puts++
			return nil, errors.New("transport lost management-secret")
		}
		if strings.Contains(req.URL.Path, "groups") {
			return siteImportResponse(`{"code":0,"data":[{"id":5,"name":"Available"}]}`), nil
		}
		return siteImportResponse(`{"code":0,"data":{"id":7,"group_id":4}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	key, err := svc.ChangeUpstreamSiteKeyGroup(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, "7", "4", "5")
	require.Error(t, err)
	require.Nil(t, key)
	require.NotContains(t, err.Error(), "management-secret")
	require.Equal(t, 1, puts)
}

func TestChangeNewAPIKeyGroupPreservesCompleteSettings(t *testing.T) {
	puts := 0
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "GET /api/token/7":
			group := "old"
			if puts > 0 {
				group = "vip"
			}
			return siteImportResponse(`{"success":true,"data":{"id":7,"key":"masked-secret","name":"Keep name","status":2,"group":"` + group + `","expired_time":1794999999,"remain_quota":123456,"unlimited_quota":false,"model_limits_enabled":true,"model_limits":"allowed-model","allow_ips":"192.0.2.4","model_mapping":{"alias":"model"},"prompt_policy":{"model":"keep"}}}`), nil
		case "GET /api/user/self/groups":
			return siteImportResponse(`{"success":true,"data":{"vip":{"ratio":0.5,"desc":"Details"}}}`), nil
		case "PUT /api/token/":
			puts++
			require.Equal(t, "true", req.URL.Query().Get("group_only"))
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"id":7,"group":"vip","name":"Keep name","expired_time":1794999999,"remain_quota":123456,"unlimited_quota":false,"model_limits_enabled":true,"model_limits":"allowed-model","allow_ips":"192.0.2.4","model_mapping":{"alias":"model"},"prompt_policy":{"model":"keep"},"cross_group_retry":false,"auto_groups":null}`, string(body))
			return siteImportResponse(`{"success":true,"data":{}}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	key, err := svc.ChangeUpstreamSiteKeyGroup(context.Background(), &UpstreamSite{Kind: "newapi", BaseURL: "https://example.com", AccessToken: "nap_management"}, "7", "old", "vip")
	require.NoError(t, err)
	require.Equal(t, "vip", key.GroupID)
	require.False(t, key.Available)
	require.Equal(t, 1, puts)
}

func TestChangeUpstreamSiteKeyGroupInvalidatesCatalogWithTimestamp(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	puts := 0
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut {
			puts++
			return siteImportResponse(`{"code":0,"data":{}}`), nil
		}
		if strings.Contains(req.URL.Path, "groups") {
			return siteImportResponse(`{"code":0,"data":[{"id":5,"name":"Available"}]}`), nil
		}
		group := "4"
		if puts > 0 {
			group = "5"
		}
		return siteImportResponse(`{"code":0,"data":{"id":7,"group_id":` + group + `}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db = db
	mock.ExpectQuery("SELECT revision FROM upstream_sites").WithArgs("site").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(3))
	mock.ExpectExec("UPDATE upstream_sites SET inventory").WithArgs("site", int64(3), "7", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	_, err = svc.ChangeUpstreamSiteKeyGroup(context.Background(), &UpstreamSite{ID: "site", Revision: 3, Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, "7", "4", "5")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
