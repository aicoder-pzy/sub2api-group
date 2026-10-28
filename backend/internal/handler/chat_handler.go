package handler

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const chatMaxBytes = 24 << 20

var chatIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

// ChatHandler stores owner-scoped browser conversations. Inference uses the existing gateway.
type ChatHandler struct{ db *sql.DB }

func NewChatHandler(db *sql.DB) *ChatHandler { return &ChatHandler{db: db} }

type chatImage struct {
	Name    string `json:"name"`
	DataURL string `json:"dataUrl"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}
type chatSheet struct {
	Name    string `json:"name"`
	Rows    int    `json:"rows"`
	Columns int    `json:"columns"`
}
type chatAttachment struct {
	Name    string      `json:"name"`
	Content string      `json:"content"`
	Sheets  []chatSheet `json:"sheets"`
}
type chatMessage struct {
	ID          string           `json:"id"`
	Role        string           `json:"role"`
	Content     string           `json:"content"`
	Reasoning   string           `json:"reasoning"`
	Status      string           `json:"status"`
	Attachments []chatAttachment `json:"attachments,omitempty"`
	Images      []chatImage      `json:"images,omitempty"`
}
type chatConversation struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	KeyID           *int64           `json:"keyId"`
	Model           string           `json:"model"`
	Effort          string           `json:"effort"`
	UpdatedAt       string           `json:"updatedAt"`
	Messages        []chatMessage    `json:"messages"`
	FinanceMode     bool             `json:"financeMode"`
	MaxOutputTokens int              `json:"maxOutputTokens"`
	Draft           string           `json:"draft"`
	DraftFiles      []chatAttachment `json:"draftFiles,omitempty"`
	DraftImages     []chatImage      `json:"draftImages,omitempty"`
}
type chatRecord struct {
	ID           string          `json:"id"`
	Revision     int64           `json:"revision"`
	Title        string          `json:"title"`
	Deleted      bool            `json:"deleted"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Conversation json.RawMessage `json:"conversation,omitempty"`
}

func chatOwner(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, http.StatusUnauthorized, "Authentication required")
		return 0, false
	}
	return subject.UserID, true
}

func validChatFiles(files []chatAttachment, images []chatImage) bool {
	if len(files)+len(images) > 4 {
		return false
	}
	for _, f := range files {
		if len(f.Name) > 1020 || len(f.Content) > 400000 || len(f.Sheets) > 12 {
			return false
		}
		for _, s := range f.Sheets {
			if len(s.Name) > 1020 || s.Rows <= 0 || s.Columns <= 0 {
				return false
			}
		}
	}
	for _, img := range images {
		if len(img.Name) > 1020 || len(img.DataURL) > 2<<20 || img.Width <= 0 || img.Width > 2560 || img.Height <= 0 || img.Height > 2560 {
			return false
		}
		prefix, data, ok := strings.Cut(img.DataURL, ",")
		if !ok || (prefix != "data:image/png;base64" && prefix != "data:image/jpeg;base64" && prefix != "data:image/webp;base64") || data == "" {
			return false
		}
		if _, err := base64.StdEncoding.Strict().DecodeString(data); err != nil {
			return false
		}
	}
	return true
}

func validChat(chat *chatConversation, id string) bool {
	if chat == nil || chat.ID != id || utf8.RuneCountInString(chat.Title) > 80 || len(chat.Model) > 800 || len(chat.Effort) > 20 || len(chat.Draft) > 400000 || len(chat.UpdatedAt) > 80 || len(chat.Messages) > 500 || (chat.KeyID != nil && *chat.KeyID <= 0) {
		return false
	}
	if chat.MaxOutputTokens != 4096 && chat.MaxOutputTokens != 8192 && chat.MaxOutputTokens != 16384 {
		return false
	}
	if !validChatFiles(chat.DraftFiles, chat.DraftImages) {
		return false
	}
	ids := make(map[string]bool, len(chat.Messages))
	for i := range chat.Messages {
		m := &chat.Messages[i]
		if !chatIDPattern.MatchString(m.ID) || ids[m.ID] || (m.Role != "user" && m.Role != "assistant") || len(m.Content) > 1600000 || len(m.Reasoning) > 1600000 || !validChatFiles(m.Attachments, m.Images) || (m.Role != "user" && (len(m.Images) > 0 || len(m.Attachments) > 0)) {
			return false
		}
		ids[m.ID] = true
		switch m.Status {
		case "complete", "stopped", "failed", "limited":
		case "streaming":
			m.Status = "stopped"
		default:
			return false
		}
	}
	if chat.Messages == nil {
		chat.Messages = []chatMessage{}
	}
	return true
}

