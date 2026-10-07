package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyDirectAccess = "direct_access_allowlist"

type DirectAccessEntry struct {
	CIDR string `json:"cidr"`
	Note string `json:"note"`
}

type DirectAccessSettings struct {
	Entries []DirectAccessEntry `json:"entries"`
}

type cachedDirectAccessSettings struct {
	settings  DirectAccessSettings
	err       error
	expiresAt time.Time
}

func normalizeDirectAccessSettings(settings DirectAccessSettings) (DirectAccessSettings, error) {
	if len(settings.Entries) > 256 {
		return DirectAccessSettings{}, infraerrors.BadRequest("INVALID_DIRECT_ACCESS_SETTINGS", "At most 256 IP addresses or CIDR ranges are allowed")
	}
	entries := make([]DirectAccessEntry, 0, len(settings.Entries))
	seen := make(map[string]bool)
	for index, entry := range settings.Entries {
		value := strings.TrimSpace(entry.CIDR)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil || address.Zone() != "" {
				return DirectAccessSettings{}, infraerrors.BadRequest("INVALID_DIRECT_ACCESS_SETTINGS", fmt.Sprintf("Entry %d: invalid IP address or CIDR range", index+1))
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if prefix.Addr().Is4In6() {
			return DirectAccessSettings{}, infraerrors.BadRequest("INVALID_DIRECT_ACCESS_SETTINGS", fmt.Sprintf("Entry %d: use an IPv4 CIDR range instead of an IPv4-mapped IPv6 range", index+1))
		}
		entry.CIDR = prefix.Masked().String()
		entry.Note = strings.TrimSpace(entry.Note)
		if utf8.RuneCountInString(entry.Note) > 100 {
			return DirectAccessSettings{}, infraerrors.BadRequest("INVALID_DIRECT_ACCESS_SETTINGS", fmt.Sprintf("Entry %d: note must be at most 100 characters", index+1))
		}
		if seen[entry.CIDR] {
			return DirectAccessSettings{}, infraerrors.BadRequest("INVALID_DIRECT_ACCESS_SETTINGS", fmt.Sprintf("Entry %d: duplicate IP address or CIDR range", index+1))
		}
		seen[entry.CIDR] = true
		entries = append(entries, entry)
	}
	return DirectAccessSettings{Entries: entries}, nil
}

func (settingsService *SettingService) DirectAccessHost() string {
	if settingsService.cfg == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(settingsService.cfg.Server.DirectAccessHost), "."))
}

func (settingsService *SettingService) GetDirectAccessSettings(ctx context.Context) (DirectAccessSettings, error) {
	value, err := settingsService.settingRepo.GetValue(ctx, SettingKeyDirectAccess)
	if errors.Is(err, ErrSettingNotFound) {
		return DirectAccessSettings{Entries: []DirectAccessEntry{}}, nil
	}
	if err != nil {
		return DirectAccessSettings{}, err
	}
	var settings DirectAccessSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DirectAccessSettings{}, fmt.Errorf("decode direct access settings: %w", err)
	}
	return normalizeDirectAccessSettings(settings)
}

func (settingsService *SettingService) SetDirectAccessSettings(ctx context.Context, settings DirectAccessSettings) (DirectAccessSettings, error) {
	normalized, err := normalizeDirectAccessSettings(settings)
	if err != nil {
		return DirectAccessSettings{}, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return DirectAccessSettings{}, err
	}
	settingsService.directAccessMu.Lock()
	defer settingsService.directAccessMu.Unlock()
	if err := settingsService.settingRepo.Set(ctx, SettingKeyDirectAccess, string(data)); err != nil {
		return DirectAccessSettings{}, err
	}
	settingsService.directAccessCache = &cachedDirectAccessSettings{settings: normalized, expiresAt: time.Now().Add(30 * time.Second)}
	return normalized, nil
}

func (settingsService *SettingService) IsDirectAccessAllowed(ctx context.Context, source string) (bool, error) {
	address, err := netip.ParseAddr(source)
	if err != nil || address.Zone() != "" {
		return false, nil
	}
	address = address.Unmap()
	settingsService.directAccessMu.Lock()
	defer settingsService.directAccessMu.Unlock()
	if settingsService.directAccessCache == nil || time.Now().After(settingsService.directAccessCache.expiresAt) {
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		settings, loadErr := settingsService.GetDirectAccessSettings(dbCtx)
		ttl := 30 * time.Second
		if loadErr != nil {
			ttl = 5 * time.Second
		}
		settingsService.directAccessCache = &cachedDirectAccessSettings{settings: settings, err: loadErr, expiresAt: time.Now().Add(ttl)}
	}
	if settingsService.directAccessCache.err != nil {
		return false, settingsService.directAccessCache.err
	}
	for _, entry := range settingsService.directAccessCache.settings.Entries {
		prefix, parseErr := netip.ParsePrefix(entry.CIDR)
		if parseErr == nil && prefix.Contains(address) {
			return true, nil
		}
	}
	return false, nil
}
