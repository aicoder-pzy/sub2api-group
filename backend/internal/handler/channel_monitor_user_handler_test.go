package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserMonitorResponseIncludesOnlyEachModelsIntelligence(t *testing.T) {
	view := &service.UserMonitorView{
		ID: 14, PrimaryModel: "sol", Intelligence: &service.MonitorIntelligence{State: "normal", Tests: []service.MonitorIntelligenceTest{{ID: 1, Kind: "pelican"}}},
		ExtraModels: []service.UserMonitorExtraModel{{
			ExtraModelStatus: service.ExtraModelStatus{Model: "astra"},
			Intelligence:     &service.MonitorIntelligence{State: "degraded", Tests: []service.MonitorIntelligenceTest{{ID: 2, Kind: "candy"}}},
		}},
	}
	encoded, err := json.Marshal(userMonitorViewToItem(view, false))
	require.NoError(t, err)
	var response struct {
		Intelligence service.MonitorIntelligence `json:"intelligence"`
		ExtraModels  []struct {
			Model        string                      `json:"model"`
			Intelligence service.MonitorIntelligence `json:"intelligence"`
		} `json:"extra_models"`
	}
	require.NoError(t, json.Unmarshal(encoded, &response))
	require.Equal(t, "pelican", response.Intelligence.Tests[0].Kind)
	require.Equal(t, "astra", response.ExtraModels[0].Model)
	require.Equal(t, "degraded", response.ExtraModels[0].Intelligence.State)
	require.Len(t, response.ExtraModels[0].Intelligence.Tests, 1)
	require.Equal(t, "candy", response.ExtraModels[0].Intelligence.Tests[0].Kind)
}