func (h *ChatHandler) List(c *gin.Context) {
	userID, ok := chatOwner(c)
	if !ok {
		return
	}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT id, revision, title, deleted, updated_at FROM web_chat_conversations WHERE user_id=$1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		response.Error(c, 500, "Unable to read conversations")
		return
	}
	defer func() { _ = rows.Close() }()
	items := []chatRecord{}
	for rows.Next() {
		var r chatRecord
		if err = rows.Scan(&r.ID, &r.Revision, &r.Title, &r.Deleted, &r.UpdatedAt); err != nil {
			response.Error(c, 500, "Unable to read conversations")
			return
		}
		items = append(items, r)
	}
	if rows.Err() != nil {
		response.Error(c, 500, "Unable to read conversations")
		return
	}
	response.Success(c, items)
}

func (h *ChatHandler) Get(c *gin.Context) {
	userID, ok := chatOwner(c)
	if !ok {
		return
	}
	if !chatIDPattern.MatchString(c.Param("id")) {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	var r chatRecord
	err := h.db.QueryRowContext(c.Request.Context(), `SELECT id, revision, title, deleted, updated_at, payload FROM web_chat_conversations WHERE user_id=$1 AND id=$2`, userID, c.Param("id")).Scan(&r.ID, &r.Revision, &r.Title, &r.Deleted, &r.UpdatedAt, &r.Conversation)
	if errors.Is(err, sql.ErrNoRows) {
		response.Error(c, 404, "Conversation not found")
		return
	}
	if err != nil {
		response.Error(c, 500, "Unable to read conversation")
		return
	}
	if r.Deleted {
		r.Conversation = nil
	}
	response.Success(c, r)
}

func (h *ChatHandler) Put(c *gin.Context) {
	userID, ok := chatOwner(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if !chatIDPattern.MatchString(id) {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	var input struct {
		Revision     int64             `json:"revision"`
		Deleted      bool              `json:"deleted"`
		Conversation *chatConversation `json:"conversation"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chatMaxBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields() // Credentials and arbitrary fields never enter persistent chat storage.
	err := decoder.Decode(&input)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(c, 413, "Conversation exceeds 24 MiB")
		} else {
			response.BadRequest(c, "Invalid conversation")
		}
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF || input.Revision < 0 || (input.Deleted && input.Conversation != nil) || (!input.Deleted && !validChat(input.Conversation, id)) {
		response.BadRequest(c, "Invalid conversation")
		return
	}
	payload, title := []byte(`{}`), ""
	if !input.Deleted {
		payload, err = json.Marshal(input.Conversation)
		if err != nil || len(payload) > chatMaxBytes {
			response.Error(c, 413, "Conversation exceeds 24 MiB")
			return
		}
		title = input.Conversation.Title
	}
	ctx := c.Request.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		response.Error(c, 500, "Unable to save conversation")
		return
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize quota checks for this owner only, including concurrent first-device imports.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('webchat:' || $1::text, 0))`, userID); err != nil {
		response.Error(c, 500, "Unable to save conversation")
		return
	}
	var revision int64
	var deleted bool
	err = tx.QueryRowContext(ctx, `SELECT revision, deleted FROM web_chat_conversations WHERE user_id=$1 AND id=$2`, userID, id).Scan(&revision, &deleted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		response.Error(c, 500, "Unable to save conversation")
		return
	}
	if revision != input.Revision || deleted {
		response.Error(c, 409, "Conversation changed; fetch the latest revision")
		return
	}
	if revision == 0 && input.Deleted {
		response.Error(c, 404, "Conversation not found")
		return
	}
	var count, active, size int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE NOT deleted), COALESCE(SUM(byte_size),0) FROM web_chat_conversations WHERE user_id=$1 AND id<>$2`, userID, id).Scan(&count, &active, &size)
	if err != nil {
		response.Error(c, 500, "Unable to save conversation")
		return
	}
	if !input.Deleted && (active >= 100 || count >= 1000 || size+int64(len(payload)) > 200<<20) {
		response.Error(c, 413, "Chat storage limit: 100 conversations or 200 MiB; export and delete older conversations")
		return
	}
	var r chatRecord
	err = tx.QueryRowContext(ctx, `INSERT INTO web_chat_conversations (user_id,id,revision,title,payload,byte_size,deleted) VALUES ($1,$2,1,$3,$4::jsonb,$5,$6) ON CONFLICT (user_id,id) DO UPDATE SET revision=web_chat_conversations.revision+1,title=EXCLUDED.title,payload=EXCLUDED.payload,byte_size=EXCLUDED.byte_size,deleted=EXCLUDED.deleted,updated_at=NOW() RETURNING id,revision,title,deleted,updated_at`, userID, id, title, string(payload), len(payload), input.Deleted).Scan(&r.ID, &r.Revision, &r.Title, &r.Deleted, &r.UpdatedAt)
	if err != nil || tx.Commit() != nil {
		response.Error(c, 500, "Unable to save conversation")
		return
	}
	response.Success(c, r)
}
