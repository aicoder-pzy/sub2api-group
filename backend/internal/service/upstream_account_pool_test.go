package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectVulcanPool(t *testing.T) {
	const raw = `{"updated_at":"2026-11-19T13:21:09Z","groups":[
		{"id":1,"name":"source A","account_count":1,"accounts":[{"id":"hidden-token","email_masked":"u***@example.test","account_type":"oauth","plan_type_label":"Pro 5x","status":"available","used_percent_5h":20,"usage_updated_at":"2026-11-19T13:18:00Z"}]},
		{"id":2,"name":"source B","account_count":2,"accounts":[{"id":"hidden-token","account_type":"oauth","plan_type_label":"Pro 5x","status":"available","used_percent_5h":23,"usage_updated_at":"2026-11-19T13:20:00Z","current_concurrency":0,"concurrency_limit":2},{"id":"second-account","account_type":"oauth","status":"temp_unschedulable"}]}
	]}`
	snapshot, err := collectVulcanPool([]byte(raw), nil)
	require.NoError(t, err)
	require.Equal(t, "2026-11-19T13:21:09Z", snapshot.Updated.Format(time.RFC3339))
	require.Len(t, snapshot.Rows, 2)
	require.Len(t, snapshot.Groups, 2)
	require.Equal(t, []string{snapshot.Groups[0].ID, snapshot.Groups[1].ID}, snapshot.Rows[0].Groups)
	require.Equal(t, 2, snapshot.Groups[1].Count)
	require.Equal(t, "Pro 5x", snapshot.Rows[0].Plan)
	require.Equal(t, "active", snapshot.Rows[0].Status)
	require.Equal(t, 23.0, *snapshot.Rows[0].Windows[0].Used)
	require.Equal(t, "2026-11-19T13:20:00Z", snapshot.Rows[0].Admin.UsageUpdated.Format(time.RFC3339))
	require.Nil(t, snapshot.Rows[0].LastUsed)
	require.Nil(t, snapshot.Rows[1].Admin.UsageUpdated)
	require.Equal(t, "temp_unschedulable", snapshot.Rows[1].Status)
	encoded, err := json.Marshal(snapshot.display(nil, true))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hidden-token")
	require.NotContains(t, string(encoded), "example.test")
	other, err := collectVulcanPool([]byte(raw), snapshot.Groups)
	require.NoError(t, err)
	require.Equal(t, snapshot, other)
	_, err = collectVulcanPool([]byte(strings.Replace(raw, `"account_count":1`, `"account_count":2`, 1)), nil)
	require.ErrorIs(t, err, errPoolChanged)
}

func TestVulcanAccountPoolPublicOnly(t *testing.T) {
	site := &UpstreamSite{BaseURL: vulcanPoolBaseURL, Kind: "sub2api", Purpose: "public"}
	require.True(t, accountPoolEnabled(site))
	site.Purpose = "private"
	require.False(t, accountPoolEnabled(site))
}

func TestAccountPoolRetryDelay(t *testing.T) {
	now := time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)
	require.Equal(t, 45*time.Second, accountPoolRetryDelay("45", now))
	require.Equal(t, 70*time.Second, accountPoolRetryDelay(now.Add(70*time.Second).Format(http.TimeFormat), now))
	require.Equal(t, 6*time.Second, accountPoolRetryDelay("0", now))
	require.Equal(t, time.Minute, accountPoolRetryDelay("", now))
}

func TestCollectAccountPool(t *testing.T) {
	get := func(page2ID int, page2Total int) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			u, err := url.Parse(path)
			require.NoError(t, err)
			page, err := strconv.Atoi(u.Query().Get("page"))
			require.NoError(t, err)
			width, err := strconv.Atoi(u.Query().Get("page_size"))
			require.NoError(t, err)
			items := make([]map[string]any, 0, width)
			add := func(id int) {
				item := map[string]any{
					"id": id, "display_name": "account@example.test", "credentials": "not-for-display",
					"platform": "openai", "plan_label": "Pro", "status": "active", "concurrency": 4,
					"auth_type": "oauth", "current_concurrency": page, "last_used_at": "2026-11-21T03:14:45Z",
					"groups": []map[string]any{{"id": 37, "name": "original-pool", "rate_multiplier": 2.4}, {"id": 37, "name": "original-pool"}},
				}
				if id == 101 {
					item["account_rate_enabled"], item["rate_multiplier"] = true, 0.12
				}
				items = append(items, item)
			}
			const total = 101
			if page < 1 || page > 2 {
				return nil, fmt.Errorf("unexpected page: %d", page)
			}
			for i := (page-1)*width + 1; i <= min(page*width, total); i++ {
				if i == total && page2ID != total {
					add(width) // An account from the previous page repeats at the boundary.
				} else {
					add(i)
				}
			}
			reported := total
			if page == 2 {
				reported = page2Total
			}
			return json.Marshal(map[string]any{"page": page, "page_size": width, "total": reported, "pages": 2, "items": items})
		}
	}
	first, err := collectAccountPool(get(101, 101), nil)
	require.NoError(t, err)
	require.Len(t, first.Rows, 101)
	require.Len(t, first.Groups, 1)
	poolID := first.Groups[0].ID
	require.Equal(t, 101, first.Groups[0].Count)
	second, err := collectAccountPool(get(101, 101), first.Groups)
	require.NoError(t, err)
	require.Equal(t, poolID, second.Groups[0].ID)
	aliases := map[string]string{poolID: "Display label"}
	public := second.display(aliases, false)
	require.Equal(t, "Display label", public.Pools[0].Name)
	require.Equal(t, []string{poolID}, public.Rows[0].Groups)
	data, err := json.Marshal(public)
	require.NoError(t, err)
	for _, hidden := range []string{"original-pool", "source_id", "credentials", "not-for-display", "@example.test", "multiplier"} {
		require.NotContains(t, string(data), hidden)
	}
	admin := second.display(aliases, true)
	require.Equal(t, "original-pool", admin.Pools[0].Original)
	require.Equal(t, "1", admin.Rows[0].Admin.SourceID)
	require.Equal(t, "account@example.test", admin.Rows[0].Admin.Name)
	require.Equal(t, "oauth", admin.Rows[0].Admin.Type)
	require.Equal(t, 2.4, admin.Rows[0].Admin.Rates[poolID])
	require.Equal(t, 0.12, *admin.Rows[100].Admin.AccountRate)
	require.NotNil(t, admin.Rows[0].Admin.ObservedAt)
	require.Equal(t, "2026-11-21T03:14:45Z", admin.Rows[0].LastUsed.Format(time.RFC3339))
	require.Nil(t, public.Rows[0].Admin)
	calls := 0
	recovered, err := collectAccountPool(func(path string) ([]byte, error) {
		calls++
		if calls <= 2 {
			return get(100, 101)(path)
		}
		return get(101, 101)(path)
	}, first.Groups)
	require.NoError(t, err)
	require.Equal(t, 5, calls)
	require.Len(t, recovered.Rows, 101)
	require.Equal(t, 101, recovered.Groups[0].Count)
	require.Equal(t, 1, *recovered.Rows[99].Current)
	for _, edge := range []struct{ id, total int }{{100, 101}, {101, 102}} {
		incomplete, err := collectAccountPool(get(edge.id, edge.total), first.Groups)
		require.ErrorIs(t, err, errPoolChanged)
		require.Nil(t, incomplete.Updated)
		require.Equal(t, 101, first.Groups[0].Count)
	}
}
