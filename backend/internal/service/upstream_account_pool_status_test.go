package service

import (
	"encoding/json"
	"testing"

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
	result, err := s.PublishedAccountPools(t.Context(), 1, 1)
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
