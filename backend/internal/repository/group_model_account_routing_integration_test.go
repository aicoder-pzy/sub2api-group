//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestGroupModelAccountRoutingMigration(t *testing.T) {
	ctx := context.Background()
	groups := createPelicanGroupTestGroups(t, "routing-a", "routing-b", "routing-c", "routing-existing")
	x := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "routing-shared"})
	y := mustCreateAccount(t, testEntClient(t), &service.Account{Name: "routing-only-b"})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, x.ID, y.ID)
	})
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO account_groups (account_id,group_id) VALUES ($1,$3),($1,$4),($2,$4)`, x.ID, y.ID, groups[0].ID, groups[1].ID)
	require.NoError(t, err)
	legacy := fmt.Sprintf(`{"rules":[{"model":"shared","account_ids":[%d,%d]},{"model":"blocked","account_ids":[]},{"model":"only-b","account_ids":[%d]}]}`, x.ID, y.ID, y.ID)
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ('model_account_routing',$1,NOW()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, legacy)
	require.NoError(t, err)
	key := func(index int) string { return fmt.Sprintf("model_account_routing_group_%d", groups[index].ID) }
	_, err = tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ($1,'{"rules":[]}',NOW())`, key(3))
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("249_custom_group_model_account_routing.sql")
	require.NoError(t, err)
	read := func(key string) string {
		t.Helper()
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&value))
		return value
	}
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"rules":[{"model":"shared","account_ids":[%d]},{"model":"blocked","account_ids":[]}]}`, x.ID), read(key(0)))
		require.JSONEq(t, legacy, read(key(1)))
		require.JSONEq(t, `{"rules":[{"model":"blocked","account_ids":[]}]}`, read(key(2)), "unrelated groups retain only the explicit deny-all rule")
		require.JSONEq(t, `{"rules":[]}`, read(key(3)), "existing scoped settings must not be overwritten")
		require.JSONEq(t, legacy, read("model_account_routing"), "rollback keeps the original global snapshot")
	}
	var settings service.ModelAccountRoutingSettings
	require.NoError(t, json.Unmarshal([]byte(read(key(0))), &settings))
	require.NoError(t, settings.Validate(), "migrated values must pass application validation")
}
