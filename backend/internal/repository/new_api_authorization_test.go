package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNewAPIAuthorizationRollsBackChangedAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, revision").WithArgs("https://example.com", int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "revision"}))
	mock.ExpectQuery("SELECT platform, type, credentials, proxy_id").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "credentials", "proxy_id"}).AddRow("openai", "apikey", `{"api_key":"changed","base_url":"https://example.com/v1"}`, nil))
	mock.ExpectRollback()
	account := &service.Account{ID: 1, Platform: "openai", Type: "apikey", Credentials: map[string]any{"api_key": "original", "base_url": "https://example.com/v1"}}
	err = NewNewAPIAuthorizationRepository(db).Save(context.Background(), &service.NewAPISiteAuthorization{SiteURL: "https://example.com", UserID: 7, Ciphertext: "encrypted"}, []service.NewAPIBindingSave{{Account: account, TokenID: 3}})
	require.ErrorIs(t, err, service.ErrUpstreamBillingProbeIdentityChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewAPIWalletRejectsRotatedAuthorization(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT revision FROM new_api_site_authorizations").WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(2))
	mock.ExpectRollback()
	err = NewNewAPIAuthorizationRepository(db).WriteSnapshot(context.Background(), &service.Account{ID: 1}, &service.NewAPIAccountBinding{Profile: service.NewAPISiteAuthorization{ID: 8, Revision: 1}}, &service.UpstreamBalanceState{})
	require.ErrorIs(t, err, service.ErrUpstreamBillingProbeIdentityChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}
