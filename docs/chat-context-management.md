# Long conversation context

The chat request now uses a persistent continuation summary plus recent original messages. Crossing the request budget starts automatic, sequential summarization through the currently selected key and model. It no longer rejects the whole conversation because its historical image count exceeds twelve. Original messages, attachments and images remain available in history and exports.

Each completed batch saves `context: { summary, through, fingerprint }` through the existing owner-scoped conversation API and IndexedDB schema. `through` counts covered original messages; the SHA256 fingerprint covers their IDs, roles, text and sources. Editing covered evidence invalidates the checkpoint. The latest user submission stays verbatim. Summary instructions retain source data, financial precision, corrections, withdrawn claims and unresolved work.

Requests normally retain up to eight images, 12 MiB of encoded image data and 160,000 text characters. These are batch budgets, not limits on the conversation's cumulative history. A model's explicit context rejection before its answer starts triggers one further compaction and retry with a smaller text budget. Authentication, billing, generic HTTP errors and partially emitted answers are never automatically replayed.

Only complete, nonempty, untruncated summaries become checkpoints. Stop, network failure or answer failure retains completed checkpoints and all originals, so retry can resume. The same-account sync protocol carries the checkpoint to other devices; its existing revision checks retain conflicting edits. The settings menu exposes the continuation summary. Automatic organization uses the selected group's normal model credits.

Checks cover long image and text history, exact recent evidence, checkpoint reload and sync, edited-source invalidation, incomplete summaries, stopping, model context rejection, account isolation and PostgreSQL round trips. No database migration or new dependency is required.
