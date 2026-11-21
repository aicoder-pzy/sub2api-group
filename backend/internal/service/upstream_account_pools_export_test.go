package service

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestExportGroupAccountPools(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := &UpstreamBillingProbeService{db: db}
	const shenji = `{
	  "updated":"2026-11-19T10:00:00Z",
	  "groups":[{"source_id":"10","id":"old","count":2},{"source_id":"20","id":"new","count":1},{"source_id":"97","id":"other","count":1}],
	  "rows":[{"number":1,"platform":"openai","plan":"promax","status":"active","pools":["old","new"]},
	          {"number":2,"plan":"Plus","pools":["old"]},{"number":3,"plan":"Free","pools":["other"]}]
	}`
	const vulcan = `{
	  "updated":"2026-11-19T09:00:00Z","site_revision":2,
	  "groups":[{"source_id":"97","id":"stock","count":1}],
	  "rows":[{"number":1,"platform":"openai","plan":"Pro 500","status":"rate_limited","current":0,"limit":10,"windows":[{"name":"5h","used":25}],"pools":["stock"],"admin":{"usage_updated":"2026-11-19T08:59:00Z","name":"internal-label"}}]
	}`
	const initial = `{"revision":1,"received_at":"2026-11-19T10:12:00Z","keys":[{"id":"1","group_id":"10","available":true},{"id":"2","group_id":"20","available":true}]}`
	const switched = `{"revision":1,"received_at":"2026-11-19T10:14:00Z","keys":[{"id":"1","group_id":"20","available":true},{"id":"2","group_id":"10","available":false}]}`
	const other = `{"revision":2,"received_at":"2026-11-19T09:15:00Z","errors":{"wallet":"unavailable"},"keys":[{"id":"1","group_id":"141","available":true}]}`
	columns := []string{"name", "base_url", "revision", "keys", "inventory", "snapshot"}
	for _, tc := range []struct {
		name, inventory, keys, second, secondSnapshot string
		complete                                      bool
		count                                         int
	}{
		{"two sites, overlapping membership", initial, `["1","1","2"]`, other, vulcan, true, 3},
		{"switch invalidated inventory", `{}`, `["1"]`, other, vulcan, false, 1},
		{"switch confirmed, old group excluded", switched, `["1"]`, other, vulcan, true, 2},
		{"disabled Key gone", switched, `["1","2"]`, other, vulcan, false, 2},
		{"Key sync error invalidates retained mappings", `{"revision":1,"received_at":"2026-11-19T10:12:00Z","errors":{"keys":"failed"},"keys":[{"id":"1","group_id":"10","available":true}]}`, `["1"]`, other, vulcan, false, 1},
		{"unknown group and missing snapshot", `{"revision":1,"received_at":"2026-11-19T10:12:00Z","keys":[{"id":"1","group_id":"404","available":true}]}`, `["1"]`, other, `{}`, false, 0},
		{"Vulcan mapping does not apply to Shenji", `{"revision":1,"received_at":"2026-11-19T10:12:00Z","keys":[{"id":"1","group_id":"141","available":true}]}`, `["1"]`, other, vulcan, false, 1},
		{"wrong snapshot revision", initial, `["2"]`, other, shenji, false, 1},
		{"dangling or missing Key", initial, `[null,"999"]`, other, vulcan, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectQuery(regexp.QuoteMeta(groupAccountPoolsQuery)).WithArgs(int64(24), accountPoolBaseURL, vulcanPoolBaseURL).
				WillReturnRows(sqlmock.NewRows(columns).AddRow("Ultra", accountPoolBaseURL, 1, tc.keys, tc.inventory, shenji).
					AddRow("Ultra", vulcanPoolBaseURL, 2, `["1"]`, tc.second, tc.secondSnapshot))
			out, err := s.ExportGroupAccountPools(t.Context(), 24, 2, 1)
			require.NoError(t, err)
			require.Equal(t, tc.count, out.Total)
			require.Equal(t, tc.complete, out.Complete)
			require.Equal(t, "Ultra", out.Name)
			require.Equal(t, max(1, tc.count), out.Pages)
			if tc.count == 2 {
				require.Equal(t, []groupAccountPoolPlan{{Code: "pro_500", Name: "Pro 500", Count: 2}}, out.Plans)
				require.Len(t, out.Rows, 1)
				require.Equal(t, 2, out.Rows[0].Number)
				require.Equal(t, "rate_limited", out.Rows[0].Status)
				require.Equal(t, "oauth", out.Rows[0].AccountType)
				require.Equal(t, 0, *out.Rows[0].Current)
				require.Equal(t, 25.0, *out.Rows[0].Windows[0].Used)
				require.Equal(t, 9, out.Updated.Hour())
				require.Equal(t, 15, out.VerifiedAt.Minute())
				body, err := json.Marshal(out)
				require.NoError(t, err)
				for _, private := range []string{`"pools"`, `"stock"`, `"source_id"`, `"site_revision"`, `"group_id":"20"`, `"admin"`, `"internal-label"`} {
					require.NotContains(t, string(body), private)
				}
			} else if tc.count < 2 {
				require.Empty(t, out.Rows)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	mock.ExpectQuery(regexp.QuoteMeta(groupAccountPoolsQuery)).WillReturnRows(sqlmock.NewRows(columns))
	_, err = s.ExportGroupAccountPools(t.Context(), 24, 1, 20)
	require.ErrorIs(t, err, ErrGroupNotFound)
	mock.ExpectQuery(regexp.QuoteMeta(groupAccountPoolsQuery)).WillReturnRows(sqlmock.NewRows(columns).AddRow("Empty", "", 0, `[]`, `{}`, `{}`))
	out, err := s.ExportGroupAccountPools(t.Context(), 24, 1, 20)
	require.NoError(t, err)
	require.True(t, out.Complete)
	require.Empty(t, out.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
