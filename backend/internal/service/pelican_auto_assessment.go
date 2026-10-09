package service

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var pelicanAssessmentMarkup = regexp.MustCompile(`(?s)<!--sub2api:pelican-assessment:start-->.*?<!--sub2api:pelican-assessment:end-->`)
var pelicanSourceFence = regexp.MustCompile("(?is)```(?:html|xml)?\\s*(.*?)```")
var pelicanDocumentTag = regexp.MustCompile(`(?i)<html[\s>]`)
var pelicanSVGTag = regexp.MustCompile(`(?i)<svg[\s>]`)
var pelicanBodyTag = regexp.MustCompile(`(?i)<body(?:\s[^>]*)?>`)

// Match the frontend's source extraction so every entry point reuses the same
// assessment. Our badge, sandbox policy and preview scripts are never uploaded.
func extractPelicanSource(raw string) string {
	source := strings.TrimSpace(pelicanAssessmentMarkup.ReplaceAllString(raw, ""))
	if match := pelicanSourceFence.FindStringSubmatch(source); len(match) > 1 {
		source = strings.TrimSpace(match[1])
	}
	if start := pelicanHTMLPattern.FindStringIndex(source); start != nil {
		source = strings.TrimSpace(source[start[0]:])
	} else {
		return ""
	}
	if end := strings.LastIndex(strings.ToLower(source), "</html>"); end >= 0 {
		source = source[:end+len("</html>")]
	}
	if !pelicanDocumentTag.MatchString(source) && pelicanSVGTag.MatchString(source) {
		source = `<html><head><meta charset="utf-8"><title>Pelican test</title><style>html,body{margin:0;height:100%}body{display:flex;align-items:center;justify-content:center}svg{max-width:100%;max-height:100%}</style></head><body>` + source + `</body></html>`
	}
	return source
}

// AssessOutput is called after successful generation, with only the model's
// drawing. Failure is an unknown badge, never a generation or scheduling error.
func (s *PelicanAssessmentService) AssessOutput(ctx context.Context, output string) string {
	source := extractPelicanSource(output)
	if source == "" {
		return output
	}
	assessment := &PelicanAssessment{Quality: "unknown", Reason: "作品评估未完成", Source: "manxue:unavailable"}
	if s != nil {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if result := s.assessSource(ctx, source); result != nil {
			assessment = result
		}
	}
	return decoratePelicanSource(source, assessment)
}

func (s *PelicanAssessmentService) assessSource(ctx context.Context, source string) (result *PelicanAssessment) {
	var record *PelicanAssessmentRecord
	defer func() {
		if result == nil && record != nil && record.Status == "running" {
			// Closing the automatic stage must not leave a running task that later
			// appears to contradict the unknown badge stored with the artwork.
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			record.Status, record.Error = "failed", "HTML assessment did not finish; quality is unknown"
			_ = s.repo.Finish(finishCtx, record)
		}
	}()
	for ctx.Err() == nil {
		polling := record != nil
		var next *PelicanAssessmentRecord
		var err error
		if !polling {
			next, err = s.Start(ctx, source)
		} else {
			next, err = s.Poll(ctx, record.Hash)
		}
		delay := 3
		if err != nil {
			failure := infraerrors.FromError(err)
			if failure.Reason != "PELICAN_ASSESSMENT_BUSY" {
				return nil
			}
			if seconds, parseErr := strconv.Atoi(failure.Metadata["retry_after_seconds"]); parseErr == nil {
				delay = max(1, seconds)
			}
		} else if next != nil {
			record = next
			switch record.Status {
			case "succeeded":
				return record.Assessment
			case "failed":
				return nil
			}
			if !polling {
				continue
			}
			delay = max(3, record.RetryAfterSeconds)
		}
		timer := time.NewTimer(time.Duration(delay) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

func decoratePelicanSource(source string, assessment *PelicanAssessment) string {
	quality, label, color, background := "unknown", "无法判定", "#475569", "#f1f5f9"
	switch assessment.Quality {
	case "normal":
		quality, label, color, background = "normal", "正常", "#065f46", "#d1fae5"
	case "degraded":
		quality, label, color, background = "degraded", "疑似降智", "#991b1b", "#fee2e2"
	}
	badge := fmt.Sprintf(`<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="%s" title="%s" style="all:initial!important;position:fixed!important;top:12px!important;right:12px!important;z-index:2147483647!important;padding:7px 12px!important;border-radius:999px!important;font:600 14px/1.4 system-ui,sans-serif!important;color:%s!important;background:%s!important;box-shadow:0 2px 8px #0002!important;pointer-events:none!important;white-space:nowrap!important">满血 AI · %s</aside><!--sub2api:pelican-assessment:end-->`, quality, html.EscapeString(assessment.Reason), color, background, label)
	if body := pelicanBodyTag.FindStringIndex(source); body != nil {
		return source[:body[1]] + badge + source[body[1]:]
	}
	if end := strings.LastIndex(strings.ToLower(source), "</html>"); end >= 0 {
		return source[:end] + badge + source[end:]
	}
	return source + badge
}
