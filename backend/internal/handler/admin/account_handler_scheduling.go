package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) RefreshGroupScheduling(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid group ID")
		return
	}
	var request struct {
		Model string `json:"model" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Model is required")
		return
	}
	request.Model = strings.TrimSpace(request.Model)
	if err := service.ValidateSchedulingProbeModel(request.Model); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.schedulingGateway == nil || h.accountTestService == nil {
		response.Error(c, 503, "Scheduling evaluation unavailable")
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	events := make(chan service.SchedulingRefreshEvent)
	go func() {
		defer close(events)
		emit := func(event service.SchedulingRefreshEvent) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}
		if err := h.schedulingGateway.RefreshGroupScheduling(ctx, h.accountTestService, id, request.Model, emit); err != nil && ctx.Err() == nil {
			emit(service.SchedulingRefreshEvent{Type: "error", Error: err.Error()})
		}
	}()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
