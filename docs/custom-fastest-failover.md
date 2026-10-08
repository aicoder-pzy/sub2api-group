# Custom Fastest Failover

This mode keeps one active upstream channel per group and requested model. Its
purpose is continuity through channel faults. Successful requests do not move to
another channel for a lower price, better score, higher priority, or spare capacity.
The policy applies to every group that enables fastest failover.

- An eligible current channel remains first, including after an older preferred
  channel finishes cooling down. Preferences and scores choose the initial channel
  and backups, or the winner of an explicit administrative scheduling refresh.
- Local concurrency exhaustion uses the existing bounded wait. It does not mark
  a channel as faulty or activate automatic load balancing.
- Existing upstream fault classification remains in effect. Retryable failures
  receive at most one same-channel retry, while an explicit zero retry count still
  disables retries. Client cancellation and request errors do not become channel
  failures. An attempt timeout does not receive an additional same-channel retry.
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

Global settings remain first output 60 seconds, stream idle 120 seconds, and
model cooldown 120 seconds by default. Existing provider-specific rate limits and
cooldowns remain separate. Manual **Refresh Scheduling** still actively evaluates
the group's eligible channels and explicitly replaces the binding with its winner.

Bindings retain the existing numeric Redis value. A companion `:revision` key
provides concurrency control; old bindings without it start at revision zero.
There is no database migration, and the previous application can still read the
bindings. No periodic traffic exploration or automatic return to a recovered
preferred channel is added.
