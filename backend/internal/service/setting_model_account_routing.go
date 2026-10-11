package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyModelAccountRouting = "model_account_routing"

func modelAccountRoutingKey(groupID int64) string {
	return fmt.Sprintf("%s_group_%d", SettingKeyModelAccountRouting, groupID)
}

type ModelAccountRoutingRule struct {
	Model      string  `json:"model"`
	AccountIDs []int64 `json:"account_ids"`
}

type ModelAccountRoutingSettings struct {
	Rules []ModelAccountRoutingRule `json:"rules"`
}

func (settings ModelAccountRoutingSettings) Validate() error {
	if settings.Rules == nil || len(settings.Rules) > 256 {
		return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "rules must be an array with at most 256 models")
	}
	models := make(map[string]struct{}, len(settings.Rules))
	for _, rule := range settings.Rules {
		if rule.Model == "" || len(rule.Model) > 200 || strings.ContainsAny(rule.Model, "*?") ||
			strings.IndexFunc(rule.Model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "model must be an exact model ID without whitespace or wildcards")
		}
		if _, duplicate := models[rule.Model]; duplicate {
			return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", fmt.Sprintf("duplicate model: %s", rule.Model))
		}
		models[rule.Model] = struct{}{}
		if rule.AccountIDs == nil || len(rule.AccountIDs) > 4096 {
			return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "account_ids must be an array with at most 4096 accounts")
		}
		ids := make(map[int64]struct{}, len(rule.AccountIDs))
		for _, id := range rule.AccountIDs {
			if id <= 0 {
				return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "account IDs must be positive integers")
			}
			if _, duplicate := ids[id]; duplicate {
				return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "duplicate account ID")
			}
			ids[id] = struct{}{}
		}
	}
	return nil
}

type cachedModelAccountRouting struct {
	accounts map[string]map[int64]struct{}
	expires  time.Time
	err      error
}

func compileModelAccountRouting(settings ModelAccountRoutingSettings) *cachedModelAccountRouting {
	cache := &cachedModelAccountRouting{accounts: make(map[string]map[int64]struct{}, len(settings.Rules)), expires: time.Now().Add(5 * time.Second)}
	for _, rule := range settings.Rules {
		ids := make(map[int64]struct{}, len(rule.AccountIDs))
		for _, id := range rule.AccountIDs {
			ids[id] = struct{}{}
		}
		cache.accounts[rule.Model] = ids
	}
	return cache
}

func (s *SettingService) GetModelAccountRouting(ctx context.Context, groupID int64) (ModelAccountRoutingSettings, error) {
	var settings ModelAccountRoutingSettings
	if groupID <= 0 {
		return settings, infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "a positive group_id is required")
	}
	value, err := s.settingRepo.GetValue(ctx, modelAccountRoutingKey(groupID))
	if errors.Is(err, ErrSettingNotFound) || (err == nil && value == "") {
		return ModelAccountRoutingSettings{Rules: []ModelAccountRoutingRule{}}, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, err
	}
	return settings, settings.Validate()
}

func (s *SettingService) SetModelAccountRouting(ctx context.Context, groupID int64, settings ModelAccountRoutingSettings) error {
	if groupID <= 0 {
		return infraerrors.BadRequest("INVALID_MODEL_ACCOUNT_ROUTING", "a positive group_id is required")
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	s.modelAccountRoutingMu.Lock()
	defer s.modelAccountRoutingMu.Unlock()
	if err := s.settingRepo.Set(ctx, modelAccountRoutingKey(groupID), string(data)); err != nil {
		return err
	}
	if s.modelAccountRoutingCache == nil {
		s.modelAccountRoutingCache = make(map[int64]*cachedModelAccountRouting)
	}
	s.modelAccountRoutingCache[groupID] = compileModelAccountRouting(settings)
	return nil
}

// Restrictions apply only within the scheduling group, to the public request
// model before channel/account mapping. A failed read denies that group's requests.
func (s *SettingService) IsModelAccountAllowed(ctx context.Context, groupID *int64, requestedModel string, accountID int64) bool {
	if s == nil || s.settingRepo == nil || groupID == nil || *groupID <= 0 {
		return true
	}
	if model, ok := ctx.Value(ctxkey.Model).(string); ok && strings.TrimSpace(model) != "" {
		requestedModel = model
	} else if publicModel, ok := RequestedPublicModelFromContext(ctx); ok {
		requestedModel = publicModel
	}
	s.modelAccountRoutingMu.Lock()
	defer s.modelAccountRoutingMu.Unlock()
	cache := s.modelAccountRoutingCache[*groupID]
	if cache == nil || !time.Now().Before(cache.expires) {
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		settings, err := s.GetModelAccountRouting(readCtx, *groupID)
		if err != nil {
			cache = &cachedModelAccountRouting{err: err, expires: time.Now().Add(time.Second)}
		} else {
			cache = compileModelAccountRouting(settings)
		}
		if s.modelAccountRoutingCache == nil {
			s.modelAccountRoutingCache = make(map[int64]*cachedModelAccountRouting)
		}
		s.modelAccountRoutingCache[*groupID] = cache
	}
	if cache.err != nil {
		return false
	}
	ids, restricted := cache.accounts[strings.TrimSpace(requestedModel)]
	if !restricted {
		return true
	}
	_, allowed := ids[accountID]
	return allowed
}

func (s *OpenAIGatewayService) IsModelAccountAllowed(ctx context.Context, groupID *int64, requestedModel string, accountID int64) bool {
	if s == nil {
		return false
	}
	return s.settingService.IsModelAccountAllowed(ctx, groupID, requestedModel, accountID)
}
