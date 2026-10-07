package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDirectAccessValidation(test *testing.T) {
	settings, err := normalizeDirectAccessSettings(DirectAccessSettings{Entries: []DirectAccessEntry{
		{CIDR: " 47.239.86.227 ", Note: " initial "},
		{CIDR: "203.0.113.10/24"},
		{CIDR: "2001:db8::1/64"},
	}})
	require.NoError(test, err)
	require.Equal(test, []DirectAccessEntry{{CIDR: "47.239.86.227/32", Note: "initial"}, {CIDR: "203.0.113.0/24"}, {CIDR: "2001:db8::/64"}}, settings.Entries)
	for _, entries := range [][]DirectAccessEntry{
		{{CIDR: ""}}, {{CIDR: "example.com"}}, {{CIDR: "47.239.86.227/33"}},
		{{CIDR: "fe80::1%eth0"}}, {{CIDR: "47.239.86.227", Note: strings.Repeat("字", 101)}},
		{{CIDR: "47.239.86.227"}, {CIDR: "47.239.86.227/32"}},
		{{CIDR: "203.0.113.1/24"}, {CIDR: "203.0.113.2/24"}},
		make([]DirectAccessEntry, 257),
	} {
		_, err := normalizeDirectAccessSettings(DirectAccessSettings{Entries: entries})
		require.Error(test, err)
	}
}

func TestDirectAccessPersistenceAndImmediateRevocation(test *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	settings := NewSettingService(repo, &config.Config{})
	allowed, err := settings.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.NoError(test, err)
	require.False(test, allowed)
	_, err = settings.SetDirectAccessSettings(ctx, DirectAccessSettings{Entries: []DirectAccessEntry{
		{CIDR: "47.239.86.227"}, {CIDR: "203.0.113.0/24"}, {CIDR: "2001:db8::/64"},
	}})
	require.NoError(test, err)
	for source, expected := range map[string]bool{
		"47.239.86.227": true, "::ffff:47.239.86.227": true, "47.239.86.228": false,
		"203.0.113.200": true, "203.0.114.1": false, "2001:db8::99": true,
		"2001:db9::1": false, "": false, "47.239.86.227, 1.2.3.4": false,
	} {
		allowed, err := settings.IsDirectAccessAllowed(ctx, source)
		require.NoError(test, err)
		require.Equal(test, expected, allowed, source)
	}
	restarted := NewSettingService(repo, &config.Config{})
	allowed, err = restarted.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.NoError(test, err)
	require.True(test, allowed)
	_, err = settings.SetDirectAccessSettings(ctx, DirectAccessSettings{Entries: []DirectAccessEntry{}})
	require.NoError(test, err)
	allowed, err = settings.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.NoError(test, err)
	require.False(test, allowed)
}

type directAccessWriteFailureRepo struct {
	SettingRepository
}

func (repo directAccessWriteFailureRepo) Set(context.Context, string, string) error {
	return errors.New("database unavailable")
}

func TestDirectAccessFailsClosed(test *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	settings := NewSettingService(repo, &config.Config{})
	_, err := settings.SetDirectAccessSettings(ctx, DirectAccessSettings{Entries: []DirectAccessEntry{{CIDR: "47.239.86.227"}}})
	require.NoError(test, err)
	settings.settingRepo = directAccessWriteFailureRepo{SettingRepository: repo}
	_, err = settings.SetDirectAccessSettings(ctx, DirectAccessSettings{})
	require.Error(test, err)
	allowed, err := settings.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.NoError(test, err)
	require.True(test, allowed)
	settings.directAccessCache.expiresAt = time.Time{}
	repo.getValueErr = errors.New("database unavailable")
	allowed, err = settings.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.Error(test, err)
	require.False(test, allowed)
	repo.getValueErr = nil
	repo.values[SettingKeyDirectAccess] = `{"entries":[{"cidr":"invalid"}]}`
	settings.directAccessCache.expiresAt = time.Time{}
	allowed, err = settings.IsDirectAccessAllowed(ctx, "47.239.86.227")
	require.Error(test, err)
	require.False(test, allowed)
}
