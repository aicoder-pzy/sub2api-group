Imported as inert data from https://github.com/mizaawa/ZakoProxyCLI at
94fb746c4f70f07ef0cf2e26397e2e29dd7b6299 (v0.14.0).
The 42 existing preset IDs and exact model lists are preserved; their instruction
text now follows the corresponding Coldcoffee seat. The general default is
unchanged. All 11 seats and 4 collaboration templates are available for explicit
model selection. DeepSeek targets include the upstream API adapter.
Coldcoffee source revision: 3249afce6b853ff2f2133e016b223db2cf38b087 (MIT).
The source manifest is preserved in coldcoffee-full.SOURCE.json. Upstream's
effectiveness status is unverified; no strategy runs automatically.
Prompt selection is opt-in. Catalog text_hash values are SHA-256 of the exact composed UTF-8 instruction.
Upstream licenses and attribution notices are preserved in licenses/.
Reproduce with: python .github/import-zako-profiles.py /path/to/ZakoProxyCLI
