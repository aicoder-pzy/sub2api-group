package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestFastestFailoverSettingsPersistenceAndAttemptSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	svc := &SettingService{settingRepo: repo}
	require.Equal(t, DefaultFastestFailoverSettings(), svc.FastestFailoverSettings(ctx))
	settings := FastestFailoverSettings{15, 40, 80, 120, 10}
	require.NoError(t, svc.SetFastestFailoverSettings(ctx, settings))
	loaded, err := (&SettingService{settingRepo: repo}).GetFastestFailoverSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, settings, loaded)
	ctx = context.WithValue(ctx, ctxkey.Group, &Group{AccountSchedulingMode: AccountSchedulingModeFastestFailover})
	_, attempt := beginFastestFailoverAttempt(ctx, &Account{ID: 1}, svc)
	require.Equal(t, 15*time.Second, attempt.firstTimeout)
	require.Equal(t, 40*time.Second, attempt.idleTimeout)
	require.Equal(t, 80*time.Second, attempt.cooldown)
	require.NoError(t, svc.SetFastestFailoverSettings(ctx, DefaultFastestFailoverSettings()))
	require.Equal(t, 15*time.Second, attempt.firstTimeout)
	require.Equal(t, DefaultFastestFailoverSettings(), svc.FastestFailoverSettings(ctx))
	require.NoError(t, svc.SetFastestFailoverSettings(ctx, settings))
	repo.getValueErr = errors.New("offline")
	svc.fastestFailoverCache.expires = time.Time{}
	require.Equal(t, settings, svc.FastestFailoverSettings(ctx))
	repo.getValueErr = nil
	for _, invalid := range []FastestFailoverSettings{{0, 120, 120, 120, 10}, {601, 120, 120, 120, 10}, {60, 1801, 120, 120, 10}, {60, 120, -1, 120, 10}, {60, 120, 86401, 120, 10}, {60, 120, 120, 0, 10}, {60, 120, 120, 120, 0}} {
		require.Error(t, svc.SetFastestFailoverSettings(ctx, invalid))
	}
	require.Equal(t, settings, svc.FastestFailoverSettings(ctx))
}

func TestFastestFailoverSettingsLegacyDefaults(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	require.NoError(t, repo.Set(ctx, SettingKeyFastestFailover, `{"first_output_timeout_seconds":15,"stream_idle_timeout_seconds":40,"model_cooldown_seconds":80}`))
	settings, err := (&SettingService{settingRepo: repo}).GetFastestFailoverSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, FastestFailoverSettings{15, 40, 80, 120, 10}, settings)
}
