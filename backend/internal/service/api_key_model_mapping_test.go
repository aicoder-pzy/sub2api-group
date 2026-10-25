//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyModelMappingValidationAndCache(t *testing.T) {
	for _, mapping := range []map[string]string{{"": "a"}, {"a": ""}, {"a*": "b"}, {"a": "b c"}, {"a": "b\n"}, {"a": strings.Repeat("x", 257)}} {
		require.Error(t, ValidateAPIKeyModelMapping(mapping))
	}
	mapping := map[string]string{"alias": "target", "target": "other"}
	require.NoError(t, ValidateAPIKeyModelMapping(mapping))
	key := &APIKey{ID: 1, UserID: 7, Key: "sk-fixture", ModelMapping: mapping, User: &User{ID: 7}}
	svc := &APIKeyService{}
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	got := svc.snapshotToAPIKey(key.Key, snapshot)
	require.Equal(t, "target", got.ResolveModel("alias"))
	require.Equal(t, "other", got.ResolveModel("target"))
	require.Equal(t, "unknown", got.ResolveModel("unknown"))
	_, valid, err := svc.applyAuthCacheEntry(key.Key, &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 26}})
	require.NoError(t, err)
	require.False(t, valid)
}

func TestAPIKeyModelMappingUpdateOwnershipAndClear(t *testing.T) {
	key := &APIKey{ID: 1, UserID: 7, Key: "sk-fixture", ModelMapping: map[string]string{"a": "b"}, QuotaUsed: 42}
	svc, repo := newUpdateFieldsAPIKeyService(key)
	empty := map[string]string{}
	_, err := svc.Update(context.Background(), 1, 8, UpdateAPIKeyRequest{ModelMapping: &empty})
	require.ErrorIs(t, err, ErrInsufficientPerms)
	require.Empty(t, repo.updateFields)
	got, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{ModelMapping: &empty})
	require.NoError(t, err)
	require.Empty(t, got.ModelMapping)
	require.Equal(t, float64(42), got.QuotaUsed)
	require.Equal(t, []APIKeyUpdateFields{{ModelMapping: true}}, repo.updateFields)
}
