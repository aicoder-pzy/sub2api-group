package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectAccountPool(t *testing.T) {
	get := func(page2ID int, page2Total int) func(string) ([]byte, error) {
		return func(path string) ([]byte, error) {
			u, err := url.Parse(path)
			require.NoError(t, err)
			page, err := strconv.Atoi(u.Query().Get("page"))
			require.NoError(t, err)
			require.Equal(t, "100", u.Query().Get("page_size"))
			items := make([]map[string]any, 0, 100)
			add := func(id int) {
				items = append(items, map[string]any{
					"id": id, "display_name": "account@example.test", "credentials": "not-for-display",
					"platform": "openai", "plan_label": "Pro", "status": "active", "concurrency": 4,
					"groups": []map[string]any{{"id": 37, "name": "original-pool", "effective_multiplier": 2.4}, {"id": 37, "name": "original-pool"}},
				})
			}
			total := 101
			switch page {
			case 1:
				for i := 1; i <= 100; i++ {
					add(i)
				}
			case 2:
				add(page2ID)
				total = page2Total
			default:
				return nil, fmt.Errorf("unexpected page: %d", page)
			}
			return json.Marshal(map[string]any{"page": page, "page_size": 100, "total": total, "pages": 2, "items": items})
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
	require.Equal(t, "original-pool", second.display(aliases, true).Pools[0].Original)
	for _, edge := range []struct{ id, total int }{{100, 101}, {101, 102}} {
		incomplete, err := collectAccountPool(get(edge.id, edge.total), first.Groups)
		require.ErrorIs(t, err, errPoolChanged)
		require.Nil(t, incomplete.Updated)
		require.Equal(t, 101, first.Groups[0].Count)
	}
}
