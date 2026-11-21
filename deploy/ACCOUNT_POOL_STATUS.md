# Account pool status API

In Sub2, open **Upstream sites → Account pools → Status API**. Refresh the
site snapshot if needed, then select the upstream group, enter public source
and pool names, set plan aliases, and select the published plans. Selecting
**Do not publish** and saving removes that site's entry. Each supported site
has one published group; the same status response includes all configured
sites. Current collectors support Shenji and Qiu API (Vulcan).

Set `SUB2_ACCOUNT_POOL_STATUS_TOKEN` in the Sub2 application container to a
separate randomly generated bearer token of at least 32 characters. The
provided Compose files read this value from `.env`; deployments using older
Compose files can pass it through in an override. The API is disabled without
it. Keep the token on the downstream server and have that server render or
relay the status for its page.

```http
GET /api/v1/integrations/account-pool-status?page=1&page_size=20
Authorization: Bearer <status-token>
```

The success envelope contains `data.pools`, `data.page`, and `data.page_size`.
Each pool has an opaque `id`, `source`, `name`, `updated`, `snapshot_available`,
`total`, `plans` and `statuses` (counts by label), `pages`, and paginated
`rows`. Each row contains a display number, platform, mapped plan and upstream
status. Page parameters apply independently to each pool and totals cover all
matching rows. For example, a Qiu API `Pro 500 → Pro Max` alias is applied
before testing whether `Pro Max` is selected for publication.

`snapshot_available: false` means there is no snapshot for the current site
connection or group. Do not present it as an empty healthy pool. A `true`
value means the timestamped snapshot can be displayed; it does not guarantee
the data is recent. Reads never refresh a site, send model requests, return
credentials, or update routing. Use the existing admin refresh action to
update snapshots.

The admin account-pool tab shows all collected rows before any publication
filters. Its Recent activity column uses Shenji `last_used_at` (or the later
observation of positive concurrency) or Qiu API `usage_updated_at`. It sorts
newest first, and the tooltip identifies the observation or source field.
On opening the tab, Sub2 starts a refresh if the last successful collection
is at least 15 minutes old or no snapshot exists. Shenji pages are reconciled
by account ID, with a second look around moving page boundaries. Sub2 keeps
the previous snapshot if it cannot account for the reported total.
Admin-only account details and Qiu quota update times are excluded from all
integration responses.
