# Telegram And Operator Surfaces

This document describes the implemented Telegram adapter and the operator-facing
surfaces it exposes. The key architecture rule is that Telegram is a scoped
gateway client: it owns Telegram transport, state, rendering, and command
handling, while gateway sessions, admission, replay, authorization, and runtime
execution stay behind the typed gateway APIs.

Status note: this document was reviewed against the current implementation on
2026-07-05 for Telegram operator command policy, secret-bearing auth command
semantics, and media rejection behavior. Claims describe this checkout, not
necessarily a clean release commit.

The decision record is [ADR 0006](../adr/0006-telegram-is-a-gateway-client.md).
The shared event/replay rules are in
[Runtime event system](runtime-event-system.md) and
[Architecture map](../architecture.md#runtime-event-delivery).

## Ownership Boundary

[internal/telegrambot](../../internal/telegrambot) owns:

- Telegram Bot API long polling, send/edit/delete calls, per-chat rate limiting,
  Bot API retry-after backoff, and Telegram token redaction in client errors
  ([client.go](../../internal/telegrambot/client.go)).
- Per-chat state ([store.go](../../internal/telegrambot/store.go)).
- Telegram command dispatch, message admission, rendering, progress edits,
  rich-message final delivery, and media attachment ingestion
  ([commands.go](../../internal/telegrambot/commands.go),
  [runner.go](../../internal/telegrambot/runner.go),
  [render.go](../../internal/telegrambot/render.go),
  [media.go](../../internal/telegrambot/media.go)).

It must not import `internal/gateway`; this is enforced by the package map in
[docs/architecture.md](../architecture.md). The package talks to the gateway
through the `Harness` interface in
[gateway_client.go](../../internal/telegrambot/gateway_client.go), whose
default implementation wraps [internal/gatewayclient](../../internal/gatewayclient).

The gateway owns session persistence, input idempotency, owner-scoped
authorization, durable event replay, cancellation, undo/redo, context reports,
managed-process status, config status, MCP status, and auth mutation endpoints.
The shared request and response DTOs live in
[internal/gatewayapi](../../internal/gatewayapi/types.go).

## Process And Operator Configuration

The CLI entrypoint is `fast-agent-harness telegram`, wired in
[cmd/fast-agent-harness/service_cmd.go](../../cmd/fast-agent-harness/service_cmd.go).
It resolves runtime config first, then configures the Telegram adapter with:

- gateway URL discovery from explicit `-gateway`, gateway URL env vars, runtime
  config, and default local candidates;
- bot token from `-token`, `BILLYHARNESS_TELEGRAM_BOT_TOKEN`, or
  `TELEGRAM_BOT_TOKEN`;
- optional Telegram Bot API base URL override;
- initial model, profile, reasoning effort, access mode, max tool rounds,
  context window, and compact threshold from runtime config and flags;
- state path, allowed chat IDs, allowed user IDs, allowed operator user IDs,
  live-send/dry-run mode, polling timeout, live-edit interval, and managed
  process watch interval.

The default Telegram state path is
`$BILLYHARNESS_HOME/telegram/state.json`, falling back to
`$HOME/billyharness/telegram/state.json` when `BILLYHARNESS_HOME` is unset
([util.go](../../internal/telegrambot/util.go)).

The `doctor` surface treats the gateway and Telegram adapter as managed
services named `billyharness-gateway.service` and
`billyharness-telegram.service`, and checks duplicate
`fast-agent-harness` processes plus `gateway.pid` and `telegram.pid`
([doctor.go](../../cmd/fast-agent-harness/doctor.go)).

## Allowlist And Send Safety

Live Telegram sending is fail-closed unless the operator explicitly scopes it.
When `SendEnabled` is true and dry-run is false, the CLI requires at least one
allowed chat ID, one allowed user ID, or `-allow-all-chats`
([service_cmd.go](../../cmd/fast-agent-harness/service_cmd.go)). The adapter
also normalizes options so live send without `AllowAllChats` requires an
allowlist ([authz.go](../../internal/telegrambot/authz.go)).

Admission accepts a message when any of these is true:

- `AllowAllChats` is set;
- the chat ID is in `AllowedChatIDs`;
- the sender user ID is in `AllowedUserIDs`;
- no allowlist is configured and `RequireAllowlist` is false.

Admission only decides whether the adapter may process the incoming Telegram
message. Operator command authorization is a second gate. Commands that expose
local config, MCP status, managed processes, memory management, undo/redo, or
auth mutation require an identified human Telegram operator. Operators come
from `AllowedOperatorUserIDs` when configured; otherwise the adapter falls back
to `AllowedUserIDs` for compatibility. The CLI exposes this through
`-operator-user`, `BILLYHARNESS_TELEGRAM_OPERATOR_USER_IDS`, and legacy
`TELEGRAM_OPERATOR_USER_IDS`
([service_cmd.go](../../cmd/fast-agent-harness/service_cmd.go),
[command_policy.go](../../internal/telegrambot/command_policy.go)).

Owner-only commands are stricter than operator-only commands. Secret-bearing
auth configuration requires a private owner chat, an identified non-bot sender,
and operator authorization. Group chats, anonymous senders, and bot senders are
rejected before auth material is persisted.

Dry-run mode logs sends and edits instead of calling Telegram. Real sending and
editing are gated in [delivery.go](../../internal/telegrambot/delivery.go), so
tests can exercise flows without external Telegram writes.

Before any Telegram send, edit, progress edit, rich markdown send/edit, or
dry-run log leaves the adapter,
[redaction.go](../../internal/telegrambot/redaction.go) passes text through
the shared `internal/secrets` redactor. That shared redactor covers credential
URLs, secret query parameters, header-like credentials, Telegram bot-token
URLs, provider keys, and MCP-style secret argv flags. Renderer error paths also
store redacted run and tool failure text before final delivery. Telegram Bot
API transport errors redact the bot token before errors are returned or logged.

Secret-bearing auth commands have an additional guard. `/auth deepseek ...`
is accepted only in a private owner chat. It calls Telegram delete before
persisting the key, refuses to persist if deletion fails, and redacts the
submitted key from error text
([commands.go](../../internal/telegrambot/commands.go),
[command_policy.go](../../internal/telegrambot/command_policy.go)). Group
chat attempts, dry-run group attempts, anonymous senders, and bot senders do
not persist the submitted key.
`/auth codex` imports local Codex OAuth through the gateway without accepting a
token in the chat. Auth status output is formatted from redacted credential
status values.

Gateway transport auth is separate from the Telegram allowlist. The shared
gateway client attaches a bearer token from `BILLYHARNESS_GATEWAY_AUTH_TOKEN`
or legacy `FAST_AGENT_GATEWAY_AUTH_TOKEN` when present
([gatewayapi/net.go](../../internal/gatewayapi/net.go)). The gateway
security middleware requires authenticated mutating `/v1/` routes unless the
server is explicitly running in its development loopback-bypass mode
([http_security.go](../../internal/gateway/http_security.go)).

## State And Session Ownership

Telegram state is keyed by chat, thread, and sender user:

- `chat_id`
- optional `thread_id`
- optional non-bot `user_id`

The key format is implemented in
[state_runtime.go](../../internal/telegrambot/state_runtime.go). Legacy
chat/thread keys are still read as a fallback when a user-scoped key has no
state, which lets older state files remain usable.

Each `ChatState` stores the active gateway session ID, selected model/profile,
reasoning effort, access mode, accumulated agent/tool counters, last durable
event sequence, pending admitted input, pending user-input request, and update
time ([types.go](../../internal/telegrambot/types.go)). The state file is saved
atomically with private file mode `0600` and a private directory
([store.go](../../internal/telegrambot/store.go)).

Every normal Telegram session creation and fork stamps gateway session owner
metadata:

- `client_type=telegram`
- Telegram chat ID
- Telegram thread ID
- Telegram user ID
- selected profile
- selected model

The adapter passes this owner both in create-session bodies and in scoped
gateway request headers through `gatewayclient.WithSessionOwner`
([session_owner.go](../../internal/telegrambot/session_owner.go),
[gatewayclient/client.go](../../internal/gatewayclient/client.go),
[gatewayapi/types.go](../../internal/gatewayapi/types.go)).

The gateway is the authority for owner enforcement. It filters session lists for
scoped clients, denies reads across another owner, denies mutating legacy
unowned sessions for scoped clients, and rejects creates whose body owner
conflicts with the request owner headers
([session_authz.go](../../internal/gateway/session_authz.go),
[gateway.go](../../internal/gateway/gateway.go)). Telegram also filters
Telegram-owned summaries before `/resume` and `/fork`, but that local filter is
defense in depth; gateway authorization is the hard boundary.

Legacy unowned sessions can appear in Telegram session lists because they are
readable to scoped clients. Continuing such a session from Telegram is not a
stable mutation path: the gateway rejects scoped mutating routes for legacy
unowned sessions. Forking a readable legacy session creates a new owned session.

## Message Admission And Run Flow

The poller handles only Telegram `message` updates
([poller.go](../../internal/telegrambot/poller.go)). Empty or unsupported
updates are logged as ignored before their update offset is acknowledged.

Normal prompt flow:

1. Check allowlist.
2. Route slash commands directly.
3. Reject unsupported voice, audio, video-note, and video messages with a
   user-facing reply after allowlist/command routing.
4. If a user-input request is pending and the update is a plain non-media
   message, answer it instead of admitting a new prompt.
5. Interrupt any active run for the same chat/thread/user before admitting the
   replacement prompt.
6. Resolve or create an owned gateway session.
7. Prepare image attachments, if present.
8. Admit the prompt to the gateway with stable input ID
   `telegram-update-<update_id>`, `client_type=telegram`, a scoped client ID,
   metadata, and interrupt policy `interrupt`.
9. Persist the pending gateway input in Telegram state, acknowledge the Telegram
   update offset, and start the session run. Ignored, admitted, and abandoned
   update outcomes are logged with `log.Printf`; gateway input durability remains
   behind the gateway session-input APIs.

If gateway admission fails or Telegram media download fails, the update offset
is not advanced so the poller can retry. If the gateway reports a duplicate
input that has already completed or otherwise left the admitted state, Telegram
acks the update and does not start another run.

Telegram treats pending-state persistence as part of admission. If gateway
admission succeeds but saving the pending input fails before the Telegram offset
is acknowledged, Telegram calls the gateway input-completion API with
`terminal_status=preflight_failed` and a failure reason before returning the
local persistence error.

On startup, Telegram reconciles durable chat states that still contain a pending
gateway input. When the gateway session is reachable, Telegram terminally
completes that input as `abandoned_after_restart`, logs the returned gateway
state, clears the local pending fields, and saves state. If the gateway session
is missing, Telegram logs `gateway_session_missing_after_restart`; other gateway
completion errors fail startup so the adapter does not silently acknowledge an
ambiguous input.

The run request carries the admitted input ID, client ID, Telegram client type,
prompt, attachment refs, selected model/profile/reasoning/access mode, max tool
rounds, interrupt policy, and metadata
([runner.go](../../internal/telegrambot/runner.go),
[gatewayapi/types.go](../../internal/gatewayapi/types.go)).

## Event Replay And Progress Updates

Gateway session JSONL is durable; Telegram live messages are progress views.
Before a new run, Telegram replays durable session events after the chat state's
last event sequence to catch up silent changes and update counters. During a
run, it streams gateway `/run` events and updates the last observed sequence.

If the gateway emits `gateway.stream_gap`, or the shared gateway client detects
a sequence gap, Telegram replays durable session events from the last observed
sequence before final delivery. The recovery path is implemented in
[runner.go](../../internal/telegrambot/runner.go); the shared client sequence
checking is in [gatewayclient/client.go](../../internal/gatewayclient/client.go).

Live progress starts with a placeholder Telegram message, then periodic edits
show the current assistant tail, model/reasoning metadata, event pulse, context
usage, tool/status summaries, and elapsed time. Edits are bounded by a timeout
and paused briefly after deadline failures
([progress_runtime.go](../../internal/telegrambot/progress_runtime.go),
[progress_stream.go](../../internal/telegrambot/progress_stream.go)).

Telegram sends `typing` chat actions during live runs when real sending is
enabled. A new non-command message for the same chat/thread/user cancels the
local run, asks the gateway to cancel the session, waits briefly for the old run
to finish, and marks the old placeholder as interrupted before processing the
newer input.

## Rendering

Telegram rendering is downstream of the shared event projector. The renderer
uses [internal/clientux/projector](../../internal/clientux/projector) for
presentation-neutral snapshots, [internal/toolrender](../../internal/toolrender)
for tool labels and compact result summaries, and
[internal/displayfmt](../../internal/displayfmt) for compact numbers.

The adapter renders:

- assistant deltas into the live tail and final answer;
- tool calls, tool results, failures, aborted calls, context threshold events,
  stream-still-running hints, gateway stream gaps, turn-change events, and
  user-input prompts into compact tool/status lines;
- provider usage and helper/model usage into footer/context counters;
- final answers as Telegram rich markdown when available, with HTML fallback.

Final output is chunked to Telegram limits. The rich path uses Telegram's
`sendRichMessage`/rich `editMessageText` payloads
([client.go](../../internal/telegrambot/client.go),
[rich_stream.go](../../internal/telegrambot/rich_stream.go)); fallback output
uses Telegram HTML generated by
[markdown.go](../../internal/telegrambot/markdown.go). Tool/status progress
deduplicates by key and keeps a compact recent set
([render.go](../../internal/telegrambot/render.go)).

Status surfaces intentionally format readable text instead of raw JSON:

- `/mcp` uses [internal/mcpstatus](../../internal/mcpstatus/status.go).
- `/context` uses `gatewayclient.FormatSessionContext`.
- `/processes` uses gateway-managed process dashboard text.
- `/config` uses resolved config formatting.
- `/toolview` replays the current session and renders compact tool lines.

## Attachments

Telegram accepts photo messages and image documents. For photos, it chooses the
largest Telegram photo size by file size or dimensions. For documents, it
requires an image MIME type when Telegram provides one
([media.go](../../internal/telegrambot/media.go)).

Voice, audio, video-note, and video messages are processable only so they can be
rejected explicitly. After allowlist and command checks, the adapter sends
"not supported yet" guidance, logs the ignored reason such as
`voice_unsupported`, and advances the update offset; it does not admit a
gateway input.

Before downloading media, the adapter checks that the selected model supports
vision input through [internal/modelinfo](../../internal/modelinfo). Unsupported
vision input is a durable rejection: Telegram sends a user-facing explanation,
logs an ignored-update reason, and advances the update offset.

Downloaded media is bounded by the attachment store size limit and validated as
PNG, JPEG, or GIF image data before storage. The image bytes are written to the
private attachment store under `$BILLYHARNESS_HOME/attachments`, and the gateway
run receives only `protocol.AttachmentRef` metadata
([attachments/store.go](../../internal/attachments/store.go),
[protocol/message_parts.go](../../internal/protocol/message_parts.go)).

Transient Telegram download failures are retryable and do not advance the
update offset. Durable invalid media errors and unsupported-media messages are
acknowledged after a user-facing message and ignored-update log entry.

## Command Surface

Telegram commands are backed by shared action metadata from
[internal/clientux](../../internal/clientux/actions.go) and dispatched in
[commands.go](../../internal/telegrambot/commands.go). The current implemented
Telegram commands are:

- `/start`, `/help`: show Telegram help.
- `/commands [query]`: search built-in actions and available profile metadata
  through [internal/commandregistry](../../internal/commandregistry).
- `/new`, `/reset`: create a new owned gateway session.
- `/resume SESSION_ID`: list or select a readable session by ID prefix.
- `/fork current|SESSION_ID`: clone replayable messages into a new owned
  session.
- `/status`: show chat settings and active runtime model when available.
- `/model`, `/profile`, `/reasoning`, `/mode`, `/access`: update chat-level run
  defaults.
- `/mcp`, `/config`, `/processes`: operator-only runtime status views.
- `/context`, `/toolview`, `/tools`: session-scoped runtime views.
- `/memory`: operator-only local memory management for the active profile.
- `/diff`: preview the latest gateway session turn-change restore operation.
- `/undo`, `/redo`: operator-only gateway session turn-change restore
  operations.

The bot also has one outbound-without-inbound-trigger path: when live sending
is enabled and a process watch interval is configured, a Telegram-side watcher
polls `GET /v1/processes?include_exited=true` through the gateway client and
sends a redacted message to configured operator/allowed chats when a managed
shell process first appears exited or transitions from running to exited. The
gateway process does not receive the Telegram bot token, and `internal/tools`
does not import Telegram.
- `/auth`, `/auth deepseek ...`, `/auth codex`: owner-only auth status,
  DeepSeek API-key persistence, or local Codex OAuth import. Secret-bearing
  `/auth deepseek ...` requires private owner chat.
- `/cancel`: cancel the current local/gateway run for the chat.

Command policy is intentionally layered:

- public commands: `/start`, `/help`, `/commands`;
- session-scoped commands: session selection, runtime defaults, context/diff,
  tool views, and cancellation for the current Telegram-scoped session;
- operator-only commands: MCP/config/process status, memory management, and
  undo/redo;
- owner-only commands: auth status/import and secret-bearing auth mutation,
  with private-chat enforcement for secret material.

Commands marked `bypassRunLock` can run while a long generation is active.
Other commands serialize through the per-chat mutex like normal messages.

## Verification Anchors

These tests cover the main claims in this document:

- Telegram allowlist fail-closed behavior and allowed-user behavior:
  [bot_test.go](../../internal/telegrambot/bot_test.go).
- Telegram admission, retryable failures, duplicate inputs, user-input answers,
  media attachment ingestion, unsupported vision rejection, and concurrent chat
  isolation: [poller_test.go](../../internal/telegrambot/poller_test.go).
- Session owner stamping, resume/fork filtering, status/config/context/process
  surfaces, toolview, auth deletion/redaction, and command behavior:
  [commands_flow_test.go](../../internal/telegrambot/commands_flow_test.go).
- Operator command authorization, anonymous/bot rejection, configured group
  operators, and dry-run group secret rejection:
  [command_policy_test.go](../../internal/telegrambot/command_policy_test.go).
- Telegram outbound and rendered error redaction:
  [redaction_test.go](../../internal/telegrambot/redaction_test.go).
- Stream-gap rendering and replay-before-final-delivery:
  [render_test.go](../../internal/telegrambot/render_test.go) and
  [runner_test.go](../../internal/telegrambot/runner_test.go).
- Gateway client owner metadata, scope headers, replay cursor behavior,
  sequence gaps, stream-gap hints, cancellation, and user-input answer endpoints:
  [client_test.go](../../internal/gatewayclient/client_test.go).
- Gateway owner persistence, list filtering, cross-owner denial, and legacy
  unowned read-only behavior for scoped clients:
  [session_events_test.go](../../internal/gateway/session_events_test.go).
