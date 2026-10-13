package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

type SchedulingNotification struct {
	GroupModelSchedulingTransition
	ID                  string                                         `json:"id"`
	GroupID             int64                                          `json:"group_id"`
	GroupName           string                                         `json:"group_name"`
	Model               string                                         `json:"model"`
	Trigger             string                                         `json:"trigger"`
	PreviousAccountName string                                         `json:"previous_account_name"`
	AccountName         string                                         `json:"account_name"`
	Status              string                                         `json:"status"`
	Deliveries          map[string]UpstreamBalanceNotificationDelivery `json:"deliveries"`
}

// The binding cache atomically queues a notice only for a committed account change.
type SchedulingNotificationQueue interface {
	NextSchedulingNotification(context.Context, time.Time) (*SchedulingNotification, error)
	SaveSchedulingNotification(context.Context, *SchedulingNotification, *time.Time) error
	SchedulingNotificationHistory(context.Context, int) ([]SchedulingNotification, error)
}

func NewSchedulingNotification(ctx context.Context, groupID int64, model string, transition GroupModelSchedulingTransition) SchedulingNotification {
	event := SchedulingNotification{GroupModelSchedulingTransition: transition, ID: uuid.NewString(), GroupID: groupID, Model: model, Status: "pending", Trigger: "调度策略重新选择账号"}
	if group, _ := ctx.Value(ctxkey.Group).(*Group); group != nil && group.ID == groupID {
		event.GroupName = group.Name
	}
	if transition.Reason == "manual_refresh" {
		event.Trigger = "手动刷新调度"
	} else if transition.Reason == "model_auto_upgrade" {
		event.Trigger = "模型自动优选：候选持续具备优势，连续三次评估通过，主账号保持间隔已满 30 分钟"
	} else if state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState); state != nil {
		state.mu.Lock()
		snapshot := state.snapshots[groupModelSchedulingScope{groupID, model}]
		reason := state.switchReasons[snapshot.accountID]
		state.mu.Unlock()
		if reason != "" {
			event.Trigger = reason
		}
	}
	return event
}

