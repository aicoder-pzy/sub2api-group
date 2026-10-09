package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const PelicanAssessmentMaxHTMLBytes = 2 << 20
const pelicanAssessmentURL = "https://manxue.ai/api/v1/tests"

var pelicanAssessmentTaskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var pelicanAssessmentHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

type PelicanAssessment struct {
	Quality   string    `json:"quality"`
	Reason    string    `json:"reason"`
	Source    string    `json:"source"`
	CheckedAt time.Time `json:"checked_at"`
}

type PelicanAssessmentRecord struct {
	Hash              string             `json:"hash"`
	Status            string             `json:"status"`
	Assessment        *PelicanAssessment `json:"assessment,omitempty"`
	Error             string             `json:"error,omitempty"`
	RetryAfterSeconds int                `json:"retry_after_seconds,omitempty"`
	TaskID            string             `json:"-"`
	Deadline          time.Time          `json:"-"`
}

type PelicanAssessmentRepository interface {
	Get(context.Context, string) (*PelicanAssessmentRecord, error)
	Save(context.Context, *PelicanAssessmentRecord) error
	Finish(context.Context, *PelicanAssessmentRecord) error
}

// This service deliberately has no account, credential, gateway or proxy dependency.
type PelicanAssessmentService struct {
	repo   PelicanAssessmentRepository
	client *http.Client
	mu     sync.Mutex
	active map[string]bool
}

func NewPelicanAssessmentService(repo PelicanAssessmentRepository) *PelicanAssessmentService {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &PelicanAssessmentService{
		repo: repo, active: make(map[string]bool),
		client: &http.Client{
			Transport: transport, Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func pelicanSourceHash(html string) (string, error) {
	if strings.TrimSpace(html) == "" || !utf8.ValidString(html) || !pelicanHTMLPattern.MatchString(html) {
		return "", infraerrors.BadRequest("PELICAN_ASSESSMENT_INVALID_HTML", "a nonempty HTML or SVG document is required")
	}
	if len(html) > PelicanAssessmentMaxHTMLBytes {
		return "", infraerrors.New(http.StatusRequestEntityTooLarge, "PELICAN_ASSESSMENT_HTML_TOO_LARGE", "HTML exceeds 2 MiB")
	}
	sum := sha256.Sum256([]byte(html))
	return hex.EncodeToString(sum[:]), nil
}

func (s *PelicanAssessmentService) Lookup(ctx context.Context, html string) (*PelicanAssessmentRecord, error) {
	hash, err := pelicanSourceHash(html)
	if err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, hash)
}

func (s *PelicanAssessmentService) lock(hash string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[hash] || len(s.active) >= 4 {
		return nil, infraerrors.Conflict("PELICAN_ASSESSMENT_BUSY", "an assessment request is already in progress; retry shortly")
	}
	s.active[hash] = true
	return func() { s.mu.Lock(); delete(s.active, hash); s.mu.Unlock() }, nil
}

func (s *PelicanAssessmentService) Start(ctx context.Context, html string) (*PelicanAssessmentRecord, error) {
	hash, err := pelicanSourceHash(html)
	if err != nil {
		return nil, err
	}
	release, err := s.lock(hash)
	if err != nil {
		return nil, err
	}
	defer release()
	previous, err := s.repo.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	if previous != nil && (previous.Status == "succeeded" || previous.Status == "running") {
		return previous, nil
	}
	// Only these two fields can cross the third-party boundary. Never marshal a
	// frontend request, account, test result or gateway payload here.
	payload, err := json.Marshal(struct {
		Benchmark string `json:"benchmark"`
		HTML      string `json:"html"`
	}{Benchmark: "pelican", HTML: html})
	if err != nil {
		return nil, err
	}
	result, err := s.request(ctx, http.MethodPost, pelicanAssessmentURL, payload, "pelican-"+uuid.NewString())
	if err != nil {
		return nil, err
	}
	if !pelicanAssessmentTaskID.MatchString(result.ID) || result.Benchmark != "pelican" {
		return nil, assessmentUnavailable("invalid response from the HTML assessment service")
	}
	record := &PelicanAssessmentRecord{Hash: hash, Status: "running", TaskID: result.ID, Deadline: time.Now().Add(10 * time.Minute)}
	if err := s.repo.Save(ctx, record); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, hash)
}

func (s *PelicanAssessmentService) Poll(ctx context.Context, hash string) (*PelicanAssessmentRecord, error) {
	if !pelicanAssessmentHash.MatchString(hash) {
		return nil, infraerrors.BadRequest("PELICAN_ASSESSMENT_INVALID_HASH", "invalid artwork hash")
	}
	release, err := s.lock(hash)
	if err != nil {
		if record, readErr := s.repo.Get(ctx, hash); readErr == nil && record != nil {
			record.RetryAfterSeconds = 3
			return record, nil
		}
		return nil, err
	}
	defer release()
	record, err := s.repo.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, infraerrors.NotFound("PELICAN_ASSESSMENT_NOT_FOUND", "assessment not found")
	}
	if record.Status != "running" {
		return record, nil
	}
	if !pelicanAssessmentTaskID.MatchString(record.TaskID) {
		return nil, assessmentUnavailable("invalid stored assessment task")
	}
	result, err := s.request(ctx, http.MethodGet, pelicanAssessmentURL+"/"+record.TaskID, nil, "")
	if err != nil {
		if ctx.Err() != nil {
			return nil, assessmentUnavailable("HTML assessment request was interrupted")
		}
		if time.Now().Before(record.Deadline) {
			record.RetryAfterSeconds = 5
			if value := infraerrors.FromError(err).Metadata["retry_after_seconds"]; value != "" {
				record.RetryAfterSeconds, _ = strconv.Atoi(value)
			}
			return record, nil
		}
		record.Status, record.Error = "failed", "HTML assessment could not be completed; quality is unknown"
	} else if result.Benchmark != "pelican" || result.ID != record.TaskID {
		record.Status, record.Error = "failed", "HTML assessment returned an invalid task; quality is unknown"
	} else {
		switch result.Status {
		case "succeeded":
			if result.Assessment == nil || !validPelicanAssessment(result.Assessment) {
				record.Status, record.Error = "failed", "HTML assessment returned an invalid result; quality is unknown"
			} else {
				record.Status, record.Assessment = "succeeded", result.Assessment
				record.Assessment.CheckedAt = time.Now().UTC()
			}
		case "failed", "cancelled":
			record.Status, record.Error = "failed", "HTML assessment service did not complete the evaluation; quality is unknown"
		case "pending", "queued", "running":
			if time.Now().Before(record.Deadline) {
				return record, nil
			}
			record.Status, record.Error = "failed", "HTML assessment timed out; quality is unknown"
		default:
			record.Status, record.Error = "failed", "HTML assessment returned an invalid status; quality is unknown"
		}
	}
	if err := s.repo.Finish(ctx, record); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, hash)
}

