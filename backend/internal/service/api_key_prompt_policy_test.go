package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPromptPolicyPreservesProtocolsAndExactNumbers(t *testing.T) {
	for _, tc := range []struct{ path, body, field string }{
		{"/v1/responses", `{"model":"m","instructions":"client","input":[{"type":"function_call_output","call_id":"c","output":"ok"}],"n":9007199254740993}`, "instructions"},
		{"/v1/responses/compact", `{"model":"m","instructions":"client","input":"hello","n":9007199254740993}`, "instructions"},
		{"/v1/chat/completions", `{"model":"m","messages":[{"role":"system","content":"client"},{"role":"tool","tool_call_id":"c","content":"ok"}],"tools":[{"type":"function"}],"n":9007199254740993}`, "messages.0.content"},
		{"/v1/messages", `{"model":"m","system":[{"type":"text","text":"client","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}],"n":9007199254740993}`, "system.0.text"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			original := []byte(tc.body)
			got, err := ApplyAPIKeyPromptPolicy(original, tc.path, map[string]string{"m": "custom:policy", "*": "custom:fallback"})
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(gjson.GetBytes(got, tc.field).String(), "policy"))
			require.Contains(t, string(got), "client")
			require.Equal(t, "9007199254740993", gjson.GetBytes(got, "n").Raw)
			// A second upstream attempt starts with the same ingress body, not an
			// already-mutated request. Neither the original nor tool fields change.
			again, err := ApplyAPIKeyPromptPolicy(original, tc.path, map[string]string{"m": "custom:policy", "*": "custom:fallback"})
			require.NoError(t, err)
			require.Equal(t, got, again)
			require.Equal(t, tc.body, string(original))
		})
	}
	for _, body := range []string{`{"model":"m","instructions":[]}`, `{"model":"m","instructions":123}`} {
		_, err := ApplyAPIKeyPromptPolicy([]byte(body), "/v1/responses", map[string]string{"*": "custom:x"})
		require.Error(t, err)
	}
	_, err := ApplyAPIKeyPromptPolicy([]byte(`{"model":"m","messages":{}}`), "/v1/chat/completions", map[string]string{"*": "custom:x"})
	require.Error(t, err)
}

func TestPromptPolicyDefaultsAndCatalogIntegrity(t *testing.T) {
	body := []byte(`{"model":"m","instructions":"original"}`)
	for _, policy := range []map[string]string{nil, {}, {"other": "custom:x"}} {
		got, err := ApplyAPIKeyPromptPolicy(body, "/v1/responses", policy)
		require.NoError(t, err)
		require.Equal(t, body, got)
	}
	got, err := ApplyAPIKeyPromptPolicy(body, "/v1/responses", map[string]string{"*": "custom:fallback"})
	require.NoError(t, err)
	require.Equal(t, "fallback\n\noriginal", gjson.GetBytes(got, "instructions").String())
	seen := map[string]bool{}
	for _, profile := range APIKeyPromptProfiles() {
		require.False(t, seen[profile.ID])
		seen[profile.ID] = true
		text := promptCatalog.Instructions[profile.TextHash]
		require.NotEmpty(t, text)
		require.Equal(t, profile.Bytes, len(text))
		hash := sha256.Sum256([]byte(text))
		require.Equal(t, profile.TextHash, hex.EncodeToString(hash[:]))
		if len(profile.Models) > 0 {
			require.NoError(t, ValidateAPIKeyPromptPolicy(map[string]string{profile.Models[0]: profile.ID}))
			require.Error(t, ValidateAPIKeyPromptPolicy(map[string]string{"*": profile.ID}))
		}
	}
	require.Len(t, seen, 43)
	for _, policy := range []map[string]string{{"*": "unknown"}, {"bad model": "custom:x"}, {"m": "custom:"}, {"m": "custom:" + strings.Repeat("x", 16385)}} {
		require.Error(t, ValidateAPIKeyPromptPolicy(policy))
	}
	require.NoError(t, ValidateAPIKeyPromptPolicy(map[string]string{"*": "zako-default"}))
}

func TestPromptPolicyAuthCacheRoundtrip(t *testing.T) {
	key := &APIKey{ID: 1, UserID: 2, Key: "test-key", Status: StatusActive, User: &User{ID: 2, Status: StatusActive}, PromptPolicy: map[string]string{"*": "custom:hello"}}
	svc := &APIKeyService{}
	encoded, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), key)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(encoded, &cached))
	got, used, err := svc.applyAuthCacheEntry(key.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, key.PromptPolicy, got.PromptPolicy)
}
