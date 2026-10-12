package service

import (
	"context"
	"testing"
	"time"
)

func TestChannelStatisticsWindow(t *testing.T) {
	now := time.Date(2026, 10, 12, 16, 30, 0, 0, time.UTC) // Oct 13, 00:30 Beijing
	start, end, err := (ChannelStatisticsQuery{}).Window(now)
	if err != nil || start.Format(time.RFC3339) != "2026-10-13T00:00:00+08:00" || !end.Equal(now) {
		t.Fatalf("default window = %v, %v, %v", start, end, err)
	}
	start, end, err = (ChannelStatisticsQuery{StartDate: "2026-10-07", EndDate: "2026-10-13"}).Window(now)
	if err != nil || start.Format(time.RFC3339) != "2026-10-07T00:00:00+08:00" || !end.Equal(now) {
		t.Fatalf("seven calendar days = %v, %v, %v", start, end, err)
	}
	_, end, err = (ChannelStatisticsQuery{StartDate: "2026-10-12"}).Window(now)
	if err != nil || end.Format(time.RFC3339) != "2026-10-13T00:00:00+08:00" {
		t.Fatalf("single previous day = %v, %v", end, err)
	}
	for _, q := range []ChannelStatisticsQuery{
		{StartDate: "2026-10-06", EndDate: "2026-10-13"},
		{StartDate: "2026-10-14"}, {StartDate: "2026-10-12", EndDate: "2026-10-11"},
		{StartDate: "2026-02-30"}, {GroupID: -1}, {Effort: "invalid"},
	} {
		if _, _, err := q.Window(now); err == nil {
			t.Fatalf("accepted invalid query %+v", q)
		}
	}
}

type channelStatisticsStub struct{ UsageLogRepository }

func (channelStatisticsStub) GetChannelStatistics(_ context.Context, _ ChannelStatisticsQuery, start, _ time.Time) (*ChannelStatistics, error) {
	return &ChannelStatistics{Accounts: []ChannelStatisticsRow{}, Daily: []ChannelStatisticsRow{{Day: start.Format(time.DateOnly), Requests: 2}}}, nil
}

func TestChannelStatisticsFillsEmptyDays(t *testing.T) {
	svc := &UsageService{usageRepo: channelStatisticsStub{}}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Now().In(loc)
	got, err := svc.GetChannelStatistics(context.Background(), ChannelStatisticsQuery{
		StartDate: now.AddDate(0, 0, -2).Format(time.DateOnly), EndDate: now.Format(time.DateOnly),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "Asia/Shanghai" || len(got.Daily) != 3 || got.Daily[0].Requests != 2 || got.Daily[1].Requests != 0 || got.Daily[1].TTFTP50MS != nil {
		t.Fatalf("unexpected daily statistics: %+v", got)
	}
}
