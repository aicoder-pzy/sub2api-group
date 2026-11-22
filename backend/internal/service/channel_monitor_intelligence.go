package service

import (
	"context"
	"log/slog"
	"time"
)

type MonitorIntelligence struct {
	State   string                     `json:"state"`
	History []MonitorIntelligencePoint `json:"history"`
}

type MonitorIntelligencePoint struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMs int       `json:"latency_ms"`
	Effort    string    `json:"effort,omitempty"`
}

func monitorIntelligenceGroup(id *int64) *int64 {
	if id != nil && *id > 0 {
		return id
	}
	return nil
}

func (s *ChannelMonitorService) batchMonitorIntelligence(ctx context.Context, monitors []*ChannelMonitor) map[int64]*MonitorIntelligence {
	summaries := make(map[int64]*MonitorIntelligence)
	ids := make([]int64, 0, len(monitors))
	for _, monitor := range monitors {
		if monitor.CheckMode == MonitorCheckModeQuota {
			continue
		}
		if monitor.IntelligenceGroupID == nil {
			summaries[monitor.ID] = &MonitorIntelligence{State: "unconfigured", History: []MonitorIntelligencePoint{}}
		} else {
			ids = append(ids, monitor.ID)
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
		if row := rows[id]; row != nil {
			summaries[id] = row
		} else {
			summaries[id] = &MonitorIntelligence{State: "unknown", History: []MonitorIntelligencePoint{}}
		}
	}
	return summaries
}
