//go:build unit

package handler

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatValidationBeforeStorage(t *testing.T) {
	for _, body := range []string{
		`{"revision":0,"conversation":{"id":"x","key":"SECRET"}}`,
		`{"revision":0,"conversation":{"id":"x","maxOutputTokens":4096,"draftImages":[{"name":"x","dataUrl":"https://external.invalid/photo","width":1,"height":1}]}}`,
		`{"revision":-1,"deleted":true}`,
		`{"revision":0,"deleted":true} {}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Params = gin.Params{{Key: "id", Value: "x"}}
		c.Request = httptest.NewRequest("PUT", "/chat/x", bytes.NewBufferString(body))
		NewChatHandler(nil).Put(c)
		require.Equal(t, 400, c.Writer.Status())
	}
	chat := &chatConversation{ID: "x", Title: strings.Repeat("财", 80), MaxOutputTokens: 4096, Messages: []chatMessage{{ID: "m", Role: "assistant", Status: "streaming"}}}
	require.True(t, validChat(chat, "x"))
	require.Equal(t, "stopped", chat.Messages[0].Status)
	chat.Messages = append(chat.Messages, chat.Messages[0])
	require.False(t, validChat(chat, "x"))
}

func TestChatContextCheckpointValidation(t *testing.T) {
	chat := &chatConversation{ID: "x", MaxOutputTokens: 4096,
		Messages: []chatMessage{{ID: "m", Role: "user", Content: "original evidence", Status: "complete"}},
		Context:  &chatContext{Summary: "GBP -270,937.50: cause unverified", Through: 1, Fingerprint: strings.Repeat("a", 64)}}
	require.True(t, validChat(chat, "x"))
	for _, ctx := range []chatContext{
		{Summary: "", Through: 1, Fingerprint: strings.Repeat("a", 64)},
		{Summary: "summary", Through: 2, Fingerprint: strings.Repeat("a", 64)},
		{Summary: "summary", Through: 0, Fingerprint: strings.Repeat("a", 64)},
		{Summary: "summary", Through: 1, Fingerprint: strings.Repeat("g", 64)},
		{Summary: strings.Repeat("财", 60001), Through: 1, Fingerprint: strings.Repeat("a", 64)},
	} {
		chat.Context = &ctx
		require.False(t, validChat(chat, "x"))
		require.Equal(t, "original evidence", chat.Messages[0].Content)
	}
}
