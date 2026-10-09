package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const upstreamBalanceNotificationsKey = "upstream_balance_notifications"

var errBalanceNotificationUnknown = errors.New("balance notification observation unavailable")

type UpstreamBalanceNotificationChannel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Provider         string `json:"provider"`
	Enabled          bool   `json:"enabled"`
	Email            string `json:"email,omitempty"`
	URL              string `json:"url,omitempty"`
	Secret           string `json:"secret,omitempty"`
	ClearSecret      bool   `json:"clear_secret,omitempty"`
	URLConfigured    bool   `json:"url_configured"`
	SecretConfigured bool   `json:"secret_configured"`
	urlCipher        string
	secretCipher     string
	revision         string
}

type UpstreamBalanceNotificationSettings struct {
	Enabled                 bool                                 `json:"enabled"`
	NotifyRecovery          bool                                 `json:"notify_recovery"`
	EncryptionKeyConfigured bool                                 `json:"encryption_key_configured"`
	Channels                []UpstreamBalanceNotificationChannel `json:"channels"`
}

type storedBalanceNotificationChannel struct {
	UpstreamBalanceNotificationChannel
	URLCipher    string `json:"url_cipher,omitempty"`
	SecretCipher string `json:"secret_cipher,omitempty"`
	Revision     string `json:"revision"`
}

type storedBalanceNotificationSettings struct {
	Enabled        bool                               `json:"enabled"`
	NotifyRecovery bool                               `json:"notify_recovery"`
	Channels       []storedBalanceNotificationChannel `json:"channels"`
}

type UpstreamBalanceNotificationAccount struct {
	ID    int64
	Name  string
	State UpstreamBalanceState
}

type UpstreamBalanceNotificationObservation struct {
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	Scope       string    `json:"scope"`
	Currency    string    `json:"currency"`
	Remaining   float64   `json:"remaining"`
	Threshold   float64   `json:"threshold"`
	Criteria    string    `json:"-"`
	ObservedAt  time.Time `json:"observed_at"`
	FreshUntil  time.Time `json:"fresh_until"`
}

type UpstreamBalanceNotificationDelivery struct {
	Name     string     `json:"name"`
	Provider string     `json:"provider"`
	Status   string     `json:"status"`
	Attempts int        `json:"attempts"`
	SentAt   *time.Time `json:"sent_at,omitempty"`
}

type UpstreamBalanceNotificationEvent struct {
	UpstreamBalanceNotificationObservation
	ID         string                                         `json:"id"`
	EpisodeID  string                                         `json:"-"`
	Phase      string                                         `json:"phase"`
	Status     string                                         `json:"status"`
	CreatedAt  time.Time                                      `json:"created_at"`
	Deliveries map[string]UpstreamBalanceNotificationDelivery `json:"deliveries"`
	Attempts   int                                            `json:"attempts"`
	Lease      string                                         `json:"-"`
}

type UpstreamBalanceNotificationRepository interface {
	ListAccounts(context.Context) ([]UpstreamBalanceNotificationAccount, error)
	Observe(context.Context, UpstreamBalanceNotificationObservation, bool) error
	Claim(context.Context, time.Time, int) ([]UpstreamBalanceNotificationEvent, error)
	SaveDelivery(context.Context, *UpstreamBalanceNotificationEvent, string, UpstreamBalanceNotificationDelivery) error
	Complete(context.Context, *UpstreamBalanceNotificationEvent, string) error
	History(context.Context, int) ([]UpstreamBalanceNotificationEvent, error)
}

func balanceNotificationConfigError() error {
	return infraerrors.BadRequest("INVALID_BALANCE_NOTIFICATIONS", "Invalid balance notification configuration")
}

func (s *UpstreamBillingProbeService) loadBalanceNotifications(ctx context.Context) (*UpstreamBalanceNotificationSettings, error) {
	result := &UpstreamBalanceNotificationSettings{NotifyRecovery: true, EncryptionKeyConfigured: s.newAPIFixedKey, Channels: []UpstreamBalanceNotificationChannel{}}
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return result, nil
	}
	raw, err := s.settingService.settingRepo.GetValue(ctx, upstreamBalanceNotificationsKey)
	if errors.Is(err, ErrSettingNotFound) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	stored := storedBalanceNotificationSettings{NotifyRecovery: true}
	if err = json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}
	result.Enabled, result.NotifyRecovery = stored.Enabled, stored.NotifyRecovery
	for _, channel := range stored.Channels {
		item := channel.UpstreamBalanceNotificationChannel
		item.URL, item.Secret, item.ClearSecret = "", "", false
		item.urlCipher, item.secretCipher, item.revision = channel.URLCipher, channel.SecretCipher, channel.Revision
		item.URLConfigured, item.SecretConfigured = item.urlCipher != "", item.secretCipher != ""
		result.Channels = append(result.Channels, item)
	}
	return result, nil
}

