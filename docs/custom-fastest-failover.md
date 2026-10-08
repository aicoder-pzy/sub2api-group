# Custom Fastest Failover

This mode keeps one active upstream channel per group and requested model. Its
purpose is continuity through channel faults. Successful requests do not move to
another channel for a lower price, better score, higher priority, or spare capacity.
The policy applies to every group that enables fastest failover.

- An eligible current channel remains first, including after an older preferred
  channel finishes cooling down. Reliability, preferences and priority choose the
  initial channel and backups, or the winner of an administrative refresh.
- Local concurrency exhaustion uses the existing bounded wait. It does not mark
  a channel as faulty or activate automatic load balancing.
- Existing upstream fault classification remains in effect. Retryable failures
  receive at most one same-channel retry, while an explicit zero retry count still
  disables retries. Client cancellation and request errors do not become channel
  failures. An attempt timeout does not receive an additional same-channel retry.
  Native HTTP retries and handler retries share a per-account submission counter.
- Selection does not update the active binding. A backup must complete an upstream
  request successfully before it can become the active channel. Interrupted or
  failed responses and canceled clients cannot commit the candidate.
- The request captures a binding revision during selection. Redis atomically
  checks that revision before committing a successful backup. Concurrent failures,
  delayed completions, binding deletion, and manual refresh cannot overwrite a
  newer choice. A manual refresh increments the revision even if it selects the
  same channel again.
- Before meaningful stream output, failover can replace a failed attempt. After
  output has reached the client, the request cannot concatenate another channel's
  answer. A failed attempt is handled by the existing cooldown and error policy;
  subsequent requests may select a backup.
- Grok Responses streams ending with no output, usage, or error are treated as
  empty upstream failures, following
  [upstream PR #7775](https://github.com/Wei-Shaw/sub2api/pull/7775).

Backup ranking uses 24 hours of complete-response evidence: measured reliable
channels, limited successful evidence, unknown channels, then degraded channels.
Within a tier, failure rate (when both meet the sample threshold), failures in the
last 15 minutes, explicit preference, configured priority, P95 latency, and ID break
ties. Prices do not participate. Unknown evidence is never displayed as 100%
success. A single successful probe does not erase a measured bad failure rate.

Global settings default to first output 60 seconds, stream idle 120 seconds,
model cooldown 120 seconds, shared pre-output attempt budget 120 seconds, and
minimum reliable sample count 10. All five are editable in Settings. The shared
budget starts at the first upstream send, survives account changes, and stops
applying after meaningful output. Its expiration does not cool the last channel.
Gemini text forwarding shares these limits; countTokens and media generation keep
their existing behavior. Existing provider limits and cooldowns remain separate.

An ambiguous Grok HTTP 403 receives at most one alternate channel attempt for the
same request. It does not alter account health or quota. Explicit credentials,
entitlement, quota, admin rules and content-policy errors retain their existing
classification. A real fault on the alternate still contributes to its health.

Manual **Refresh Scheduling** probes each currently eligible text channel, retests
transient failures once, and combines successful probes with historical quality.
Only a completed successful probe can win. If all probes fail, the old binding
is retained. **Scheduling Status** is a read-only view of the active channel,
backup ranking, evidence, recent faults, cooldown and last binding transition;
opening it sends no model requests and does not change the binding.

Fastest-failover HTTP forwarding can use the proxy's existing configured fallback
chain. At most four backup records are visited; duplicate endpoints, cycles,
disabled, expired and (for OpenAI) quarantined proxies are skipped. Direct egress
requires an explicit `direct` fallback mode. Another egress may be tried only
after an independently traced persistent failure before HTTP receives a
connection, with a rewindable request body and no response. Unknown submission
state never permits egress replay. BPS/ticket and handled plugin routes retain
their own egress rules and are not replayed by this native HTTP helper.

Bindings retain the existing numeric Redis value. A companion `:revision` key
provides concurrency control; old bindings without it start at revision zero.
A `:transition` value is committed atomically with the binding and revision and
records initial selection, confirmed failover or manual refresh.
There is no database migration, and the previous application can still read the
bindings. No periodic traffic exploration or automatic return to a recovered
preferred channel is added.
