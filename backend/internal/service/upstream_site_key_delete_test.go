package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDeleteUpstreamSiteKeyVerifiesOwnershipAndAbsence(t *testing.T) {
	for _, kind := range []string{"sub2api", "newapi"} {
		for _, ambiguous := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/normal", true: "/lost_response"}[ambiguous], func(t *testing.T) {
				deletes := 0
				path := "/api/v1/keys/7"
				if kind == "newapi" {
					path = "/api/token/7"
				}
				upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
					require.Equal(t, "Bearer nap_management", req.Header.Get("Authorization"))
					if req.Method == http.MethodDelete {
						require.Equal(t, path, req.URL.Path)
						deletes++
						if ambiguous {
							return nil, errors.New("transport lost with secret")
						}
						return siteImportResponse(`{"code":0,"success":true,"data":{}}`), nil
					}
					if req.URL.Path == path {
						require.Zero(t, deletes)
						return siteImportResponse(`{"code":0,"data":{"id":7,"key":"never-expose"}}`), nil
					}
					require.Equal(t, 1, deletes)
					return siteImportResponse(`{"code":0,"data":{"items":[],"total":0}}`), nil
				}}
				svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
				err := svc.DeleteUpstreamSiteKey(context.Background(), &UpstreamSite{Kind: kind, BaseURL: "https://example.com", AccessToken: "nap_management"}, "7")
				require.NoError(t, err)
				require.Equal(t, 1, deletes)
			})
		}
	}
}

func TestDeleteUpstreamSiteKeyRejectsUnownedInvalidOrUnconfirmed(t *testing.T) {
	for _, keyID := range []string{"../7", "07", "7"} {
		deletes := 0
		upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodDelete {
				deletes++
			}
			return siteImportResponse(`{"code":0,"data":{"id":8}}`), nil
		}}
		svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
		require.Error(t, svc.DeleteUpstreamSiteKey(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, keyID))
		require.Zero(t, deletes)
	}
	deletes := 0
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			deletes++
			return siteImportResponse(`{"code":0,"data":{}}`), nil
		}
		if strings.HasSuffix(req.URL.Path, "/7") {
			return siteImportResponse(`{"code":0,"data":{"id":7}}`), nil
		}
		return siteImportResponse(`{"code":0,"data":{"items":[{"id":7}],"total":1}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	err := svc.DeleteUpstreamSiteKey(context.Background(), &UpstreamSite{Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, "7")
	require.Error(t, err)
	require.Contains(t, err.Error(), "still exists")
	require.Equal(t, 1, deletes)
}

func TestDeleteUpstreamSiteKeyRejectsChangedConnectionBeforeDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	upstream := &siteImportHTTP{call: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		return siteImportResponse(`{"code":0,"data":{"id":7}}`), nil
	}}
	svc := newUpstreamBillingProbeTestService(nil, upstream, nil)
	svc.db = db
	mock.ExpectQuery("SELECT revision FROM upstream_sites").WithArgs("site").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(4))
	err = svc.DeleteUpstreamSiteKey(context.Background(), &UpstreamSite{ID: "site", Revision: 3, Kind: "sub2api", BaseURL: "https://example.com", AccessToken: "management"}, "7")
	require.ErrorIs(t, err, ErrUpstreamBillingProbeIdentityChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}
