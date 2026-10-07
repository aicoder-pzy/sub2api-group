package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyFastestFailover = "fastest_failover_settings"

type FastestFailoverSettings struct {
	FirstOutputTimeoutSeconds int `json:"first_output_timeout_seconds"`
	StreamIdleTimeoutSeconds  int `json:"stream_idle_timeout_seconds"`
	ModelCooldownSeconds      int `json:"model_cooldown_seconds"`
}

func DefaultFastestFailoverSettings() FastestFailoverSettings {
	return FastestFailoverSettings{60, 120, 120}
}

func (settings FastestFailoverSettings) Validate() error {
	for _, field := range []struct {
		name            string
		value, min, max int
	}{
		{"first_output_timeout_seconds", settings.FirstOutputTimeoutSeconds, 1, 600},
		{"stream_idle_timeout_seconds", settings.StreamIdleTimeoutSeconds, 1, 1800},
		{"model_cooldown_seconds", settings.ModelCooldownSeconds, 1, 86400},
	} {
		if field.value < field.min || field.value > field.max {
			return infraerrors.BadRequest("INVALID_SCHEDULING_SETTINGS", fmt.Sprintf("%s must be between %d and %d", field.name, field.min, field.max))
		}
	}
	return nil
}

type cachedFastestFailoverSettings struct {
	settings FastestFailoverSettings
	expires  time.Time
}

func (s *SettingService) GetFastestFailoverSettings(ctx context.Context) (FastestFailoverSettings, error) {
	settings := DefaultFastestFailoverSettings()
	value, err := s.settingRepo.GetValue(ctx, SettingKeyFastestFailover)
	if errors.Is(err, ErrSettingNotFound) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, err
	}
	return settings, settings.Validate()
}

func (s *SettingService) SetFastestFailoverSettings(ctx context.Context, settings FastestFailoverSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	s.fastestFailoverMu.Lock()
	defer s.fastestFailoverMu.Unlock()
	if err := s.settingRepo.Set(ctx, SettingKeyFastestFailover, string(data)); err != nil {
		return err
	}
	s.fastestFailoverCache = &cachedFastestFailoverSettings{settings, time.Now().Add(30 * time.Second)}
	return nil
}

// Requests snapshot the settings at the start of an attempt. Saving updates the
// local cache immediately; other instances observe the change within 30 seconds.
func (s *SettingService) FastestFailoverSettings(ctx context.Context) FastestFailoverSettings {
	if s == nil || s.settingRepo == nil {
		return DefaultFastestFailoverSettings()
	}
	s.fastestFailoverMu.Lock()
	defer s.fastestFailoverMu.Unlock()
	if s.fastestFailoverCache != nil && time.Now().Before(s.fastestFailoverCache.expires) {
		return s.fastestFailoverCache.settings
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	settings, err := s.GetFastestFailoverSettings(readCtx)
	ttl := 30 * time.Second
	if err != nil {
		settings = DefaultFastestFailoverSettings()
		if s.fastestFailoverCache != nil {
			settings = s.fastestFailoverCache.settings
		}
		ttl = 5 * time.Second
	}
	s.fastestFailoverCache = &cachedFastestFailoverSettings{settings, time.Now().Add(ttl)}
	return settings
}
