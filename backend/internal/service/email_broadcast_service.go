package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const EmailBroadcastMaxRecipients = 10000
const emailBroadcastMaxInput = 1 << 20

var qqNumberPattern = regexp.MustCompile(`^[1-9][0-9]{4,11}$`)

type EmailBroadcastPreview struct {
	Recipients []string `json:"recipients"`
	Invalid    []string `json:"invalid"`
	Duplicates int      `json:"duplicates"`
}

// ParseEmailBroadcastRecipients accepts complete tokens only; never guesses addresses from prose.
func ParseEmailBroadcastRecipients(raw string) (*EmailBroadcastPreview, error) {
	if len(raw) > emailBroadcastMaxInput || !utf8.ValidString(raw) {
		return nil, infraerrors.BadRequest("BROADCAST_INPUT_TOO_LARGE", "Recipient input must be valid UTF-8 and at most 1 MiB")
	}
	result := &EmailBroadcastPreview{Recipients: []string{}, Invalid: []string{}}
	seen := map[string]bool{}
	tokens := strings.FieldsFunc(strings.TrimPrefix(raw, "\ufeff"), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",;，；、", r)
	})
	if len(tokens) > EmailBroadcastMaxRecipients*2 {
		return nil, infraerrors.BadRequest("BROADCAST_TOO_MANY_RECIPIENTS", "At most 20000 input entries and 10000 unique recipients are allowed")
	}
	for _, token := range tokens {
		token = strings.Trim(token, "\"'")
		address := strings.ToLower(token)
		if qqNumberPattern.MatchString(address) {
			address += "@qq.com"
		}
		parsed, err := mail.ParseAddress(address)
		at := strings.LastIndexByte(address, '@')
		if err != nil || parsed.Name != "" || parsed.Address != address || len(address) > 254 || at <= 0 || !strings.Contains(address[at+1:], ".") || strings.ContainsAny(address, "<>\r\n") {
			result.Invalid = append(result.Invalid, token)
			continue
		}
		if seen[address] {
			result.Duplicates++
			continue
		}
		seen[address] = true
		result.Recipients = append(result.Recipients, address)
	}
	if len(result.Recipients) > EmailBroadcastMaxRecipients {
		return nil, infraerrors.BadRequest("BROADCAST_TOO_MANY_RECIPIENTS", "At most 10000 unique recipients are allowed")
	}
	return result, nil
}

type EmailBroadcastInput struct {
	ID              string `json:"id"`
	Recipients      string `json:"recipients"`
	Subject         string `json:"subject"`
	Content         string `json:"content"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type EmailBroadcast struct {
	ID              string    `json:"id"`
	Subject         string    `json:"subject"`
	Content         string    `json:"content,omitempty"`
	IntervalSeconds int       `json:"interval_seconds"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	Total           int       `json:"total"`
	Pending         int       `json:"pending"`
	Sending         int       `json:"sending"`
	Sent            int       `json:"sent"`
	Failed          int       `json:"failed"`
	Unknown         int       `json:"unknown"`
	Cancelled       int       `json:"cancelled"`
}

