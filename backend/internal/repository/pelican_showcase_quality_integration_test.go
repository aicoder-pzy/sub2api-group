//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestPelicanShowcaseQualityMigration(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
CREATE TEMP TABLE pelican_showcase_items (id bigint, source_result_id bigint, response_text text);
CREATE TEMP TABLE pelican_group_test_results (id bigint, response_text text, quality_verdict text, quality_reason text);
CREATE TEMP TABLE scheduled_test_results (id bigint, response_text text, quality_verdict text, quality_reason text, pelican_config jsonb);
INSERT INTO pelican_showcase_items VALUES
 (1,10,'<svg>saved</svg>'),
 (2,20,'<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="degraded" title="old">badge</aside><!--sub2api:pelican-assessment:end--><svg/>'),
 (3,30,'<svg data-sub2api-quality="normal"/>'),
 (4,40,'<svg>scheduled</svg>');
INSERT INTO pelican_group_test_results VALUES (10,'<svg>saved</svg>','normal','stored reason');
INSERT INTO scheduled_test_results VALUES (40,'<svg>scheduled</svg>','unknown','evaluation failed','{}');`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("250_custom_pelican_showcase_quality.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		rows, err := tx.QueryContext(ctx, `SELECT quality_verdict, quality_reason FROM pelican_showcase_items ORDER BY id`)
		require.NoError(t, err)
		var actual [][2]string
		for rows.Next() {
			var value [2]string
			require.NoError(t, rows.Scan(&value[0], &value[1]))
			actual = append(actual, value)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.Equal(t, [][2]string{{"normal", "stored reason"}, {"degraded", ""}, {"unknown", ""}, {"unknown", "evaluation failed"}}, actual)
	}
}
