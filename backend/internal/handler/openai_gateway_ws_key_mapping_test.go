package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesWebSocketKeyModelMapping(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		t.Run(mode, func(t *testing.T) {
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:    `{"type":"response.create","model":"my-alias","input":"first"}`,
				secondPayload:   `{"type":"response.create","model":"my-alias","input":"second"}`,
				keyModelMapping: map[string]string{"my-alias": "gpt-5.4", "gpt-5.4": "disallowed"},
				group:           wsAllowlistGroup(true, "gpt-5.4"), ingressMode: mode,
			})
			require.Len(t, got.upstreamPayloads, 2)
			for i, payload := range got.upstreamPayloads {
				require.Equal(t, "gpt-5.4", gjson.GetBytes(payload, "model").String())
				require.Equal(t, "gpt-5.4", got.logs[i].RequestedModel)
			}
		})
	}
}

func TestOpenAIResponsesWebSocketKeyMappingStillEnforcesGroup(t *testing.T) {
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:    `{"type":"response.create","model":"my-alias"}`,
		keyModelMapping: map[string]string{"my-alias": "gpt-4.1"},
		group:           wsAllowlistGroup(true, "gpt-5.4"), firstFrameCloseExpected: true,
	})
}
