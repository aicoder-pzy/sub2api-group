package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPublishedAccountPoolStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := &UpstreamBillingProbeService{db: db}
	config := func(base, id, source, name string, mapping map[string]string, plans ...string) []byte {
		t.Helper()
		cfg := storedAccountPoolStatusConfig{AccountPoolStatusConfig: AccountPoolStatusConfig{
			PoolID: id, SourceName: source, Name: name, PlanMap: mapping, Plans: plans,
		}, BaseURL: base}
		require.NoError(t, cfg.normalize())
		raw, err := json.Marshal(cfg)
		require.NoError(t, err)
		return raw
	}
	const shenji = `{"updated":"2026-11-21T00:00:00Z","site_revision":3,
		"groups":[{"source_id":"9","id":"public-1","original":"upstream-name"}],
		"rows":[{"pools":["public-1"],"plan":"promax","status":"active"},
		{"pools":["public-1"],"plan":"Team","status":"disabled"},
		{"pools":["public-1"],"plan":"Free","status":"active"},
		{"pools":["other"],"plan":"Team","status":"active"}]}`
	const qiu = `{"updated":"2026-11-21T00:05:00Z","site_revision":2,
		"groups":[{"source_id":"97","id":"public-2"}],
		"rows":[{"pools":["public-2"],"platform":"openai","plan":"Pro 500","status":"rate_limited"},
		{"pools":["public-2"],"plan":"Plus","status":"active"}]}`
	shenjiConfig := config(accountPoolBaseURL, "public-1", "Shenji", "Full pool", nil, "Pro Max", "Team")
	qiuConfig := config(vulcanPoolBaseURL, "public-2", "Qiu API", "Codex pool", map[string]string{" Pro 500 ": "Pro Max"}, "Pro Max")
	mock.ExpectQuery(`WHERE s.purpose='public' AND s.kind='sub2api'`).WithArgs(poolStatusSettingPrefix, accountPoolBaseURL, vulcanPoolBaseURL).
		WillReturnRows(sqlmock.NewRows([]string{"base_url", "revision", "value", "snapshot"}).
			AddRow(accountPoolBaseURL, 3, shenjiConfig, shenji).
			AddRow(vulcanPoolBaseURL, 2, qiuConfig, qiu).
			AddRow(vulcanPoolBaseURL, 3, qiuConfig, qiu).
			AddRow(accountPoolBaseURL, 2, qiuConfig, qiu))
	result, err := s.AccountPoolStatus(t.Context(), 1, 1, false)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, result, 3)
	require.Equal(t, "Full pool", result[0].Name)
	require.Equal(t, map[string]int{"Pro Max": 1, "Team": 1}, result[0].Plans)
	require.Equal(t, map[string]int{"active": 1, "disabled": 1}, result[0].Statuses)
	require.Equal(t, 2, result[0].Total)
	require.Equal(t, 2, result[0].Pages)
	require.Len(t, result[0].Rows, 1)
	require.Equal(t, "Qiu API", result[1].Source)
	require.Equal(t, []accountPoolStatusRow{{Number: 1, Platform: "openai", Plan: "Pro Max", Status: "rate_limited"}}, result[1].Rows)
	require.Equal(t, 1, result[1].Total)
	require.False(t, result[2].Available)
	require.Nil(t, result[2].Updated)
	require.Empty(t, result[2].Rows)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{"upstream-name", "source_id", "base_url", "plan_map", "Pro 500"} {
		require.NotContains(t, string(raw), private)
	}
}

func TestPublishedAccountPoolDisplayFields(t *testing.T) {
	var snapshot accountPoolSnapshot
	require.NoError(t, json.Unmarshal([]byte(`{
		"updated":"2026-11-21T07:47:00Z", "checked_at":"2026-11-21T07:48:00Z", "site_revision":1,
		"groups":[{"id":"public","original":"private-group"}], "rows":[
		 {"number":1,"platform":"openai","plan":"Plus","status":"active","pools":["public"],"current":0,"last_used":"2026-11-21T07:35:00Z"},
		 {"number":2,"platform":"openai","plan":"Plus","status":"active","pools":["public","private-id"],"current":2,"limit":5,
		  "last_used":"2026-11-21T07:30:00Z","windows":[{"name":"5h","used":0}],
		  "admin":{"observed_at":"2026-11-21T07:46:00Z","type":"oauth","source_id":"private-account-id","name":"private-email@example.invalid","account_rate":0.4,"rates":{"public":0.5}}},
		 {"number":3,"platform":"openai","plan":"Plus","status":"rate_limited","pools":["public"],
		  "admin":{"usage_updated":"2026-11-21T07:40:00Z","reset_5h":"2026-11-21T12:00:00Z","reset_7d":"2026-11-28T00:00:00Z","recover_at":"2026-11-21T08:00:00Z","priority":17,"intelligence":"private-note"}}
		]}`), &snapshot))
	cfg := AccountPoolStatusConfig{PoolID: "public", Name: "Public pool", Plans: []string{"Plus"}}
	first := cfg.status(snapshot, 1, 1, 2, false)
	require.Equal(t, snapshot.CheckedAt, first.CheckedAt)
	require.Equal(t, 3, first.Total)
	require.Len(t, first.Rows, 2)
	active, quota := first.Rows[0], first.Rows[1]
	require.Equal(t, "active_concurrency", active.ActivityKind)
	require.Equal(t, active.ObservedAt, active.ActivityAt)
	require.Equal(t, "2026-11-21T07:30:00Z", active.LastUsed.Format(time.RFC3339))
	require.Equal(t, "oauth", active.AccountType)
	require.Equal(t, 2, *active.Current)
	require.Equal(t, 5, *active.Limit)
	require.Zero(t, *active.Windows[0].Used)
	require.Equal(t, "usage_updated", quota.ActivityKind)
	require.Equal(t, quota.UsageUpdated, quota.ActivityAt)
	require.Equal(t, "2026-11-21T12:00:00Z", quota.Reset5h.Format(time.RFC3339))
	require.Equal(t, "2026-11-28T00:00:00Z", quota.Reset7d.Format(time.RFC3339))
	require.Equal(t, "2026-11-21T08:00:00Z", quota.RecoverAt.Format(time.RFC3339))
	second := cfg.status(snapshot, 1, 2, 2, false)
	require.Len(t, second.Rows, 1)
	require.Equal(t, 3, second.Rows[0].Number)
	require.Equal(t, "last_used", second.Rows[0].ActivityKind)
	require.Zero(t, *second.Rows[0].Current)
	require.Nil(t, second.Rows[0].Limit)
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	for _, private := range []string{"private-", `"admin"`, `"source_id"`, `"rates"`, `"account_rate"`, `"priority"`} {
		require.NotContains(t, string(raw), private)
	}
	preview := cfg.status(snapshot, 1, 1, 2, true)
	require.Equal(t, &accountPoolStatusIdentity{Name: "private-email@example.invalid", SourceID: "private-account-id"}, preview.Rows[0].Admin)
	require.Nil(t, preview.Rows[1].Admin)
	preview.Rows[0].Admin = nil
	require.Equal(t, first, preview)
}
