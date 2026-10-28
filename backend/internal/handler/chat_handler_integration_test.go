//go:build integration

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestChatSyncPostgres(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("chat_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users (id BIGINT PRIMARY KEY); INSERT INTO users VALUES (1),(2)`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/260_web_chat_conversations.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	h := NewChatHandler(db)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("Test-User") != "" {
			var id int64
			_, _ = fmt.Sscan(c.GetHeader("Test-User"), &id)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
		}
	})
	router.GET("/chat", h.List)
	router.GET("/chat/:id", h.Get)
	router.PUT("/chat/:id", h.Put)
	request := func(user, method, path string, body any) *httptest.ResponseRecorder {
		data, marshalErr := json.Marshal(body)
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Test-User", user)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	chat := &chatConversation{ID: "shared", Title: "Finance", Model: "vision", MaxOutputTokens: 4096, Messages: []chatMessage{}, DraftImages: []chatImage{{Name: "receipt.png", DataURL: "data:image/png;base64,AA==", Width: 20, Height: 30}}}
	put := func(revision int, deleted bool) any {
		if deleted {
			return map[string]any{"revision": revision, "deleted": true}
		}
		return map[string]any{"revision": revision, "conversation": chat}
	}
	require.Equal(t, 401, request("", "GET", "/chat", nil).Code)
	require.Equal(t, 200, request("1", "PUT", "/chat/shared", put(0, false)).Code)
	w := request("1", "GET", "/chat/shared", nil)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Body.String(), "receipt.png")
	require.NotContains(t, request("1", "GET", "/chat", nil).Body.String(), "receipt.png")
	require.Equal(t, 404, request("2", "GET", "/chat/shared", nil).Code)
	require.Equal(t, 409, request("2", "PUT", "/chat/shared", put(1, true)).Code)
	require.NotContains(t, request("2", "GET", "/chat", nil).Body.String(), "shared")
	// Same revision from two devices admits exactly one writer.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- request("1", "PUT", "/chat/shared", put(1, false)).Code }()
	}
	wg.Wait()
	close(codes)
	statuses := map[int]int{}
	for code := range codes {
		statuses[code]++
	}
	require.Equal(t, map[int]int{200: 1, 409: 1}, statuses)
	require.Equal(t, 200, request("1", "PUT", "/chat/shared", put(2, true)).Code)
	require.NotContains(t, request("1", "GET", "/chat/shared", nil).Body.String(), "receipt.png")
	require.Equal(t, 409, request("1", "PUT", "/chat/shared", put(3, false)).Code) // stale devices cannot resurrect tombstones
	// Quota admission is serialized; existing history remains intact on rejection.
	_, err = db.Exec(`INSERT INTO web_chat_conversations (user_id,id) SELECT 2,'quota-' || n FROM generate_series(1,100) n`)
	require.NoError(t, err)
	require.Equal(t, 413, request("2", "PUT", "/chat/shared", put(0, false)).Code)
	_, err = db.Exec(`DELETE FROM users WHERE id=1`)
	require.NoError(t, err)
	require.Equal(t, 404, request("1", "GET", "/chat/shared", nil).Code)
}