func validPelicanAssessment(value *PelicanAssessment) bool {
	return (value.Quality == "normal" || value.Quality == "degraded" || value.Quality == "unknown") &&
		len(value.Reason) <= 4096 && len(value.Source) <= 256
}

type pelicanRemoteAssessment struct {
	ID         string             `json:"id"`
	Benchmark  string             `json:"benchmark"`
	Status     string             `json:"status"`
	Assessment *PelicanAssessment `json:"assessment"`
}

func assessmentUnavailable(message string) *infraerrors.ApplicationError {
	return infraerrors.New(http.StatusBadGateway, "PELICAN_ASSESSMENT_UNAVAILABLE", message)
}

func (s *PelicanAssessmentService) request(ctx context.Context, method, target string, payload []byte, idempotencyKey string) (*pelicanRemoteAssessment, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, assessmentUnavailable("could not create HTML assessment request")
	}
	// Fresh headers and a dedicated client: no inbound Authorization, cookies,
	// API keys, account identities, credential plugins or proxy authentication.
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, assessmentUnavailable("could not reach the HTML assessment service")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && !(method == http.MethodPost && resp.StatusCode == http.StatusAccepted) {
		failure := assessmentUnavailable("HTML assessment service rejected the request")
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			delay := 5
			if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
				delay = min(seconds, 3600)
			} else if until, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
				delay = min(max(1, int(time.Until(until).Seconds())+1), 3600)
			}
			failure = infraerrors.New(http.StatusServiceUnavailable, "PELICAN_ASSESSMENT_BUSY", "HTML assessment service is busy").WithMetadata(map[string]string{"retry_after_seconds": strconv.Itoa(delay)})
		}
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 3*PelicanAssessmentMaxHTMLBytes+1))
	if err != nil || len(data) > 3*PelicanAssessmentMaxHTMLBytes {
		return nil, assessmentUnavailable("invalid response from the HTML assessment service")
	}
	var result pelicanRemoteAssessment
	if json.Unmarshal(data, &result) != nil {
		return nil, assessmentUnavailable("invalid response from the HTML assessment service")
	}
	return &result, nil
}
