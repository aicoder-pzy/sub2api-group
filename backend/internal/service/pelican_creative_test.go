package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreativePoolAndReproducibleRounds(t *testing.T) {
	pool := pelicanCreativePool()
	require.Len(t, pool.Categories, 12)
	total := 0
	keys := map[string]bool{}
	for _, category := range pool.Categories {
		require.False(t, keys[category.Key])
		keys[category.Key] = true
		require.NotEmpty(t, category.Label)
		require.GreaterOrEqual(t, len(category.Values), 18)
		values := map[string]bool{}
		for _, value := range category.Values {
			require.NotEmpty(t, strings.TrimSpace(value))
			require.False(t, values[value], "duplicate in %s: %s", category.Key, value)
			values[value] = true
			total++
		}
	}
	require.Equal(t, 541, total)
	first := newPelicanCreativeRound(groupTestNow)
	same := newPelicanCreativeRound(groupTestNow.Add(29 * time.Minute).In(time.FixedZone("UTC+8", 8*3600)))
	require.Equal(t, first, same, "replicas and restarts draw the same round without process state")
	require.NotEqual(t, first.Materials, newPelicanCreativeRound(groupTestNow.Add(30*time.Minute)).Materials)
	for i, material := range first.Materials {
		require.Contains(t, pool.Categories[i].Values, material.Value)
		require.Contains(t, first.Prompt, material.Label+"："+material.Value)
	}
}

func TestCreativeGroupRunsShareQuestionAcrossSamplesFailoverAndGroups(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	generate := func(_ context.Context, id int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		mu.Lock()
		prompts = append(prompts, cfg.Prompt)
		mu.Unlock()
		if id == 11 {
			return &ScheduledTestResult{Status: "failed", ErrorMessage: "HTTP 503"}, nil
		}
		_, checks, err := withPelicanQualityChecks(&PelicanTestConfig{PromptMode: pelicanRandomCreative, CreativeRound: cfg.CreativeRound, VerificationTasks: true})
		require.NoError(t, err)
		output := fmt.Sprintf(`<html><body><svg></svg><!--quality-check:%s:{"paths":%d,"code":%d}--></body></html>`, checks.nonce, checks.paths, checks.code)
		return &ScheduledTestResult{Status: "success", ResponseText: output}, nil
	}
	plan := groupTestPlan(2)
	plan.PelicanConfig.PromptMode = pelicanRandomCreative
	plan.PelicanConfig.VerificationTasks = true
	repo := newGroupTestRepoFake()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}, {account: account(13, "c")}}}
	svc, gallery := newGroupTestService(repo, router, generate)
	svc.assessOutput = func(context.Context, string) (string, *PelicanAssessment) {
		t.Error("the fixed Pelican classifier must not assess a creative scene")
		return "", nil
	}
	// Queued work crosses the half-hour boundary, but keeps the claim's round.
	claimedAt := groupTestNow.Add(29 * time.Minute)
	svc.now = func() time.Time { return groupTestNow.Add(31 * time.Minute) }
	svc.execute(plan, claimedAt.Add(pelicanGroupTestLease))
	require.Len(t, repo.results, 2)
	require.Len(t, gallery.published, 2)
	require.Nil(t, plan.PelicanConfig.CreativeRound, "the stored plan is not mutated")
	for _, result := range repo.results {
		require.Equal(t, "normal", result.QualityVerdict, "artistic rule misses do not override objective checks")
		require.Len(t, result.PelicanConfig.CreativeRound.Checks.Missing, 4)
		require.Equal(t, newPelicanCreativeRound(claimedAt).ID, result.PelicanConfig.CreativeRound.ID)
		require.Equal(t, result.PelicanConfig.Prompt, result.PelicanConfig.CreativeRound.Prompt)
	}
	other := groupTestPlan(1)
	other.ID, other.GroupID = 8, 5
	other.PelicanConfig.PromptMode, other.PelicanConfig.VerificationTasks = pelicanRandomCreative, true
	otherSvc, _ := newGroupTestService(newGroupTestRepoFake(), &groupTestRouterFake{steps: []routeStep{{account: account(14, "d")}}}, generate)
	otherSvc.groups = groupTestGroupsFake{5: {ID: 5, Status: StatusActive}}
	otherSvc.execute(other, groupTestNow.Add(10*time.Minute).Add(pelicanGroupTestLease))
	require.Len(t, prompts, 4)
	for _, prompt := range prompts {
		require.Equal(t, prompts[0], prompt)
	}
	require.Equal(t, repo.results[0].PelicanConfig.CreativeRound, gallery.published[0].snapshot.CreativeRound)
}

func TestCreativeSyntaxChecksAreConservativeHints(t *testing.T) {
	round := newPelicanCreativeRound(groupTestNow)
	var labels strings.Builder
	for _, material := range round.Materials {
		labels.WriteString(material.Value)
	}
	good := `<html><head><style>@keyframes move {to {transform:translateX(1px)}} svg {animation:move 1s infinite}</style></head><body><svg><desc>` + labels.String() + `</desc></svg></body></html>`
	checks := checkPelicanCreative(good, round)
	require.True(t, checks.HasSVG && checks.Animated && checks.Complete && checks.NoExternal)
	require.Len(t, checks.Matched, 4)
	for _, external := range []string{`<script src="/external.js"></script>`, `<style>p {background:url('https://example.com/x')}</style>`, `<script>fetch('/api')</script>`} {
		require.False(t, checkPelicanCreative(strings.Replace(good, "</body>", external+"</body>", 1), round).NoExternal)
	}
	checks = checkPelicanCreative(`<html><body><svg><!--`+labels.String()+`--></svg>`, round)
	require.False(t, checks.Complete)
	require.False(t, checks.Animated)
	require.Len(t, checks.Missing, 4, "a copied prompt in a comment is not material text")
}
