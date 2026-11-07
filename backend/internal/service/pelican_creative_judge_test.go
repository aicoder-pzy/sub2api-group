//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func creativeJudgeFixture() string {
	return `{"confidence":"high","reason":"All four required objects interact visibly in a coherent animated scene.","criteria":[{"key":"materials","score":4,"reason":"All required objects are recognizable."},{"key":"story","score":4,"reason":"The subject uses both objects in the required setting."},{"key":"visual","score":4,"reason":"Clear shapes, balanced composition and matching palette."},{"key":"animation","score":4,"reason":"The subject moves meaningfully between the three frames."}],"materials":[{"key":"subject","status":"present","reason":"Visible at the center."},{"key":"element1","status":"present","reason":"Held by the subject."},{"key":"element2","status":"present","reason":"Used during the action."},{"key":"setting","status":"present","reason":"Visible in the background."}]}`
}

func TestCreativeVisualJudgmentDistinguishesFailuresAndUncertainty(t *testing.T) {
	good := creativeJudgeFixture()
	for _, tc := range []struct {
		name, output, status string
		motion               bool
		score                int
	}{
		{"complete", good, "passed", true, 80},
		{"missing material", strings.Replace(good, `"status":"present"`, `"status":"missing"`, 1), "failed", true, 80},
		{"wrong scene", strings.ReplaceAll(good, `"score":4`, `"score":1`), "failed", true, 20},
		{"static artwork", good, "failed", false, 64},
		{"unclear material", strings.Replace(good, `"status":"present"`, `"status":"unclear"`, 1), "unknown", true, 80},
		{"low confidence", strings.Replace(good, `"confidence":"high"`, `"confidence":"low"`, 1), "unknown", true, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grade, err := parseCreativeJudgment(tc.output, tc.motion)
			require.NoError(t, err)
			require.Equal(t, tc.status, grade.Status)
			require.Equal(t, tc.score, *grade.Score)
			if grade.Status == "unknown" {
				require.Equal(t, "unknown", grade.assessment().Quality)
			}
		})
	}
	for _, invalid := range []string{
		`{}`, good + `{}`, "```json\n" + good + "\n```",
		strings.Replace(good, `"score":4`, `"score":0,"score":4`, 1),
		strings.Replace(good, `"score":4`, `"score":null`, 1),
		strings.Replace(good, `"score":4`, `"score":6`, 1),
		strings.Replace(good, `"key":"story"`, `"key":"materials"`, 1),
		strings.Replace(good, `"key":"setting"`, `"key":"subject"`, 1),
		strings.Replace(good, `"confidence":"high"`, `"confidence":"high","overall":"pass"`, 1),
	} {
		_, err := parseCreativeJudgment(invalid, true)
		require.Error(t, err, invalid)
	}
	medium, err := parseCreativeJudgment(strings.Replace(strings.ReplaceAll(good, `"score":4`, `"score":1`), `"confidence":"high"`, `"confidence":"medium"`, 1), true)
	require.NoError(t, err)
	require.Equal(t, "failed", medium.Status)
	require.Equal(t, "unknown", medium.assessment().Quality, "uncertain failures cannot evict accounts")
}

func TestCreativeVisionUsesIndependentAccountAndActualImages(t *testing.T) {
	judge, upstream, slots := qualityTestJudge([]Account{qualityTestAccount(1), qualityTestAccount(2)}, qualityTextResponse(creativeJudgeFixture(), `{"type":"response.completed"}`))
	svc, _ := newGroupTestService(newGroupTestRepoFake(), &groupTestRouterFake{}, nil)
	svc.creativeJudge = judge
	svc.groups = judge.groups
	svc.renderCreative = func(context.Context, string) (*pelicanCreativeFrames, error) {
		return &pelicanCreativeFrames{Images: []string{"data:image/jpeg;base64,frameA", "data:image/jpeg;base64,frameB", "data:image/jpeg;base64,frameC"}, Hashes: []string{"A", "B", "C"}, Motion: true}, nil
	}
	cfg := &PelicanTestConfig{PromptMode: pelicanRandomCreative, CreativeRound: newPelicanCreativeRound(groupTestNow), CreativeJudgeGroupID: 1, CreativeJudgeModelID: "gpt-6-astra"}
	grade, costs := svc.assessCreative(context.Background(), 1, cfg, "<html><body><svg></svg><script>secretGeneratedCode()</script></body></html>")
	require.Equal(t, "passed", grade.Status)
	require.Equal(t, 80, *grade.Score)
	require.Len(t, grade.FrameHashes, 3)
	require.NotEmpty(t, grade.SourceHash)
	require.Equal(t, []int64{2}, slots.acquired)
	require.Equal(t, 1, slots.released)
	require.NotEmpty(t, costs, "unavailable usage is still marked as an incomplete cost")
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "input_image", gjson.GetBytes(body, "input.0.content.1.type").String())
	require.Equal(t, "data:image/jpeg;base64,frameC", gjson.GetBytes(body, "input.0.content.3.image_url").String())
	require.NotContains(t, gjson.GetBytes(body, "input.0.content.0.text").String(), "secretGeneratedCode")
	for _, material := range cfg.CreativeRound.Materials {
		require.Contains(t, gjson.GetBytes(body, "input.0.content.0.text").String(), material.Value)
	}
	svc.renderCreative = func(context.Context, string) (*pelicanCreativeFrames, error) { return nil, fmt.Errorf("unavailable") }
	grade, _ = svc.assessCreative(context.Background(), 1, cfg, "<svg></svg>")
	require.Equal(t, "unknown", grade.Status)
	require.Nil(t, grade.Score)
	require.Len(t, upstream.requests, 1, "render failures cannot call a blind judge")
}

func TestCreativeJudgeFramesForChatAndResponses(t *testing.T) {
	ctx := context.WithValue(context.Background(), qualityVisionInputKey{}, []string{"data:image/jpeg;base64,frame"})
	for _, chat := range []bool{true, false} {
		payload := createOpenAITestPayload("judge", false, "Compare frames")
		path := "input.0.content.1.image_url"
		if chat {
			payload = createOpenAIChatCompletionsTestPayload("judge", "Compare frames")
			path = "messages.0.content.1.image_url.url"
		}
		applyQualityVisionInput(ctx, payload, chat)
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		require.Equal(t, "data:image/jpeg;base64,frame", gjson.GetBytes(raw, path).String())
	}
}

func TestCreativeRendererSamplesRealAnimation(t *testing.T) {
	if _, err := creativeChromium(); err != nil {
		t.Skip("Chromium is provided by the production image")
	}
	for _, animated := range []bool{false, true} {
		animation := ""
		if animated {
			animation = `<animate attributeName="cx" values="100;850;100" dur="6s" repeatCount="indefinite"/>`
		}
		frames, err := renderPelicanCreative(context.Background(), `<html><body style="margin:0"><svg width="960" height="720"><rect width="960" height="720" fill="white"/><circle cx="100" cy="300" r="80" fill="red">`+animation+`</circle></svg></body></html>`)
		require.NoError(t, err)
		require.Len(t, frames.Images, 3)
		require.Equal(t, animated, frames.Motion)
		if !animated {
			require.Equal(t, frames.Hashes[0], frames.Hashes[1])
		}
	}
}
