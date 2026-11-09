package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseNetworkObservationKeepsGinWriteAndFlushSemantics(t *testing.T) {
	output := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(output)
	w := &responseNetworkObservation{ResponseWriter: c.Writer}
	w.Header().Set("Content-Type", "text/event-stream")
	require.False(t, w.Written())
	require.Equal(t, -1, w.Size())
	w.WriteHeader(http.StatusOK)
	require.False(t, w.Written(), "header selection must not prematurely commit output")
	n, err := w.WriteString("data: first\n\n")
	require.NoError(t, err)
	require.Equal(t, n, w.Size())
	require.True(t, w.Written())
	require.False(t, w.firstBody.IsZero())
	require.True(t, w.firstFlush.IsZero())
	firstBody := w.firstBody
	w.Flush()
	require.True(t, output.Flushed)
	require.False(t, w.firstFlush.IsZero())
	firstFlush := w.firstFlush
	_, err = w.Write([]byte("data: terminal\n\n"))
	require.NoError(t, err)
	w.Flush()
	require.Equal(t, firstBody, w.firstBody)
	require.Equal(t, firstFlush, w.firstFlush)
	require.Equal(t, "data: first\n\ndata: terminal\n\n", output.Body.String())
	require.Equal(t, output.Body.Len(), w.Size())
	require.Equal(t, "text/event-stream", output.Header().Get("Content-Type"))
}
