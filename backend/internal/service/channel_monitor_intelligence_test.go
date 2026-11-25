package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type modelIntelligenceRepoStub struct {
	ChannelMonitorRepository
	monitors []*ChannelMonitor
	results  map[int64]map[string]*MonitorIntelligence
	err      error
	ids      []int64
}

func (r *modelIntelligenceRepoStub) ListEnabled(context.Context) ([]*ChannelMonitor, error) {
	return r.monitors, nil
}
func (r *modelIntelligenceRepoStub) ListMonitorIntelligence(_ context.Context, ids []int64) (map[int64]map[string]*MonitorIntelligence, error) {
	r.ids = ids
	return r.results, r.err
}
func (r *modelIntelligenceRepoStub) ListLatestForMonitorIDs(context.Context, []int64) (map[int64][]*ChannelMonitorLatest, error) {
	return nil, nil
}
func (r *modelIntelligenceRepoStub) ComputeAvailabilityForMonitors(context.Context, []int64, int) (map[int64][]*ChannelMonitorAvailability, error) {
	return nil, nil
}
func (r *modelIntelligenceRepoStub) ListRecentHistoryForMonitors(context.Context, map[int64][]string, int) (map[int64]map[string][]*ChannelMonitorHistoryEntry, error) {
	return nil, nil
}

func TestMonitorIntelligenceFollowsEachModelInUserViews(t *testing.T) {
	groupID := int64(45)
	sol := &MonitorIntelligence{State: "normal", Tests: []MonitorIntelligenceTest{{ID: 1, Kind: "candy"}, {ID: 2, Kind: "pelican"}}}
	astra := &MonitorIntelligence{State: "degraded", Tests: []MonitorIntelligenceTest{{ID: 3, Kind: "candy"}}}
	noPlan := &MonitorIntelligence{State: "no_plan", History: []MonitorIntelligencePoint{}}
	repo := &modelIntelligenceRepoStub{
		monitors: []*ChannelMonitor{{ID: 14, PrimaryModel: "sol", ExtraModels: []string{"astra", "no-tests", "missing"}, IntelligenceGroupID: &groupID}},
		results:  map[int64]map[string]*MonitorIntelligence{14: {"sol": sol, "astra": astra, "no-tests": noPlan}},
	}
	svc := NewChannelMonitorService(repo, nil)
	views, err := svc.ListUserView(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{14}, repo.ids)
	require.Same(t, sol, views[0].Intelligence)
	require.Same(t, astra, views[0].ExtraModels[0].Intelligence)
	require.Same(t, noPlan, views[0].ExtraModels[1].Intelligence)
	require.Equal(t, "unknown", views[0].ExtraModels[2].Intelligence.State)
	require.Empty(t, views[0].ExtraModels[2].Intelligence.Tests)
	passive, err := svc.ListUserIntelligence(context.Background())
	require.NoError(t, err)
	require.Equal(t, "sol", passive[0].PrimaryModel)
	require.Same(t, sol, passive[0].Intelligence)
}

func TestMonitorIntelligenceUnavailableDoesNotBorrowPrimaryResults(t *testing.T) {
	groupID := int64(45)
	repo := &modelIntelligenceRepoStub{err: errors.New("database unavailable")}
	svc := NewChannelMonitorService(repo, nil)
	results := svc.batchMonitorIntelligence(context.Background(), []*ChannelMonitor{
		{ID: 14, PrimaryModel: "sol", ExtraModels: []string{"astra"}, IntelligenceGroupID: &groupID},
		{ID: 16, PrimaryModel: "sol", ExtraModels: []string{"astra"}},
		{ID: 18, PrimaryModel: "quota", CheckMode: MonitorCheckModeQuota},
	})
	require.Equal(t, []int64{14}, repo.ids)
	for _, model := range []string{"sol", "astra"} {
		require.Equal(t, "unknown", results[14][model].State)
		require.Empty(t, results[14][model].History)
		require.Equal(t, "unconfigured", results[16][model].State)
	}
	require.Nil(t, results[18])
}