func (s *UpstreamBillingProbeService) GetBalanceNotificationSettings(ctx context.Context) (*UpstreamBalanceNotificationSettings, error) {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	return s.loadBalanceNotifications(ctx)
}

func (s *UpstreamBillingProbeService) SetBalanceNotificationSettings(ctx context.Context, settings UpstreamBalanceNotificationSettings) (*UpstreamBalanceNotificationSettings, error) {
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if len(settings.Channels) > 6 {
		return nil, balanceNotificationConfigError()
	}
	previous, err := s.loadBalanceNotifications(ctx)
	if err != nil {
		return nil, err
	}
	old := map[string]UpstreamBalanceNotificationChannel{}
	for _, channel := range previous.Channels {
		old[channel.ID] = channel
	}
	stored := storedBalanceNotificationSettings{Enabled: settings.Enabled, NotifyRecovery: settings.NotifyRecovery, Channels: []storedBalanceNotificationChannel{}}
	seen := map[string]bool{}
	active := false
	for _, channel := range settings.Channels {
		if channel.ID == "" {
			channel.ID = uuid.NewString()
		}
		if _, err := uuid.Parse(channel.ID); err != nil || seen[channel.ID] || len(channel.Name) > 100 {
			return nil, balanceNotificationConfigError()
		}
		seen[channel.ID] = true
		channel.Name = strings.TrimSpace(channel.Name)
		item := storedBalanceNotificationChannel{UpstreamBalanceNotificationChannel: channel, Revision: uuid.NewString()}
		prev, exists := old[channel.ID]
		if exists && prev.Provider == channel.Provider {
			item.URLCipher, item.SecretCipher = prev.urlCipher, prev.secretCipher
		}
		switch channel.Provider {
		case "email":
			email, err := mail.ParseAddress(channel.Email)
			if err != nil || email.Address != channel.Email || len(channel.Email) > 254 || strings.ContainsAny(channel.Email, "\r\n") {
				return nil, balanceNotificationConfigError()
			}
			item.URLCipher, item.SecretCipher = "", ""
		case "wecom", "dingtalk", "feishu", "webhook":
			if channel.Provider == "wecom" && channel.Secret != "" {
				return nil, balanceNotificationConfigError()
			}
			if !s.newAPIFixedKey || s.newAPIEncryptor == nil {
				return nil, ErrNewAPIEncryptionKey
			}
			if len(channel.URL) > 4096 || len(channel.Secret) > 8192 || strings.ContainsAny(channel.Secret, "\r\n") {
				return nil, balanceNotificationConfigError()
			}
			if channel.URL != "" {
				if validateBalanceNotificationURL(channel.Provider, channel.URL) != nil {
					return nil, balanceNotificationConfigError()
				}
				item.URLCipher, err = s.newAPIEncryptor.Encrypt(channel.URL)
				if err != nil {
					return nil, errors.New("notification encryption failed")
				}
			}
			if item.URLCipher == "" {
				return nil, balanceNotificationConfigError()
			}
			if channel.ClearSecret {
				item.SecretCipher = ""
			}
			if channel.Secret != "" {
				item.SecretCipher, err = s.newAPIEncryptor.Encrypt(channel.Secret)
				if err != nil {
					return nil, errors.New("notification encryption failed")
				}
			}
			item.Email = ""
		default:
			return nil, balanceNotificationConfigError()
		}
		item.URL, item.Secret, item.ClearSecret = "", "", false
		item.URLConfigured, item.SecretConfigured = false, false
		// An unchanged destination keeps its receipts valid when other settings change.
		if exists && prev.Provider == channel.Provider && prev.Email == item.Email && channel.URL == "" && channel.Secret == "" && !channel.ClearSecret {
			item.Revision = prev.revision
		}
		active = active || item.Enabled
		stored.Channels = append(stored.Channels, item)
	}
	if settings.Enabled && !active {
		return nil, balanceNotificationConfigError()
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if err = s.settingService.settingRepo.Set(ctx, upstreamBalanceNotificationsKey, string(data)); err != nil {
		return nil, err
	}
	return s.loadBalanceNotifications(ctx)
}

func balanceNotificationObservations(account UpstreamBalanceNotificationAccount, threshold float64, now time.Time) []UpstreamBalanceNotificationObservation {
	state := account.State
	snapshot := state.Snapshot
	if !state.Enabled || state.Validate() != nil || state.NotificationEnabled != nil && !*state.NotificationEnabled || snapshot == nil || snapshot.Status != UpstreamBillingProbeStatusOK || snapshot.ReceivedAt == nil || snapshot.FreshUntil == nil || snapshot.ReceivedAt.After(now) || !snapshot.FreshUntil.After(*snapshot.ReceivedAt) || !snapshot.FreshUntil.After(now) {
		return nil
	}
	if state.NotificationThreshold != nil {
		threshold = *state.NotificationThreshold
	}
	out := []UpstreamBalanceNotificationObservation{}
	for _, amount := range snapshot.Amounts {
		if amount.Unlimited || amount.Remaining == nil || !newAPIFinite(*amount.Remaining) || !upstreamBalanceCurrencyPattern.MatchString(amount.Currency) || amount.ResetAt != nil && !amount.ResetAt.After(now) {
			continue
		}
		identity, _ := json.Marshal([]any{snapshot.SourceIdentity, state.Provider, state.QuotaPerUnit, state.Currency, amount.Scope, amount.Currency, threshold})
		sum := sha256.Sum256(identity)
		until := *snapshot.FreshUntil
		if amount.ResetAt != nil && amount.ResetAt.Before(until) {
			until = *amount.ResetAt
		}
		out = append(out, UpstreamBalanceNotificationObservation{AccountID: account.ID, AccountName: account.Name, Scope: amount.Scope, Currency: amount.Currency, Remaining: *amount.Remaining, Threshold: threshold, Criteria: hex.EncodeToString(sum[:]), ObservedAt: *snapshot.ReceivedAt, FreshUntil: until})
	}
	return out
}

func (s *UpstreamBillingProbeService) RunBalanceNotifications(ctx context.Context) error {
	if s.balanceNotificationRepo == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	release, acquired, err := s.tryAcquireLeaderLock(ctx, "upstream:balance:notifications:leader")
	if err != nil || !acquired {
		return err
	}
	defer release()
	config, err := s.GetBalanceNotificationSettings(ctx)
	if err != nil || !config.Enabled {
		return err
	}
	balanceSettings, err := s.GetBalanceSettings(ctx)
	if err != nil || !balanceSettings.Enabled {
		return err
	}
	accounts, err := s.balanceNotificationRepo.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		for _, observation := range balanceNotificationObservations(account, balanceSettings.LowBalanceThreshold, s.currentTime()) {
			if err := s.balanceNotificationRepo.Observe(ctx, observation, config.NotifyRecovery); err != nil {
				return err
			}
		}
	}
	for i := 0; i < 5; i++ {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 75*time.Second {
			break
		}
		events, err := s.balanceNotificationRepo.Claim(ctx, s.currentTime(), 1)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			break
		}
		if err := s.deliverBalanceNotification(ctx, &events[0]); err != nil {
			return err
		}
	}
	return nil
}

