# Global relay network optimization

All groups and accounts use the same HTTP forwarding policy. There are no
account/group compression switches and no database migration.

Large JSON POSTs to Responses, Chat Completions, and Messages are eligible for
gzip level 1 after conversion. Bodies below 256 KiB, above 16 MiB, already coded,
signed, multipart, upgraded, or with trailers retain their original transport.
Only an encoding saving at least 10% is sent. Concurrent encoding is bounded;
saturation sends identity without queueing requests. Payload bytes after gzip
decoding are identical to the final converted request.

Each endpoint/credential/transport route automatically learns gzip compatibility
with four small control requests. Complete and truncated JSON contain no model
identifier or user content. Both identity and gzip must produce the distinct
missing-model and parse/stream-gate validation results. A generic error, auth
failure, redirect, or timeout cannot enable compression. Unknown business calls
continue normally while a bounded background probe runs; this adds control-plane
validation traffic, with at most two probes concurrently and eight seconds per
sequence. Positive results live for 24 hours, other results for ten minutes.
The in-process cache is bounded to 512 entries and contains hashed identities.
Rotation of credentials, endpoint, or proxy route gets a fresh capability result.

A subsequent 415 disables gzip temporarily and retries the unconsumed original
body once because the HTTP representation was rejected. Other status codes,
transport errors, and ambiguous failures do not add retries. Existing caller
retry/failover policies remain authoritative. Response compression negotiation
and SSE flushing are unchanged; request gzip does not enable SSE gzip.

For an operational rollback, set `SUB2API_UPSTREAM_GZIP=off` on the application
and roll out that environment with the normal drain procedure. The default is
automatic global optimization. This setting is operational, not an account UI
or a prerequisite for each new upstream.

Access logs now preserve original inbound encoding and measured body bytes,
read/decode time, and first response body write / Flush time. Shared upstream
logs record connection readiness/reuse, completed request write, first response
byte, and response headers, relative to each physical attempt. Failed/unobserved
stages are absent. Generated gzip sizes are body-buffer sizes, not measured TLS
or network-interface traffic. No prompts, response content, keys, or raw
credential-cache keys are logged. An application Flush is not proof of remote
receipt and a first body write is not necessarily a semantic token.

The large control-free JSON normalization path comes from ranxi2001/sub2api
PR #716. Differential tests retain the original state machine, with exhaustive
adjacent-byte/offset coverage and fuzz seeds. Streaming tests cover normal and
TLS entry points and verify the first SSE chunk before terminal completion.
The implementation uses the existing shared HTTP service and standard library;
it does not import the full traffic-compression PR #8538 or its migrations.