type EmailBroadcastRecipient struct {
	Email  string `json:"email"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

type EmailBroadcastDetail struct {
	Broadcast  EmailBroadcast            `json:"broadcast"`
	Recipients []EmailBroadcastRecipient `json:"recipients"`
	Total      int                       `json:"total"`
}

// One persistent queue shares the existing SMTP configuration. Every address receives its own message.
type EmailBroadcastService struct {
	db        *sql.DB
	email     *EmailService
	send      func(context.Context, *SMTPConfig, string, string, string) error
	stop      context.CancelFunc
	wg        sync.WaitGroup
	startOnce sync.Once
}

func NewEmailBroadcastService(db *sql.DB, email *EmailService) *EmailBroadcastService {
	return &EmailBroadcastService{db: db, email: email, send: email.sendEmailWithConfig}
}

func (s *EmailBroadcastService) Start() {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.stop = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := s.tick(ctx); err != nil && ctx.Err() == nil {
						slog.Error("email broadcast worker failed", "error", err)
					}
				}
			}
		}()
	})
}

func (s *EmailBroadcastService) Stop() {
	if s.stop != nil {
		s.stop()
		s.wg.Wait()
	}
}

func validateEmailBroadcastMessage(subject, content string) error {
	if strings.TrimSpace(subject) == "" || utf8.RuneCountInString(subject) > 200 || strings.ContainsAny(subject, "\r\n\x00") || !utf8.ValidString(subject) || strings.TrimSpace(content) == "" || len(content) > 100000 || !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return infraerrors.BadRequest("BROADCAST_INVALID_MESSAGE", "Subject is required (at most 200 characters, one line); content is required (at most 100000 UTF-8 bytes)")
	}
	return nil
}

func emailBroadcastHTML(content string) string {
	return `<!doctype html><html><body><div style="font-family:Arial,sans-serif;line-height:1.7;white-space:pre-wrap;overflow-wrap:anywhere">` + strings.ReplaceAll(html.EscapeString(content), "\n", "<br>\n") + `</div></body></html>`
}

func (s *EmailBroadcastService) Create(ctx context.Context, actorID int64, input EmailBroadcastInput) (*EmailBroadcastDetail, error) {
	if _, err := uuid.Parse(input.ID); err != nil || actorID <= 0 {
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_ID", "A valid request UUID is required")
	}
	input.Subject = strings.TrimSpace(input.Subject)
	if err := validateEmailBroadcastMessage(input.Subject, input.Content); err != nil {
		return nil, err
	}
	if input.IntervalSeconds < 1 || input.IntervalSeconds > 60 {
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_INTERVAL", "Send interval must be between 1 and 60 seconds")
	}
	preview, err := ParseEmailBroadcastRecipients(input.Recipients)
	if err != nil {
		return nil, err
	}
	if len(preview.Recipients) == 0 || len(preview.Invalid) != 0 {
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_RECIPIENTS", "Preview recipients and remove invalid entries before sending")
	}
	config, err := s.email.GetSMTPConfig(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = buildSMTPMessage(config, preview.Recipients[0], input.Subject, emailBroadcastHTML(input.Content)); err != nil {
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_SMTP", "Check SMTP sender configuration")
	}
	payload, err := json.Marshal([]any{actorID, input.Subject, input.Content, input.IntervalSeconds, preview.Recipients})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO email_broadcasts (id,actor_id,payload_hash,subject,content,interval_seconds) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO NOTHING`, input.ID, actorID, hash, input.Subject, input.Content, input.IntervalSeconds)
	if err != nil {
		return nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if inserted == 0 {
		var existingHash string
		if err := tx.QueryRowContext(ctx, `SELECT payload_hash FROM email_broadcasts WHERE id=$1`, input.ID).Scan(&existingHash); err != nil {
			return nil, err
		}
		if hash != existingHash {
			return nil, infraerrors.Conflict("BROADCAST_ID_CONFLICT", "This request ID already belongs to a different broadcast")
		}
	} else {
		recipients, err := json.Marshal(preview.Recipients)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO email_broadcast_recipients (broadcast_id,email) SELECT $1,recipient.value FROM jsonb_array_elements_text($2::jsonb) WITH ORDINALITY AS recipient(value,ordinal) ORDER BY recipient.ordinal`, input.ID, string(recipients)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Detail(ctx, input.ID, "", 1)
}

func (s *EmailBroadcastService) Test(ctx context.Context, recipient, subject, content string) error {
	if err := validateEmailBroadcastMessage(subject, content); err != nil {
		return err
	}
	preview, err := ParseEmailBroadcastRecipients(recipient)
	if err != nil {
		return err
	}
	if len(preview.Recipients) != 1 || len(preview.Invalid) != 0 || preview.Duplicates != 0 {
		return infraerrors.BadRequest("BROADCAST_TEST_RECIPIENT", "Enter exactly one QQ number or email for the test")
	}
	config, err := s.email.GetSMTPConfig(ctx)
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := s.send(sendCtx, config, preview.Recipients[0], subject, emailBroadcastHTML(content)); err != nil {
		return infraerrors.BadRequest("BROADCAST_TEST_FAILED", "SMTP test delivery failed; check SMTP configuration and recipient")
	}
	return nil
}

const emailBroadcastSelect = `SELECT b.id,b.subject,b.content,b.interval_seconds,b.status,b.created_at,
COUNT(r.id),COUNT(r.id) FILTER (WHERE r.status='pending'),COUNT(r.id) FILTER (WHERE r.status='sending'),
COUNT(r.id) FILTER (WHERE r.status='sent'),COUNT(r.id) FILTER (WHERE r.status='failed'),
COUNT(r.id) FILTER (WHERE r.status='unknown'),COUNT(r.id) FILTER (WHERE r.status='cancelled')
FROM email_broadcasts b LEFT JOIN email_broadcast_recipients r ON r.broadcast_id=b.id `

func scanEmailBroadcast(row interface{ Scan(...any) error }) (EmailBroadcast, error) {
	var b EmailBroadcast
	err := row.Scan(&b.ID, &b.Subject, &b.Content, &b.IntervalSeconds, &b.Status, &b.CreatedAt, &b.Total, &b.Pending, &b.Sending, &b.Sent, &b.Failed, &b.Unknown, &b.Cancelled)
	return b, err
}

func (s *EmailBroadcastService) List(ctx context.Context, page int) ([]EmailBroadcast, error) {
	rows, err := s.db.QueryContext(ctx, emailBroadcastSelect+`GROUP BY b.id ORDER BY b.created_at DESC,b.id LIMIT 20 OFFSET $1`, (page-1)*20)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []EmailBroadcast{}
	for rows.Next() {
		item, err := scanEmailBroadcast(rows)
		if err != nil {
			return nil, err
		}
		item.Content = ""
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *EmailBroadcastService) Detail(ctx context.Context, id, status string, page int) (*EmailBroadcastDetail, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_ID", "Invalid broadcast ID")
	}
	switch status {
	case "", "pending", "sending", "sent", "failed", "unknown", "cancelled":
	default:
		return nil, infraerrors.BadRequest("BROADCAST_INVALID_STATUS", "Invalid recipient status")
	}
	b, err := scanEmailBroadcast(s.db.QueryRowContext(ctx, emailBroadcastSelect+`WHERE b.id=$1 GROUP BY b.id`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("BROADCAST_NOT_FOUND", "Broadcast not found")
	}
	if err != nil {
		return nil, err
	}
	result := &EmailBroadcastDetail{Broadcast: b, Recipients: []EmailBroadcastRecipient{}}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM email_broadcast_recipients WHERE broadcast_id=$1 AND ($2='' OR status=$2)`, id, status).Scan(&result.Total); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT email,status,error FROM email_broadcast_recipients WHERE broadcast_id=$1 AND ($2='' OR status=$2) ORDER BY id LIMIT 100 OFFSET $3`, id, status, (page-1)*100)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r EmailBroadcastRecipient
		if err := rows.Scan(&r.Email, &r.Status, &r.Error); err != nil {
			return nil, err
		}
		result.Recipients = append(result.Recipients, r)
	}
	return result, rows.Err()
}

func (s *EmailBroadcastService) Cancel(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return infraerrors.BadRequest("BROADCAST_INVALID_ID", "Invalid broadcast ID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM email_broadcasts WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return infraerrors.NotFound("BROADCAST_NOT_FOUND", "Broadcast not found")
		}
		return err
	}
	if status == "queued" || status == "running" {
		if _, err := tx.ExecContext(ctx, `UPDATE email_broadcasts SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE email_broadcast_recipients SET status='cancelled',updated_at=NOW() WHERE broadcast_id=$1 AND status='pending'`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Claims are committed before SMTP. A crashed or timed-out attempt is never automatically resent.
func (s *EmailBroadcastService) tick(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(728156392)`).Scan(&locked); err != nil || !locked {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE email_broadcast_recipients SET status='unknown',error='Delivery interrupted; verify receipt before resending',updated_at=NOW() WHERE status='sending' AND updated_at<NOW()-INTERVAL '2 minutes'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE email_broadcasts b SET status='completed' WHERE status IN ('queued','running') AND NOT EXISTS (SELECT 1 FROM email_broadcast_recipients r WHERE r.broadcast_id=b.id AND r.status IN ('pending','sending'))`); err != nil {
		return err
	}
	var sending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM email_broadcast_recipients WHERE status='sending')`).Scan(&sending); err != nil {
		return err
	}
	if sending {
		return tx.Commit()
	}
	var id, subject, content string
	var interval int
	err = tx.QueryRowContext(ctx, `SELECT id,subject,content,interval_seconds FROM email_broadcasts WHERE status IN ('queued','running') AND next_send_at<=NOW() ORDER BY created_at,id LIMIT 1 FOR UPDATE`).Scan(&id, &subject, &content, &interval)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	var recipientID int64
	var email string
	if err := tx.QueryRowContext(ctx, `UPDATE email_broadcast_recipients SET status='sending',updated_at=NOW() WHERE id=(SELECT id FROM email_broadcast_recipients WHERE broadcast_id=$1 AND status='pending' ORDER BY id LIMIT 1) RETURNING id,email`, id).Scan(&recipientID, &email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE email_broadcasts SET status='running',next_send_at=NOW()+($2 * INTERVAL '1 second') WHERE id=$1`, id, interval); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	config, sendErr := s.email.GetSMTPConfig(ctx)
	if sendErr == nil {
		sendCtx, stop := context.WithTimeout(ctx, 25*time.Second)
		sendErr = s.send(sendCtx, config, email, subject, emailBroadcastHTML(content))
		stop()
	}
	status, detail := "sent", ""
	if sendErr != nil {
		status, detail = "failed", "SMTP delivery failed; check sender settings, recipient or provider limits"
		if errors.Is(sendErr, ErrSMTPDeliveryUncertain) {
			status, detail = "unknown", "SMTP acknowledgement lost; verify receipt before resending"
		}
		// Sender errors may contain recipient/configuration data; store only a generic failure.
	}
	// Finish even during shutdown; a lost acknowledgement remains 'unknown' after recovery.
	finishCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if _, err := s.db.ExecContext(finishCtx, `UPDATE email_broadcast_recipients SET status=$2,error=$3,updated_at=NOW() WHERE id=$1 AND status='sending'`, recipientID, status, detail); err != nil {
		return fmt.Errorf("record broadcast delivery outcome: %w", err)
	}
	return nil
}
