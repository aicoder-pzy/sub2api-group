# Upstream balance probing

API-key accounts expose balance controls in the account list. Global periodic
probe settings live under Settings > Gateway. Manual and batch probes only call
read-only usage endpoints; they never submit model inference requests.

Supported protocols:

| Provider | Endpoint | Data |
| --- | --- | --- |
| Sub2API | `GET /v1/usage` | Wallet balance, key quota, subscription remaining and 5h/1d/7d limits, as exposed by the upstream |
| NewAPI | `GET /api/usage/token/` | Key quota; unlimited keys stay unlimited |

Wallet balance and key quota are different scopes. A quota-only endpoint cannot
reveal the upstream user's wallet. Missing amounts are unknown, never zero.
NewAPI raw quota defaults to 500000 units per USD; configure the upstream's
actual conversion and currency per account. An explicit response currency
means the response is already expressed in that currency.

Auto detection tries Sub2API and then NewAPI on missing endpoints or incompatible
successful schemas. HTTP rejection and transport failure stop the attempt.
Requests reuse the account proxy, TLS profile and header overrides, disable
redirects, and enforce a 10-second timeout and 64 KiB response limit.

Periodic probing defaults to every 30 minutes and requires both the global and
account switch. Batch probing accepts at most 20 accounts and uses the existing
bounded probe worker slots. The low-balance threshold produces an account-list
warning in each amount's currency; it does not send external notifications.

Automatic exhaustion pause is opt-in per account. Only a successful, fresh,
limited amount at or below zero can pause scheduling. Failed, unsupported,
stale, malformed, missing or unlimited data cannot pause an account. A rolling
window stops blocking after its reset time. Data stays fresh for twice the probe
interval. A later positive probe restores eligibility without changing the
account's manual schedulable switch or replacing a healthy current binding.

Last successful amounts remain visible after failures with an explicit failure
status and timestamp. Changing credentials, proxy identity or conversion clears
the old snapshot. Atomic writes reject results obtained using old credentials
or account configuration. No schema migration is needed.
