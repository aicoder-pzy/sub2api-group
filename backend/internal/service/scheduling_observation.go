package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// SchedulingObservationScope keeps business evidence on the public model and
// actual ingress. Quality tests continue to use the supplied routing model.
func SchedulingObservationScope(ctx context.Context, routingModel string) (publicModel, endpoint string, smart bool) {
	publicModel = routingModel
	if !smartChannelEnabled(ctx) {
		return publicModel, "", false
	}
	if requested, ok := RequestedPublicModelFromContext(ctx); ok {
		publicModel = requested
	}
	endpoint, _ = ctx.Value(ctxkey.InboundEndpoint).(string)
	return publicModel, strings.TrimSpace(endpoint), true
}

type smartChannelObservationRepository interface {
	GetSmartChannelAccountQuality(context.Context, int64, string, string) (map[int64]GroupModelAccountQuality, error)
}
