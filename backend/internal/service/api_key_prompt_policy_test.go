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
	require.Len(t, seen, 58)
	require.Equal(t, "b129465c69fd806a31c0590d0e3d9b4d14603d125ad8dfbbf92dbaa1b7f72b55", promptProfile("zako-default").TextHash)
	for _, policy := range []map[string]string{{"*": "unknown"}, {"bad model": "custom:x"}, {"m": "custom:"}, {"m": "custom:" + strings.Repeat("x", 16385)}} {
		require.Error(t, ValidateAPIKeyPromptPolicy(policy))
	}
	require.NoError(t, ValidateAPIKeyPromptPolicy(map[string]string{"*": "zako-default"}))
}

func TestPromptPolicyColdcoffeeTemplatesRequireExactModels(t *testing.T) {
	templates := 0
	for _, profile := range APIKeyPromptProfiles() {
		if profile.ID == "zako-default" {
			continue
		}
		require.NotNil(t, profile.Metadata)
		require.Equal(t, "3249afce6b853ff2f2133e016b223db2cf38b087", profile.Metadata.SourceRevision)
		require.Equal(t, "unverified", profile.Metadata.Validation)
		if !profile.Template {
			continue
		}
		templates++
		require.Error(t, ValidateAPIKeyPromptPolicy(map[string]string{"*": profile.ID}))
		for _, model := range []string{"my-actual-model", "deepseek-v4.1-flash"} {
			require.NoError(t, ValidateAPIKeyPromptPolicy(map[string]string{model: profile.ID}))
			body, _ := json.Marshal(map[string]string{"model": model, "instructions": "client"})
			got, err := ApplyAPIKeyPromptPolicy(body, "/v1/responses", map[string]string{model: profile.ID})
			require.NoError(t, err)
			hash := profile.TextHash
			if strings.HasPrefix(model, "deepseek-") {
				hash = profile.DeepSeekTextHash
				require.Contains(t, promptCatalog.Instructions[hash], "# DeepSeek API request adapter")
			}
			require.Equal(t, promptCatalog.Instructions[hash]+"\n\nclient", gjson.GetBytes(got, "instructions").String())
			// Unselected models, including stale wildcard entries, stay untouched.
			for _, policy := range []map[string]string{{"other": profile.ID}, {"*": profile.ID}} {
				unchanged, err := ApplyAPIKeyPromptPolicy(body, "/v1/responses", policy)
				require.NoError(t, err)
				require.Equal(t, body, unchanged)
			}
		}
	}
	require.Equal(t, 15, templates)
	require.Equal(t, []string{"gpt-6-astra"}, promptProfile("gpt-6-astra-v2-rc1").Models)
	require.Equal(t, promptProfile("coldcoffee@codex").TextHash, promptProfile("gpt-6-astra-v2-rc1").TextHash)
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
