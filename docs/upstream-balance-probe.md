# Upstream balance probing

API-key accounts expose balance controls in the account list. Global periodic
probe settings live under Settings > Gateway. Manual and batch probes only call
read-only usage endpoints; they never submit model inference requests.

Supported protocols:

| Provider | Endpoint | Data |
| --- | --- | --- |
| Sub2API | `GET /v1/usage` | Wallet balance, key quota, subscription remaining and 5h/1d/7d limits, as exposed by the upstream |
| NewAPI | `GET /api/usage/token/` | Key quota; unlimited keys stay unlimited |
| NewAPI with management authorization | `GET /api/status`, `GET /api/user/self`, `GET /api/token/` | Verified user wallet and available key quota |

Wallet balance and key quota are different scopes. A quota-only endpoint cannot
reveal the upstream user's wallet. Missing amounts are unknown, never zero.
Without management authorization, NewAPI raw quota defaults to 500000 units per USD; configure the upstream's
actual conversion and currency per account. An explicit response currency
means the response is already expressed in that currency.

Auto detection tries Sub2API and then NewAPI on missing endpoints or incompatible
successful schemas. HTTP rejection and transport failure stop the attempt.
Requests reuse the account proxy, TLS profile and header overrides, disable
redirects, and enforce a 10-second timeout and 64 KiB response limit.

Periodic probing defaults to every 30 minutes and requires both the global and
account switch. Batch probing accepts at most 20 accounts and uses the existing
bounded probe worker slots. The low-balance threshold produces an account-list
warning in each amount's currency. External notifications are separately opt-in.

Automatic exhaustion pause is opt-in per account. Only a successful, fresh,
limited amount at or below zero can pause scheduling. Failed, unsupported,
stale, malformed, missing or unlimited data cannot pause an account. A rolling
window stops blocking after its reset time. Data stays fresh for twice the probe
interval. A later positive probe restores eligibility without changing the
account's manual schedulable switch or replacing a healthy current binding.

Last successful amounts remain visible after failures with an explicit failure
status and timestamp. Changing credentials, proxy identity or conversion clears
the old snapshot. Atomic writes reject results obtained using old credentials
or account configuration.

## NewAPI wallets

Open an API-key account's balance configuration and select **NewAPI Wallet
Authorization**. Supply the upstream user ID and the **system access token**
from that user's personal settings. A model API key is not a management token.
Preview the wallet and key ownership, then verify and save. Multiple accounts
at the same site can share one encrypted authorization, provided that user owns
each selected key. Saving revalidates every account and commits the bindings
atomically. An ambiguous masked key requires selecting a token and verification
through NewAPI's read-only `POST /api/token/:id/key` key retrieval endpoint.

The wallet is `/api/user/self.quota` divided by the positive `quota_per_unit`
published by `/api/status`, displayed as **normalized USD**. This is not an
exchange-rate conversion. Unlimited key quota never substitutes for wallet
balance. A key's finite quota remains a separate scheduling constraint when
the management API exposes it. This migration does not change local pricing.

Authorizations are scoped to scheme, host, effective port, tenant path and user
ID. Default ports, trailing slashes and a final `/v1` are normalized. Different
users and tenant paths never share credentials. Credentials use the existing
AES-GCM encryptor and require a fixed `TOTP_ENCRYPTION_KEY`; preserve that key
when backing up or moving the deployment. Tokens are never returned by the
configuration API or included in audit bodies. Clearing a binding affects only
that account. Credential edits invalidate bindings; authorization rotations
invalidate snapshots and reject pending writes using old revisions.

## Balance notifications

Settings > Gateway > Upstream Balance Probing contains **Balance Notifications**.
Notifications default to off. Configure up to six destinations: SMTP email,
WeCom, DingTalk, Feishu or a public HTTPS Webhook. Email uses existing SMTP
settings. Robot URLs and signing secrets are encrypted and never returned to
the browser. Save a channel before sending its test notification. Tests send
only a sample notification; they do not call model endpoints.

The global balance threshold applies in each amount's currency. Account balance
configuration supports opting out and overriding the threshold. Wallet, key,
subscription and rolling-window amounts are evaluated independently. Only a
fresh, finite, successful amount can start a low-balance event or confirm its
recovery. Query failures, absent amounts, unlimited quotas and expired windows
are unknown; they never become zero or trigger a recovery notification.

Each low-balance episode and its recovery are persisted once. A continuously
low amount does not generate repeated notices. A later decline starts a new
episode. Disabling recovery notices keeps recovery tracking active. Changes to
account identity or thresholds start a new baseline rather than reporting a
recovery from unrelated data. Pending events are rechecked against current
account state and configuration before delivery. Unknown results defer sending;
obsolete events are cancelled.

Delivery history shows each destination's status. Only failed destinations are
retried, at five-minute intervals, with at most three delivery attempts.
Database leases prevent concurrent workers from claiming the same event. A
crash between an external send and receipt persistence can cause a duplicate;
generic Webhooks include `event_id` for recipient-side deduplication. Robot
rate reservations are shared between instances, with a 3.1-second minimum gap
per destination. Requests use a public-address-checking transport, refuse
redirects and limit time and response size.

Generic Webhooks receive a JSON object with `text`, `event` (`low`, `recovery`
or `test`), `event_id`, `account_id`, `account_name`, `scope`, `currency`,
`remaining`, `threshold` and `observed_at`. An optional signing secret produces
`X-Sub2API-Signature: sha256=<HMAC-SHA256 hex of the exact body>`.

Migration `245_custom_new_api_wallet_notifications.sql` adds authorization,
binding, notification-monitor, event and rate-limit tables. It preserves
existing balances and scheduling settings. Older binaries can ignore the new
tables; binary rollback does not undo database migrations.

Adapted from [ranxi2001/sub2api v2.10.1](https://github.com/ranxi2001/sub2api/tree/v2.10.1),
especially its NewAPI shared authorization and balance notification behavior.
The migration uses this custom branch's existing balance runner and amount
schema. It does not import the reference fork's scheduler or OAuth quota rules.
