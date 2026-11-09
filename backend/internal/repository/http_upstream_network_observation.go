package repository

import (
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func doObservedUpstreamRequest(client *http.Client, req *http.Request, accountID int64) (*http.Response, error) {
	started := time.Now()
	var connected, written, firstByte atomic.Int64
	connected.Store(-1)
	written.Store(-1)
	firstByte.Store(-1)
	var reused atomic.Bool
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			connected.Store(time.Since(started).Milliseconds())
			reused.Store(info.Reused)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				written.Store(time.Since(started).Milliseconds())
			}
		},
		GotFirstResponseByte: func() {
			firstByte.Store(time.Since(started).Milliseconds())
		},
	}
	resp, err := doUpstreamRequest(client, req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	fields := []zap.Field{
		zap.Int64("account_id", accountID),
		zap.Int64("response_headers_ms", time.Since(started).Milliseconds()),
		zap.Bool("connection_reused", reused.Load()),
	}
	for _, stage := range []struct {
		name  string
		value int64
	}{
		{"connection_ready_ms", connected.Load()},
		{"request_written_ms", written.Load()},
		{"response_first_byte_ms", firstByte.Load()},
	} {
		if stage.value >= 0 {
			fields = append(fields, zap.Int64(stage.name, stage.value))
		}
	}
	if resp != nil {
		fields = append(fields, zap.Int("status_code", resp.StatusCode), zap.String("upstream_protocol", resp.Proto))
	}
	if err != nil {
		fields = append(fields, zap.Bool("transport_failed", true))
	}
	logger.FromContext(req.Context()).Info("upstream network headers", fields...)
	return resp, err
}
