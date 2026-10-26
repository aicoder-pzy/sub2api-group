# Official v0.2.18 synchronization

This custom release merges the stable v0.2.18 tag (`ac1b8f1271be0d674fa6f074403c1410f30a3a81`) and its VERSION-only follow-up (`5c185c5c5d4f5e409e2139806e0964ae944e03e8`). Later main-branch changes are outside this release.

The merge retains per-key model mapping, custom account scheduling, first-output failover, request budgets, balance checks, intelligence tests, notifications, and web chat. Anthropic compatibility streams use the official disconnect drain for final billing while preserving pre-output retry buffering. The official decoded scheduler snapshot cache supersedes the previous batching optimization; custom scheduler metadata remains included.

Migration `246_enable_gemini_image_generation_groups.sql` enables image generation for existing Gemini and Antigravity groups. Composite groups retain their existing image switch. The official Grok 4.7 Fast pricing update is included.

Validation runs in GitHub Linux CI: backend unit and database integration tests, lint, frontend type checking and critical tests, security scanning, and the production image build. Production acceptance checks existing key mappings/configuration, custom scheduling state, migration checksums, unrelated container identities, public routes, and key mapping CRUD/cache invalidation without paid inference.