func recordSchedulingSwitchFailure(ctx context.Context, accountID int64, err error) {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	var failure *UpstreamFailoverError
	if state == nil || !errors.As(err, &failure) || failure.StatusCode < 400 {
		return
	}
	reason := fmt.Sprintf("原账号请求失败（HTTP %d）", failure.StatusCode)
	if failure.Reason == accountRateLimitExceeded {
		reason = "原账号倍率超过分组上限"
	}
	if failure.Reason == accountRateLimitUnknown {
		reason = "原账号当前倍率无法确认"
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.switchReasons == nil {
		state.switchReasons = make(map[int64]string)
	}
	state.switchReasons[accountID] = reason
}

func recordSchedulingSelectionReason(ctx context.Context, accountID int64, reason string) {
	state, _ := ctx.Value(groupModelSchedulingRequestKey{}).(*groupModelSchedulingRequestState)
	if state == nil || accountID <= 0 {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.switchReasons == nil {
		state.switchReasons = make(map[int64]string)
	}
	// Preserve a concrete request failure over a later ranking explanation.
	if state.switchReasons[accountID] == "" {
		state.switchReasons[accountID] = reason
	}
}

func (s *UpstreamBillingProbeService) runSchedulingNotificationLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.RunSchedulingNotifications(s.parentCtx); err != nil && s.parentCtx.Err() == nil {
			logger.LegacyPrintf("service.scheduling_notifications", "delivery_failed: err=%v", err)
		}
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *UpstreamBillingProbeService) RunSchedulingNotifications(ctx context.Context) error {
	if s.schedulingNotifications == nil {
		return nil
	}
	// Shorter than the shared two-minute leader lease, even with six destinations.
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	release, acquired, err := s.tryAcquireLeaderLock(ctx, "scheduling:notifications:leader")
	if err != nil || !acquired {
		return err
	}
	defer release()
	event, err := s.schedulingNotifications.NextSchedulingNotification(ctx, s.currentTime())
	if err != nil || event == nil {
		return err
	}
	return s.deliverSchedulingNotification(ctx, event)
}

func (s *UpstreamBillingProbeService) deliverSchedulingNotification(ctx context.Context, event *SchedulingNotification) error {
	config, err := s.GetBalanceNotificationSettings(ctx)
	if err != nil {
		return err
	}
	if !config.NotifyAccountSwitch || s.currentTime().Sub(event.ChangedAt) > 24*time.Hour {
		event.Status = "suppressed"
		return s.schedulingNotifications.SaveSchedulingNotification(ctx, event, nil)
	}
	if s.accountRepo != nil {
		for id, name := range map[int64]*string{event.PreviousAccountID: &event.PreviousAccountName, event.AccountID: &event.AccountName} {
			if *name == "" {
				if account, err := s.accountRepo.GetByID(ctx, id); err == nil && account != nil {
					*name = account.Name
				}
			}
		}
	}
	clean := func(value string) string { return strings.Join(strings.Fields(value), " ") }
	subject := fmt.Sprintf("[Sub2API] 账号切换：%s / %s", clean(event.GroupName), clean(event.Model))
	text := fmt.Sprintf("%s\n分组：%s (#%d)\n模型：%s\n原账号：%s (#%d)\n新账号：%s (#%d)\n原因：%s\n切换时间：%s（北京时间）\n事件编号：%s", subject, clean(event.GroupName), event.GroupID, clean(event.Model), clean(event.PreviousAccountName), event.PreviousAccountID, clean(event.AccountName), event.AccountID, clean(event.Trigger), event.ChangedAt.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05"), event.ID)
	if event.Deliveries == nil {
		event.Deliveries = make(map[string]UpstreamBalanceNotificationDelivery)
	}
	event.Status = "sent"
	anyDestination, retry := false, false
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
			event.Status = "failed"
			continue
		}
		payload := map[string]any{"event": "account_switch", "event_id": event.ID, "text": text, "group_id": event.GroupID, "group_name": event.GroupName, "model": event.Model, "previous_account_id": event.PreviousAccountID, "account_id": event.AccountID, "previous_account_name": event.PreviousAccountName, "account_name": event.AccountName, "reason": event.Trigger, "changed_at": event.ChangedAt}
		err := s.sendAdminChannelNotification(ctx, channel, subject, text, payload, false, func(_ context.Context, latest *UpstreamBalanceNotificationSettings) error {
			if !latest.NotifyAccountSwitch {
				return errors.New("account switch notifications disabled")
			}
			return nil
		})
		delivery.Name, delivery.Provider, delivery.Attempts = channel.Name, channel.Provider, delivery.Attempts+1
		delivery.Status = "sent"
		if err != nil {
			delivery.Status, event.Status = "failed", "failed"
			retry = retry || delivery.Attempts < 3
			logger.LegacyPrintf("service.scheduling_notifications", "send_failed: event=%s channel=%s attempt=%d", event.ID, channel.ID, delivery.Attempts)
		} else {
			delivery.SentAt = probeTimePtr(s.currentTime())
		}
		event.Deliveries[key] = delivery
		// Save each receipt before the next destination so retries skip successful sends.
		next := s.currentTime().Add(time.Minute)
		if err := s.schedulingNotifications.SaveSchedulingNotification(ctx, event, &next); err != nil {
			return err
		}
	}
	if !anyDestination {
		event.Status = "suppressed"
	}
	var next *time.Time
	if retry {
		at := s.currentTime().Add(time.Minute)
		next = &at
		event.Status = "pending"
	}
	if err := s.schedulingNotifications.SaveSchedulingNotification(ctx, event, next); err != nil {
		return err
	}
	logger.LegacyPrintf("service.scheduling_notifications", "delivery_recorded: event=%s group=%d model=%s previous=%d account=%d status=%s", event.ID, event.GroupID, event.Model, event.PreviousAccountID, event.AccountID, event.Status)
	return nil
}

func (s *UpstreamBillingProbeService) GetSchedulingNotificationHistory(ctx context.Context) ([]SchedulingNotification, error) {
	if s.schedulingNotifications == nil {
		return []SchedulingNotification{}, nil
	}
	return s.schedulingNotifications.SchedulingNotificationHistory(ctx, 50)
}
