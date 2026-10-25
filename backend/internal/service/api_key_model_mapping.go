package service

import (
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ValidateAPIKeyModelMapping bounds user-controlled aliases. Mappings are exact
// and run once, so targets may also be aliases without recursive expansion.
func ValidateAPIKeyModelMapping(mapping map[string]string) error {
	if len(mapping) > 100 {
		return infraerrors.BadRequest("INVALID_MODEL_MAPPING", "At most 100 model mappings are allowed")
	}
	for from, to := range mapping {
		for _, model := range []string{from, to} {
			if model == "" || len(model) > 256 || strings.ContainsAny(model, "*?#\\") || strings.ContainsFunc(model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
				return infraerrors.BadRequest("INVALID_MODEL_MAPPING", "Model names must be 1-256 bytes without whitespace, wildcards, or URL delimiters")
			}
		}
	}
	return nil
}

func (k *APIKey) ResolveModel(model string) string {
	if k != nil {
		if mapped := k.ModelMapping[model]; mapped != "" {
			return mapped
		}
	}
	return model
}
