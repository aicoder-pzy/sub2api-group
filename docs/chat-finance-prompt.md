# Built-in financial review instructions

Financial review mode automatically includes the versioned `frontend/src/api/financeSystemPrompt.txt` as the system message on every answer request, including follow-ups and answers after context compaction. Users do not upload a prompt document. The existing mode switch controls whether these instructions are included.

The instructions retain general reconciliation and financial reporting behavior and add consolidation review rules for ownership layers, non-controlling interests, equity transactions, foreign currency translation and independent equity reconciliation. They require evidence for adjustments and preserve corrections and withdrawn conclusions.

Case-specific entities, balances and private documents stay in their owner's conversation. They are never built into the shared instructions. Summary requests continue using their separate continuation instructions; the specialist prompt is reapplied when answering from a summary. The chat has no spreadsheet or code execution tools, so the instructions must not claim actual execution.
