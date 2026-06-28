# Context Accounting

`/context` in the TUI and Telegram shows the active conversation context that will be sent back through the gateway session. It is intentionally separate from provider cache hit/miss counters.

The report includes:

- active context tokens and percent of `context_window_tokens`;
- compaction threshold and whether the session is below or over it;
- 50%, 70%, 85%, and 95% threshold status;
- source buckets for user messages, assistant messages, reasoning content, tool outputs, web summaries, MCP outputs, system instructions, and compaction summaries;
- top context contributors with message index, role, source, tool name when available, token estimate, and a short preview.

Provider usage counters such as cache hit/miss can be larger than active context because they are provider billing/cache accounting for model calls. `/context` is the cleaner place to answer "why did this chat get large?".

Gateway API:

```bash
curl http://127.0.0.1:8765/v1/sessions/$SESSION_ID/context
```
