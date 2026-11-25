package service

import (
	"context"
	"log/slog"
	"time"
)

type MonitorIntelligence struct {
	State   string                     `json:"state"`
	History []MonitorIntelligencePoint `json:"history"`
	Tests   []MonitorIntelligenceTest  `json:"tests,omitempty"`
}

type MonitorIntelligenceTest struct {
	ID      int64                      `json:"id"`
	Kind    string                     `json:"kind"`
	Name    string                     `json:"name,omitempty"`
	State   string                     `json:"state"`
	History []MonitorIntelligencePoint `json:"history"`
}

type MonitorIntelligenceView struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	PrimaryModel string               `json:"primary_model"`
	Intelligence *MonitorIntelligence `json:"intelligence"`
}

// ListUserIntelligence serves the passive dashboard without loading probe or quota history.
func (s *ChannelMonitorService) ListUserIntelligence(ctx context.Context) ([]MonitorIntelligenceView, error) {
	monitors, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	configured := make([]*ChannelMonitor, 0, len(monitors))
	for _, monitor := range monitors {
		if monitor.CheckMode != MonitorCheckModeQuota && monitor.IntelligenceGroupID != nil {
			configured = append(configured, monitor)
		}
	}
	intelligence := s.batchMonitorIntelligence(ctx, configured)
	items := make([]MonitorIntelligenceView, 0, len(configured))
	for _, monitor := range configured {
		items = append(items, MonitorIntelligenceView{
			ID: monitor.ID, Name: monitor.Name, PrimaryModel: monitor.PrimaryModel, Intelligence: intelligence[monitor.ID][monitor.PrimaryModel],
		})
	}
	return items, nil
}

type MonitorIntelligencePoint struct {
	Status              string    `json:"status"`
	CheckedAt           time.Time `json:"checked_at"`
	LatencyMs           int       `json:"latency_ms"`
	Effort              string    `json:"effort,omitempty"`
	Prompt              string    `json:"prompt,omitempty"`
	PromptTruncated     bool      `json:"prompt_truncated,omitempty"`
	Answer              string    `json:"answer,omitempty"`
	ExpectedAnswer      string    `json:"expected_answer,omitempty"`
	AnswerTruncated     bool      `json:"answer_truncated,omitempty"`
	ConfirmationPending bool      `json:"confirmation_pending,omitempty"`
}

func monitorIntelligenceGroup(id *int64) *int64 {
	if id != nil && *id > 0 {
		return id
	}
	return nil
}

func (s *ChannelMonitorService) batchMonitorIntelligence(ctx context.Context, monitors []*ChannelMonitor) map[int64]map[string]*MonitorIntelligence {
	summaries := make(map[int64]map[string]*MonitorIntelligence)
	ids := make([]int64, 0, len(monitors))
	for _, monitor := range monitors {
		if monitor.CheckMode == MonitorCheckModeQuota {
			continue
		}
		state := "unknown"
		if monitor.IntelligenceGroupID == nil {
			state = "unconfigured"
		} else {
			ids = append(ids, monitor.ID)
		}
		summaries[monitor.ID] = make(map[string]*MonitorIntelligence)
		for _, model := range normalizeModels(append([]string{monitor.PrimaryModel}, monitor.ExtraModels...)) {
			summaries[monitor.ID][model] = &MonitorIntelligence{State: state, History: []MonitorIntelligencePoint{}}
		}
	}
	if len(ids) == 0 {
		return summaries
	}
	rows, err := s.repo.ListMonitorIntelligence(ctx, ids)
	if err != nil {
		slog.Warn("channel_monitor: unable to load intelligence results", "error", err)
	}
	for _, id := range ids {
		for model := range summaries[id] {
			if row := rows[id][model]; row != nil {
				summaries[id][model] = row
			}
		}
	}
	return summaries
}
