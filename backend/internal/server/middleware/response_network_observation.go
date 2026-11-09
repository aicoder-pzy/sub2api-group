package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
)

// This observes accepted writes and Flush calls without buffering or changing
// Gin's Size/Written semantics, which the gateway uses for safe failover.
// These are application boundaries, not remote receipt or semantic-token times.
type responseNetworkObservation struct {
	gin.ResponseWriter
	firstBody  time.Time
	firstFlush time.Time
}

func (w *responseNetworkObservation) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 && w.firstBody.IsZero() {
		w.firstBody = time.Now()
	}
	return n, err
}

func (w *responseNetworkObservation) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 && w.firstBody.IsZero() {
		w.firstBody = time.Now()
	}
	return n, err
}

func (w *responseNetworkObservation) Flush() {
	w.ResponseWriter.Flush()
	if w.firstFlush.IsZero() {
		w.firstFlush = time.Now()
	}
}
