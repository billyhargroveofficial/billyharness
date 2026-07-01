# Telegram

Billyharness can run as a Telegram gateway through the same gateway sessions used by the TUI. The bot keeps one editable progress message per active run, then finalizes it into rich Telegram text when the run completes.

## Setup

Set the bot token in `$BILLYHARNESS_HOME/config.toml`, `.env`, or the systemd environment:

```sh
TELEGRAM_BOT_TOKEN=123456:bot-token
```

Build and run:

```sh
cd /root/billyharness
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
./bin/fast-agent-harness telegram
```

For the server deployment, use systemd:

```sh
systemctl restart billyharness-gateway.service billyharness-telegram.service
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
journalctl -u billyharness-telegram.service -f
```

The gateway must be reachable by the Telegram service. In the normal local setup the service uses the configured gateway URL or the default local gateway.

## Allowlist

Live Telegram sending is fail-closed unless `allow_all_chats` is explicitly enabled. Allow either whole chats or individual Telegram users in billyharness config.

Allowed users are checked by Telegram `from.id`, so an allowlisted user can use the bot in a private chat or an allowed group/thread without sharing state with another allowlisted user.

## Sessions

Telegram state is keyed by:

```text
chat_id[:message_thread_id][:u<from.id>]
```

That means two users in the same Telegram group or topic get separate profile, model, reasoning, session id, event cursor, turn totals, and tool totals when Telegram provides `from.id`. Private chats naturally get separate state because each private chat has its own chat id.

Older state files used only `chat_id[:message_thread_id]`. Billyharness still reads that legacy key as a fallback and writes the next update to the per-user key.

`/cancel` is scoped to the same key, so one user's cancel command does not cancel another user's local run in the same group. Gateway cancellation is sent only for that user's current gateway session.

## Update Admission Durability

Telegram prompts are durably admitted before Billyharness advances the Telegram
offset. For each non-command text update, the bot resolves or creates the
gateway session, admits the prompt to `/v1/sessions/{id}/inputs` with a
deterministic `input_id` such as `telegram-update-123`, appends local admission
evidence to `*.admissions.jsonl`, and only then persists the next offset.

If the process exits before offset persistence, Telegram can redeliver the
update and the gateway admission remains idempotent. If the same update is
already promoted, completed, or ambiguous, the bot acknowledges it without
starting a duplicate run. If gateway admission fails, the offset is left
unchanged so the update can be retried after restart.

Empty, unsupported, and command updates are recorded as intentional ignores in
the same local admission JSONL before their offset is advanced. A prompt that is
admitted and acknowledged but crashes before execution remains visible as a
pending input in `/status` and in the gateway session's `inputs.jsonl`; operator
repair/retry of such inputs is deferred to the later input-inbox recovery
extension.

## Commands

```text
/start
/help
/new
/resume
/resume SESSION_ID
/fork current|SESSION_ID
/status
/model flash|pro|gpt|gpt-5.5
/profile billy
/reasoning low|medium|high|xhigh|off
/mcp
/context
/toolview
/config
/auth
/auth deepseek sk-...
/auth codex
/cancel
```

`/new` starts a fresh gateway session for the current Telegram state key and resets that key's turn/tool totals. `/resume` lists recent gateway sessions, and `/resume SESSION_ID` switches the current Telegram state key to that session. `/fork current` or `/fork SESSION_ID` clones the replayable messages from the source session into a new gateway session and makes it current. `/context` shows active context and contributors for the current session. `/toolview` replays the current session and shows compact tool details for the latest run without raw tool output. `/mcp`, `/config`, and `/auth` show sanitized status.

Session id arguments can be full ids or unambiguous prefixes.

Telegram `/resume` and `/fork` are owner-scoped. A user can see and select
their own Telegram-owned sessions plus ownerless legacy/solo sessions; sessions
owned by another Telegram user are hidden and explicit id/prefix selection fails
as not found. Billyharness currently has no Telegram admin/global session-owner
override. `AllowAllChats` controls whether incoming chats are admitted, but it
does not bypass per-user session ownership.

## Rendering And Throttling

During a run, Telegram edits one progress message at a configured interval to avoid Telegram edit throttling. The progress message shows model, reasoning, elapsed time, compact tool progress, assistant tail, context percent, and turn/tool totals.

Long progress text is truncated from the beginning so the newest activity remains visible. Finished messages hide raw tool args/output by default and render only Telegram-supported rich text. Large tool output should stay behind summaries or refs instead of entering the chat as raw JSON.
