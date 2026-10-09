//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPelicanAssessmentRepo_PersistenceAndStaleUpdates(t *testing.T) {
	ctx := context.Background()
	repo := NewPelicanAssessmentRepository(integrationDB)
	hash := fmt.Sprintf("%064x", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM pelican_html_assessments WHERE html_hash=$1`, hash)
	})
	initial := &service.PelicanAssessmentRecord{Hash: hash, Status: "running", TaskID: "first-task", Deadline: time.Now().Add(time.Minute)}
	require.NoError(t, repo.Save(ctx, initial))
	other := *initial
	other.TaskID = "another-task"
	require.NoError(t, repo.Save(ctx, &other))
	loaded, err := repo.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "first-task", loaded.TaskID)
	initial.Status = "failed"
	require.NoError(t, repo.Finish(ctx, initial))
	require.NoError(t, repo.Save(ctx, &other))
	initial.Status = "succeeded"
	initial.Assessment = &service.PelicanAssessment{Quality: "degraded"}
	require.NoError(t, repo.Finish(ctx, initial))
	loaded, err = repo.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "running", loaded.Status)
	require.Nil(t, loaded.Assessment)
	other.Status = "succeeded"
	other.Assessment = &service.PelicanAssessment{Quality: "normal", Reason: "source assessment", Source: "classifier:test", CheckedAt: time.Now().UTC()}
	require.NoError(t, repo.Finish(ctx, &other))
	require.NoError(t, repo.Save(ctx, initial))
	loaded, err = repo.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "normal", loaded.Assessment.Quality)
	require.Equal(t, "another-task", loaded.TaskID)
}