func (s *UpstreamBillingProbeService) balanceNotificationStillValid(ctx context.Context, event *UpstreamBalanceNotificationEvent, config *UpstreamBalanceNotificationSettings) (bool, error) {
	if !config.Enabled || event.Phase == "recovery" && !config.NotifyRecovery {
		return false, nil
	}
	account, err := s.accountRepo.GetByID(ctx, event.AccountID)
	if errors.Is(err, ErrAccountNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errBalanceNotificationUnknown
	}
	if !account.IsActive() || account.Type != AccountTypeAPIKey {
		return false, nil
	}
	settings, err := s.GetBalanceSettings(ctx)
	if err != nil {
		return false, errBalanceNotificationUnknown
	}
	if !settings.Enabled {
		return false, nil
	}
	state := DecodeUpstreamBalanceState(account.Extra)
	if !state.Enabled || state.NotificationEnabled != nil && !*state.NotificationEnabled {
		return false, nil
	}
	if state.Snapshot == nil || state.Snapshot.Status != UpstreamBillingProbeStatusOK || state.Snapshot.FreshUntil == nil || !state.Snapshot.FreshUntil.After(s.currentTime()) {
		return false, errBalanceNotificationUnknown
	}
	observations := balanceNotificationObservations(UpstreamBalanceNotificationAccount{ID: account.ID, Name: account.Name, State: DecodeUpstreamBalanceState(account.Extra)}, settings.LowBalanceThreshold, s.currentTime())
	for _, observation := range observations {
		if observation.Scope == event.Scope && observation.Currency == event.Currency && observation.Criteria == event.Criteria {
			return (observation.Remaining <= observation.Threshold) == (event.Phase == "low"), nil
		}
	}
	return false, nil
}

