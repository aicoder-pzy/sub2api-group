//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

type channelOperationsFixture struct {
	repo                                         *channelRepository
	channel, group, account, outsider, user, key int64
}

func newChannelOperationsFixture(t *testing.T) *channelOperationsFixture {
	t.Helper()
	ctx := context.Background()
	f := &channelOperationsFixture{repo: &channelRepository{db: integrationDB}}
	ch := &service.Channel{Name: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()), Status: service.StatusActive, BillingModelSource: service.BillingModelSourceRequested}
	require.NoError(t, f.repo.Create(ctx, ch))
	f.channel = ch.ID
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, fmt.Sprintf("operations-%d", f.channel)).Scan(&f.group))
	require.NoError(t, f.repo.SetGroupIDs(ctx, f.channel, []int64{f.group}))
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "Operations account", Platform: service.PlatformOpenAI, Credentials: map[string]any{"api_key": "synthetic-fixture-only"}})
	f.account = a.ID
	a = mustCreateAccount(t, integrationEntClient, &service.Account{Name: "Outside account", Platform: service.PlatformOpenAI})
	f.outsider = a.ID
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, f.account, f.group)
	require.NoError(t, err)
	u := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("operations-%d@test.invalid", f.channel)})
	f.user = u.ID
	k := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("operations-fixture-%d", f.channel), Name: "Operations fixture"})
	f.key = k.ID
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM ops_error_logs WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, f.user)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM channels WHERE id=$1`, f.channel)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, f.group)
	})
	return f
}
func (f *channelOperationsFixture) catalog(t *testing.T) *service.ChannelOperationsCatalog {
	t.Helper()
	out, err := f.repo.ChannelOperationsCatalog(context.Background(), f.channel, service.ChannelOperationsQuery{Page: 1, PageSize: 20, UpstreamPage: 1, Membership: "all"})
	require.NoError(t, err)
	return out
}
func (f *channelOperationsFixture) mutate(t *testing.T, m service.ChannelOperationsMutation) error {
	t.Helper()
	revision := f.catalog(t).Revision
	m.Revision = &revision
	_, err := f.repo.MutateChannelOperations(context.Background(), f.channel, m)
	return err
}
func (f *channelOperationsFixture) upstream(t *testing.T, name string) int64 {
	t.Helper()
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "create_upstream", Name: name}))
	var id int64
	require.NoError(t, integrationDB.QueryRow(`SELECT id FROM channel_operation_upstreams WHERE channel_id=$1 AND name=$2`, f.channel, name).Scan(&id))
	return id
}
func TestChannelOperationsMembershipValidationConflictAndDetach(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	u1 := f.upstream(t, "First")
	u2 := f.upstream(t, "Second")
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.outsider, UpstreamID: u1}), service.ErrChannelOperationsValidation)
	// A canonical channel's upstream cannot be attached through another channel.
	other := newChannelOperationsFixture(t)
	require.ErrorIs(t, other.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: other.account, UpstreamID: u1}), service.ErrChannelOperationsValidation)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u1}))
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2}), service.ErrChannelOperationsConflict)
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2, ExpectedUpstreamID: u1}), service.ErrChannelOperationsValidation)
	revision := f.catalog(t).Revision
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "update_upstream", UpstreamID: u1, Name: "Renamed"}))
	_, err := f.repo.MutateChannelOperations(ctx, f.channel, service.ChannelOperationsMutation{Action: "detach", AccountID: f.account, ExpectedUpstreamID: u1, Confirm: true, Revision: &revision})
	require.ErrorIs(t, err, service.ErrChannelOperationsConflict)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2, ExpectedUpstreamID: u1, Confirm: true}))
	// Current group edits do not erase stale memberships; they remain detachable.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, f.account)
	require.NoError(t, err)
	got := f.catalog(t)
	require.Len(t, got.Accounts, 1)
	require.False(t, got.Accounts[0].Eligible)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "detach", AccountID: f.account, ExpectedUpstreamID: u2, Confirm: true}))
	require.Empty(t, f.catalog(t).Accounts)
	var credentials string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials->>'api_key' FROM accounts WHERE id=$1`, f.account).Scan(&credentials))
	require.Equal(t, "synthetic-fixture-only", credentials)
}
func TestChannelOperationsConcurrentChangesHaveOneWinner(t *testing.T) {
	f := newChannelOperationsFixture(t)
	revision := f.catalog(t).Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"A", "B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := f.repo.MutateChannelOperations(context.Background(), f.channel, service.ChannelOperationsMutation{Action: "create_upstream", Name: name, Revision: &revision})
			results <- err
		}(name)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrChannelOperationsConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	require.Equal(t, int64(1), f.catalog(t).UpstreamTotal)
}
func TestChannelOperationsCatalogPaginationSoftDeleteAndChannelDelete(t *testing.T) {
	f := newChannelOperationsFixture(t)
	u1 := f.upstream(t, "A")
	_ = f.upstream(t, "B")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u1}))
	q := service.ChannelOperationsQuery{Page: 1, PageSize: 1, UpstreamPage: 2, Membership: "assigned", Search: fmt.Sprint(f.account)}
	page, err := f.repo.ChannelOperationsCatalog(context.Background(), f.channel, q)
	require.NoError(t, err)
	require.Len(t, page.Upstreams, 1)
	require.Equal(t, "B", page.Upstreams[0].Name)
	require.Equal(t, int64(2), page.UpstreamTotal)
	require.Equal(t, int64(1), page.Total)
	q.Page = 2
	page, err = f.repo.ChannelOperationsCatalog(context.Background(), f.channel, q)
	require.NoError(t, err)
	require.Empty(t, page.Accounts)
	require.Equal(t, int64(1), page.Total)
	_, err = integrationDB.Exec(`UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, f.account)
	require.NoError(t, err)
	page = f.catalog(t)
	require.Len(t, page.Accounts, 1)
	require.False(t, page.Accounts[0].Eligible)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "delete_upstream", UpstreamID: u1, Confirm: true}))
	require.Zero(t, f.catalog(t).Total)
	require.NoError(t, f.repo.Delete(context.Background(), f.channel))
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE id=$1`, f.account).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_groups WHERE account_id=$1`, f.account).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM channel_operation_upstreams WHERE channel_id=$1`, f.channel).Scan(&n))
	require.Zero(t, n)
}
func TestChannelOperationsMigrationReplayIsAdditive(t *testing.T) {
	f := newChannelOperationsFixture(t)
	u := f.upstream(t, "Kept")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u}))
	raw, err := migrations.FS.ReadFile("259_custom_channel_operations.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		tx, err := integrationDB.Begin()
		require.NoError(t, err)
		_, err = tx.Exec(string(raw))
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
	got := f.catalog(t)
	require.Equal(t, int64(2), got.Revision)
	require.Len(t, got.Accounts, 1)
	require.Equal(t, u, got.Accounts[0].UpstreamID)
}

