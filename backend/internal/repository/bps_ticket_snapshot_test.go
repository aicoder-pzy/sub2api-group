package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBPSTicketSettingsSurviveSchedulerProjection(t *testing.T) {
	extra := map[string]any{"openai_bps_ticket": map[string]any{"bps": true, "models": []string{"gpt-6-astra"}}, "openai_bps_ticket_state": map[string]any{"revision": "current", "models": map[string]any{"gpt-6-astra": map[string]any{"auto_bps": true}}}, "unrelated_admin_data": "omitted"}
	projected := filterSchedulerExtra(extra)
	require.Equal(t, extra["openai_bps_ticket"], projected["openai_bps_ticket"])
	require.Equal(t, extra["openai_bps_ticket_state"], projected["openai_bps_ticket_state"])
	require.NotContains(t, projected, "unrelated_admin_data")
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{"openai_bps_ticket": extra["openai_bps_ticket"]}))
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{"openai_bps_ticket_state": extra["openai_bps_ticket_state"]}))
}
