package service

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

//go:embed zako_profiles/catalog.json
var promptCatalogJSON []byte

type PromptProfile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Models   []string `json:"models"`
	Bytes    int      `json:"bytes"`
	TextHash string   `json:"text_hash"`
}

var promptCatalog struct {
	once         sync.Once
	Profiles     []PromptProfile   `json:"profiles"`
	Instructions map[string]string `json:"instructions"`
}

func APIKeyPromptProfiles() []PromptProfile {
	promptCatalog.once.Do(func() {
		if err := json.Unmarshal(promptCatalogJSON, &promptCatalog); err != nil {
			panic(err)
		}
	})
	return promptCatalog.Profiles
}

func promptProfile(id string) *PromptProfile {
	for i := range APIKeyPromptProfiles() {
		if promptCatalog.Profiles[i].ID == id {
			return &promptCatalog.Profiles[i]
		}
	}
	return nil
}

// A policy maps exact effective client model IDs to immutable catalog IDs or
// custom:<text>. The * entry is an optional fallback. An empty policy is off.
func ValidateAPIKeyPromptPolicy(policy map[string]string) error {
	invalid := func() error {
		return infraerrors.BadRequest("INVALID_PROMPT_POLICY", "Invalid prompt policy: use exact model IDs, a compatible built-in profile, or custom text (maximum 16 KiB each, 64 KiB total)")
	}
	if len(policy) > 100 {
		return invalid()
	}
	total := 0
	for model, id := range policy {
		if model != "*" {
			if err := ValidateAPIKeyModelMapping(map[string]string{model: model}); err != nil {
				return invalid()
			}
		}
		total += len(model) + len(id)
		if strings.HasPrefix(id, "custom:") {
			text := strings.TrimPrefix(id, "custom:")
			if strings.TrimSpace(text) == "" || len(text) > 16384 || strings.ContainsRune(text, 0) {
				return invalid()
			}
			continue
		}
		profile := promptProfile(id)
		if profile == nil {
			return invalid()
		}
		if len(profile.Models) > 0 {
			found := false
			for _, supported := range profile.Models {
				found = found || supported == model
			}
			if !found {
				return invalid()
			}
		}
	}
	if total > 65536 {
		return invalid()
	}
	return nil
}

func ApplyAPIKeyPromptPolicy(body []byte, path string, policy map[string]string) ([]byte, error) {
	return applyAPIKeyPromptPolicyForModel(body, path, policy, "")
}

func applyAPIKeyPromptPolicyForModel(body []byte, path string, policy map[string]string, model string) ([]byte, error) {
	if len(policy) == 0 {
		return body, nil
	}
	var field string
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		field = "messages"
	case strings.HasSuffix(path, "/messages"), strings.HasSuffix(path, "/messages/count_tokens"):
		field = "system"
	case strings.HasSuffix(path, "/responses"), strings.HasSuffix(path, "/responses/compact"):
		field = "instructions"
	default:
		return body, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return nil, infraerrors.BadRequest("INVALID_PROMPT_REQUEST", "Prompt policies require a JSON object")
	}
	if model == "" {
		_ = json.Unmarshal(payload["model"], &model)
	}
	id := policy[model]
	if id == "" {
		id = policy["*"]
	}
	if id == "" {
		return body, nil
	}
	instruction := ""
	if strings.HasPrefix(id, "custom:") {
		instruction = strings.TrimPrefix(id, "custom:")
	} else if profile := promptProfile(id); profile != nil {
		instruction = promptCatalog.Instructions[profile.TextHash]
	}
	if instruction == "" {
		return body, nil
	}
	previous := payload[field]
	var combined any
	switch field {
	case "messages":
		var messages []json.RawMessage
		if len(previous) == 0 || json.Unmarshal(previous, &messages) != nil || messages == nil {
			return nil, infraerrors.BadRequest("INVALID_PROMPT_REQUEST", "messages must be an array")
		}
		prefix, _ := json.Marshal(map[string]string{"role": "system", "content": instruction})
		combined = append([]json.RawMessage{prefix}, messages...)
	default:
		var text string
		if len(previous) == 0 || string(previous) == "null" {
			combined = instruction
		} else if json.Unmarshal(previous, &text) == nil {
			combined = instruction
			if text != "" {
				combined = instruction + "\n\n" + text
			}
		} else if field == "system" {
			var blocks []json.RawMessage
			if json.Unmarshal(previous, &blocks) != nil {
				return nil, infraerrors.BadRequest("INVALID_PROMPT_REQUEST", "system must be text or an array")
			}
			prefix, _ := json.Marshal(map[string]string{"type": "text", "text": instruction})
			combined = append([]json.RawMessage{prefix}, blocks...)
		} else {
			return nil, infraerrors.BadRequest("INVALID_PROMPT_REQUEST", "instructions must be text")
		}
	}
	encoded, err := json.Marshal(combined)
	if err != nil {
		return nil, err
	}
	payload[field] = encoded
	return json.Marshal(payload)
}