func TestChannelOperationsStatisticsPreserveAttributionAndUnknowns(t *testing.T) {
	f := newChannelOperationsFixture(t)
	u := f.upstream(t, "Supplier")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u}))
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,channel_id,model,requested_model,upstream_model,model_mapping_chain,stream,first_token_ms,duration_ms,input_tokens,output_tokens,total_cost,actual_cost,account_stats_cost,account_rate_multiplier,created_at)
 SELECT $1,$2,$3,$4,$5,'recorded','public','actual','public -> actual',true,ttft,2000,100,10,2,1,3,.5,NOW()-INTERVAL '1 minute' FROM unnest(ARRAY[100,300,NULL]::integer[]) ttft`, f.user, f.key, f.account, f.group, f.channel)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO upstream_quality_samples(source,source_id,account_id,group_id,model,kind,verdict,reason,sampled_at) VALUES('operations_fixture','sample',$1,$2,'actual','pelican','unknown','fixture',NOW()-INTERVAL '1 minute')`, f.account, f.group)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM upstream_quality_samples WHERE account_id=$1`, f.account)
	})
	filter := service.ChannelOperationsFilter{ChannelID: f.channel, UpstreamID: u, GroupID: f.group,
		From: time.Now().Add(-time.Hour), Until: time.Now(), Model: "public", ModelScope: "public", Dimension: "upstream", Page: 1, PageSize: 20}
	read := func() *service.ChannelOperationsSnapshot {
		snapshot := f.repo.ReadChannelOperations(ctx, filter)
		for _, section := range []service.ChannelOperationsSection{snapshot.Usage, snapshot.Errors, snapshot.Capacity} {
			require.Equal(t, "ready", section.Status, section.Error)
		}
		return snapshot
	}
	data := read()
	var usage struct {
		Summary struct {
			Requests, Samples int
			Unknown           int      `json:"unknown_samples"`
			P50               *float64 `json:"p50_ms"`
			Cost              float64  `json:"account_cost"`
		}
		Rows []struct {
			ID int64 `json:"dimension_id"`
		}
	}
	require.NoError(t, json.Unmarshal(data.Usage.Data, &usage))
	require.Equal(t, 3, usage.Summary.Requests)
	require.Equal(t, 2, usage.Summary.Samples)
	require.Equal(t, 1, usage.Summary.Unknown)
	require.InDelta(t, 200, *usage.Summary.P50, .001, "percentile uses raw samples, not averages of account percentiles")
	require.InDelta(t, 4.5, usage.Summary.Cost, .001)
	require.Len(t, usage.Rows, 1)
	require.Equal(t, u, usage.Rows[0].ID)
	require.Equal(t, "unsupported_scope", data.Quality.Status, "do not invent test-to-public alias correspondence")
	filter.Model, filter.ModelScope = "actual", "upstream"
	data = read()
	require.Equal(t, "ready", data.Quality.Status, data.Quality.Error)
	var quality struct{ Summary struct{ Unknown int } }
	require.NoError(t, json.Unmarshal(data.Quality.Data, &quality))
	require.Equal(t, 1, quality.Summary.Unknown)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM channel_groups WHERE channel_id=$1`, f.channel)
	require.NoError(t, err)
	data = read()
	require.NoError(t, json.Unmarshal(data.Usage.Data, &usage))
	require.Equal(t, 3, usage.Summary.Requests, "historical channel attribution survives current group removal")
	filter.From = filter.Until
	data = read()
	require.NoError(t, json.Unmarshal(data.Usage.Data, &usage))
	require.Zero(t, usage.Summary.Requests)
	require.Nil(t, usage.Summary.P50, "a successful empty query is distinct from a failed query")
}
