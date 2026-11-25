package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestMonitorIntelligenceKeepsModelsAndMonitorsSeparate(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &channelMonitorRepository{db: db}
	rows := sqlmock.NewRows([]string{"id", "model", "plan", "kind", "name", "state", "history"}).
		AddRow(14, "gpt-6.2-sol", 1, "candy", "", "normal", `[{"status":"normal","checked_at":"2026-11-25T01:00:00Z","answer":"21"}]`).
		AddRow(14, "gpt-6.2-sol", 2, "pelican", "", "normal", `[{"status":"normal","checked_at":"2026-11-25T00:55:00Z"}]`).
		AddRow(14, "gpt-6.2-astra", 3, "candy", "", "degraded", `[{"status":"degraded","checked_at":"2026-11-25T01:05:00Z","answer":"29"}]`).
		AddRow(14, "no-tests", 0, "pelican", "", "no_plan", `[]`).
		AddRow(16, "gpt-6.2-astra", 4, "candy", "", "failed", `[]`).
		AddRow(16, "gpt-6.2-sol", 5, "candy", "", "pending", `[{"status":"normal","checked_at":"2026-11-25T00:30:00Z","answer":"21"}]`)
	mock.ExpectQuery(channelMonitorIntelligenceSQL).
		WithArgs(pq.Array([]int64{14, 16}), pq.Array(service.BuiltinCandyPrompts())).WillReturnRows(rows)
	results, err := repo.ListMonitorIntelligence(context.Background(), []int64{14, 16})
	require.NoError(t, err)
	require.Len(t, results[14]["gpt-6.2-sol"].Tests, 2)
	require.Equal(t, "unknown", results[14]["gpt-6.2-sol"].State)
	require.Equal(t, "21", results[14]["gpt-6.2-sol"].History[1].Answer)
	astra := results[14]["gpt-6.2-astra"]
	require.Equal(t, "degraded", astra.State)
	require.Len(t, astra.Tests, 1)
	require.Equal(t, "candy", astra.Tests[0].Kind)
	require.Equal(t, "29", astra.History[0].Answer)
	require.Equal(t, "failed", results[16]["gpt-6.2-astra"].State)
	require.Equal(t, "pending", results[16]["gpt-6.2-sol"].State)
	require.Equal(t, "normal", results[16]["gpt-6.2-sol"].History[0].Status)
	require.Equal(t, "21", results[16]["gpt-6.2-sol"].History[0].Answer)
	require.Equal(t, "no_plan", results[14]["no-tests"].State)
	require.Empty(t, results[14]["no-tests"].Tests)
	require.Empty(t, results[14]["no-tests"].History)
	encoded, err := json.Marshal(astra)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "account")
	require.NoError(t, mock.ExpectationsWereMet())
}
