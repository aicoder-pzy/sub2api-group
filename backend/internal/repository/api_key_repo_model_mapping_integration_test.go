//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyModelMappingPersistenceAndAuthProjection(t *testing.T) {
	ctx := context.Background()
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("key-mapping-%d@example.com", time.Now().UnixNano()), Concurrency: 1})
	repo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: owner.ID, Key: fmt.Sprintf("sk-key-mapping-%d", time.Now().UnixNano()), Name: "mapping fixture", Status: service.StatusActive, ModelMapping: map[string]string{"alias": "target"}}
	require.NoError(t, repo.Create(ctx, key))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
		require.NoError(t, err)
	})
	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, key.ModelMapping, got.ModelMapping)
	key.ModelMapping = map[string]string{}
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{ModelMapping: true}))
	got, err = repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Empty(t, got.ModelMapping)
}