func (s *UpstreamBillingProbeService) deliverBalanceNotification(ctx context.Context, event *UpstreamBalanceNotificationEvent) error {
	status := "sent"
	if event.Deliveries == nil {
		event.Deliveries = map[string]UpstreamBalanceNotificationDelivery{}
	}
	config, err := s.GetBalanceNotificationSettings(ctx)
	if err != nil {
		return err
	}
	if valid, err := s.balanceNotificationStillValid(ctx, event, config); err != nil {
		return s.balanceNotificationRepo.Complete(ctx, event, "pending")
	} else if !valid {
		return s.balanceNotificationRepo.Complete(ctx, event, "suppressed")
	}
	anyDestination := false
	for _, channel := range config.Channels {
		if !channel.Enabled {
			continue
		}
		anyDestination = true
		key := channel.ID + ":" + channel.revision
		delivery := event.Deliveries[key]
		if delivery.Status == "sent" {
			continue
		}
		if delivery.Attempts >= 3 {
			status = "failed"
			continue
		}
		latest, err := s.GetBalanceNotificationSettings(ctx)
		if err != nil {
			return err
		}
		if valid, err := s.balanceNotificationStillValid(ctx, event, latest); err != nil {
			return s.balanceNotificationRepo.Complete(ctx, event, "pending")
		} else if !valid {
			return s.balanceNotificationRepo.Complete(ctx, event, "suppressed")
		}
		found := false
		for _, current := range latest.Channels {
			if current.ID == channel.ID && current.revision == channel.revision && current.Enabled {
				channel, found = current, true
				break
			}
		}
		if !found {
			status = "failed"
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		err = s.sendBalanceNotification(sendCtx, channel, event)
		cancel()
		if errors.Is(err, errBalanceNotificationUnknown) {
			return s.balanceNotificationRepo.Complete(ctx, event, "pending")
		}
		delivery.Name, delivery.Provider, delivery.Attempts = channel.Name, channel.Provider, delivery.Attempts+1
		delivery.Status = "sent"
		if err != nil {
			delivery.Status, status = "failed", "failed"
		} else {
			delivery.SentAt = probeTimePtr(s.currentTime())
		}
		if err := s.balanceNotificationRepo.SaveDelivery(ctx, event, key, delivery); err != nil {
			return err
		}
		event.Deliveries[key] = delivery
	}
	if !anyDestination {
		status = "suppressed"
	}
	return s.balanceNotificationRepo.Complete(ctx, event, status)
}

func (s *UpstreamBillingProbeService) TestBalanceNotification(ctx context.Context, id string) error {
	config, err := s.GetBalanceNotificationSettings(ctx)
	if err != nil {
		return err
	}
	for _, channel := range config.Channels {
		if channel.ID == id {
			now := s.currentTime()
			event := &UpstreamBalanceNotificationEvent{Phase: "test", UpstreamBalanceNotificationObservation: UpstreamBalanceNotificationObservation{AccountName: "Test", Scope: "wallet", Currency: "USD", Remaining: 10, Threshold: 5, ObservedAt: now}}
			if err := s.sendBalanceNotification(ctx, channel, event); err != nil {
				return infraerrors.BadRequest("BALANCE_NOTIFICATION_SEND_FAILED", "Balance notification delivery failed")
			}
			return nil
		}
	}
	return balanceNotificationConfigError()
}

func (s *UpstreamBillingProbeService) GetBalanceNotificationHistory(ctx context.Context) ([]UpstreamBalanceNotificationEvent, error) {
	if s.balanceNotificationRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	return s.balanceNotificationRepo.History(ctx, 50)
}

func balanceNotificationSubject(event *UpstreamBalanceNotificationEvent) string {
	phase := "Low balance"
	if event.Phase == "recovery" {
		phase = "Balance recovered"
	}
	if event.Phase == "test" {
		phase = "Notification test"
	}
	return fmt.Sprintf("[Sub2API] %s: %s (#%d)", phase, strings.Join(strings.Fields(event.AccountName), " "), event.AccountID)
}

func balanceNotificationThresholdValid(value *float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1e9
}
