# Vision, Skills, and Web Backend TODO

Generated: 2026-07-02

This document is the source of truth for adding Codex/OpenAI vision input,
Hermes-compatible skills, and Tavily/Exa search/extract backends without
turning billyharness into a broad plugin platform.

## Current Findings

- Native vision is not supported end to end today.
- `protocol.Message` stores `Content string`; there is no multipart user input
  or attachment reference model.
- `gatewayapi.RunRequest` and `SessionInputRequest` are prompt-only.
- TUI and Telegram submit text only; Telegram photo/document updates are not
  parsed or downloaded.
- `codex_provider.go` always emits Responses `input_text`; it never emits
  `input_image`.
- DeepSeek is text-only for this harness and must reject or strip image input
  with a visible explanation.
- Model capability data lives in `internal/modelinfo`; vision support should be
  a first-class capability there, not scattered across adapters.
- Hermes runtime uses local `SKILL.md` packages and has active web config:
  search via Exa and extract via Tavily.
- Non-empty `TAVILY_API_KEY` and `EXA_API_KEY` assignments exist in Hermes
  dotenv files, but raw key values must never be copied into docs, logs, tests,
  traces, JSONL session files, or user-visible output.

## Primary References

- Codex local image pattern:
  `/root/agent-research/codex/codex-rs/protocol/src/user_input.rs`
- Codex Responses content item mapping:
  `/root/agent-research/codex/codex-rs/protocol/src/models.rs`
- Codex TUI attachment state:
  `/root/agent-research/codex/codex-rs/tui/src/bottom_pane/chat_composer/attachment_state.rs`
- Codex image capability gate:
  `/root/agent-research/codex/codex-rs/tui/src/chatwidget/input_submission.rs`
- Hermes active skills:
  `/root/.hermes/skills`
- Hermes runtime skills:
  `/opt/hermes-agent-src/skills`
- Hermes optional skills:
  `/opt/hermes-agent-src/optional-skills`
- Hermes web provider implementation:
  `/opt/hermes-agent-src/plugins/web`
- Tavily docs:
  `https://docs.tavily.com/documentation/api-reference/introduction`
  `https://docs.tavily.com/documentation/api-reference/endpoint/search`
  `https://docs.tavily.com/documentation/api-reference/endpoint/extract`
- Exa docs:
  `https://exa.ai/docs/reference/search`
  `https://exa.ai/docs/reference/contents-api-guide`
  `https://exa.ai/docs/changelog/may-2026-api-deprecations`

## Architecture Rules

- Keep JSONL session history as the durable source of truth.
- Store attachment metadata and stable refs in JSONL; never store raw image
  bytes or base64 blobs in conversation JSONL.
- Add `internal/attachments` for attachment metadata, validation, hashing,
  private storage, MIME sniffing, size caps, and ref resolution.
- Add `internal/skills` for skill discovery/parsing/viewing. Move skill logic
  out of `internal/tools` after behavior is covered by tests.
- Extend `internal/webtools` for Tavily/Exa HTTP clients and normalized web
  backend structs. Do not create model-facing `tavily_*` or `exa_*` tools.
- Keep model-facing tools stable: `web_search`, `web_fetch`, `web_extract`,
  and `web_crawl`.
- Keep `web_fetch` local by default. Provider-backed extraction belongs behind
  `web_extract` and can be used by `web_fetch` only when explicitly configured.
- Do not add marketplaces, remote skill stores, SQLite/FTS/vector indexes,
  shell-history scraping, enterprise policy layers, or copied competitor code.
- Do not let TUI or Telegram import provider, tools, gateway server, or agent
  directly. Preserve the package map in `docs/architecture.md`.

## Milestone P0: Protocol and Capability Foundation

- [x] Add protocol DTOs for multipart user input while preserving
  `Message.Content` compatibility.
  - Suggested types: `MessagePart`, `AttachmentRef`, `AttachmentKind`,
    `AttachmentDetail`.
  - `Message.Content` remains the text projection for old sessions and tests.
  - Add helpers for `MessageText()`, `MessagePartsOrText()`, and stable
    attachment counting.
  - Evidence: `internal/protocol/message_parts.go` adds multipart DTOs and
    helpers; `go test -count=1 ./internal/protocol` passed.
- [x] Add model capability fields in `internal/modelinfo`.
  - Suggested fields: `InputModalities []string`, `VisionInput bool`, or
    `ImageInput bool`.
  - Codex/OpenAI OAuth models support text and image.
  - DeepSeek models support text only.
  - Add `CapabilityPolicyRequest.RequireVisionInput`.
  - Evidence: `internal/modelinfo` now exposes `InputModalities`,
    `VisionInput`, and `RequireVisionInput`; `go test -count=1
    ./internal/modelinfo` passed.
- [x] Add `internal/attachments`.
  - Validate local image paths without following unsafe symlink tricks.
  - MIME sniff only supported image formats.
  - Enforce size and dimension caps.
  - Compute SHA-256 and stable attachment ids.
  - Store Telegram/downloaded files under `$BILLYHARNESS_HOME/attachments`
    with private permissions.
  - Provide a resolver used by gateway/session/provider boundaries.
  - Evidence: `internal/attachments` validates PNG/JPEG/GIF metadata, rejects
    symlink/traversal/stale refs, stores files with `0700`/`0600`
    permissions, and computes stable SHA-256 ids; `go test -count=1
    ./internal/attachments` passed.
- [x] Update `docs/architecture.md` package map and run
  `go test -count=1 ./internal/architecture`.
  - Evidence: `docs/architecture.md` includes `internal/attachments`;
    `go test -count=1 ./internal/architecture` passed.
- [x] Add tests for protocol JSON backward compatibility and attachment
  metadata round trips.
  - Evidence: `internal/protocol/message_parts_test.go` covers legacy JSON and
    multipart round trips; `internal/attachments/store_test.go` covers
    metadata-only refs and resolver safety. Combined command passed:
    `go test -count=1 ./internal/architecture ./internal/protocol
    ./internal/modelinfo ./internal/attachments ./internal/session`.

## Milestone P0: Codex Provider Vision

- [x] Extend provider requests so messages can carry parts/attachments.
  - Evidence: provider requests continue to carry `[]protocol.Message`; Codex
    and DeepSeek now inspect `Message.Parts`/attachment counts directly.
- [x] Update `internal/provider/codex_provider.go`.
  - Convert text parts to Responses `input_text`.
  - Convert local image refs to `input_image` data URLs at request time.
  - Include `detail` only when set and supported.
  - Add short text labels around images, like Codex `[Image #N]`, so the model
    can refer to images without seeing local filesystem internals.
  - Redact data URLs/base64 from errors, debug payloads, traces, and tests.
  - Evidence: `TestCodexBodySerializesImageAttachment` verifies text plus one
    image emits one `input_image` data URL with `detail`, uses `[Image #1]`,
    keeps JSONL message payloads metadata-only, and redacts data URLs through
    `internal/secrets`.
- [x] Update DeepSeek/OpenAI-compatible chat serialization.
  - Reject image input before provider call for known text-only models, or
    insert a clear omitted-image placeholder only when the caller explicitly
    chooses text fallback.
  - Evidence: `TestDeepSeekStreamRejectsImageInputBeforeHTTP` verifies
    DeepSeek returns a capability error before contacting HTTP.
- [x] Add provider tests.
  - Codex request with text plus one image emits one `input_image`.
  - DeepSeek image submission fails with a capability error before HTTP.
  - Missing/unreadable image produces a bounded user-visible error.
  - No base64 appears in loggable strings, JSONL session payloads, or trace
    text.
  - Evidence: provider/secrets coverage includes Codex image serialization,
    missing attachments, DeepSeek rejection, and image data URL redaction.
    Combined command passed: `go test -count=1 ./internal/architecture
    ./internal/protocol ./internal/modelinfo ./internal/attachments
    ./internal/provider ./internal/secrets`.

## Milestone P0: Gateway, Session, and Admission

- [x] Extend `gatewayapi.RunRequest`, `SessionInputRequest`, and related client
  DTOs with attachment refs while keeping `prompt` backwards compatible.
  - Evidence: `gatewayapi.RunRequest` and `SessionInputRequest` now include
    `attachments`; text-only helper paths still omit `parts` in messages, and
    gateway client JSON tests cover attachment refs.
- [x] Update gateway admission and idempotency.
  - Input hash includes attachment metadata hashes, not raw bytes.
  - Duplicate detection distinguishes same prompt with different attachments.
  - Admission rejects missing attachment refs before starting a run.
  - Evidence: `TestGatewaySessionInputAdmissionHashesAttachmentMetadata` covers
    duplicate/conflict behavior and verifies the input inbox JSONL has no image
    bytes; `TestGatewaySessionRunRejectsStaleAttachmentBeforeProvider` verifies
    stale refs fail before provider execution.
- [x] Update `internal/session`.
  - Add a text+parts run path instead of appending only
    `protocol.Message{Content: prompt}`.
  - Preserve old text-only behavior.
  - Evidence: `Session.RunInput`/`RunMessage` append text+attachment user
    messages, while `TestRunInputTextOnlyPreservesLegacyMessageShape` keeps
    text-only messages legacy-shaped.
- [x] Update replay/status/context projections to count attachments without
  inflating token estimates with base64.
  - Evidence: session status, summaries, responses, and context responses now
    expose `attachment_count`; `TestGatewaySessionRunPersistsImageOnlyAttachmentMetadata`
    and `TestContextStatusCountsAttachmentsWithoutImageBytes` cover replay and
    context projections without raw bytes.
- [x] Add tests for session replay, gateway client JSON, input inbox, and stale
  attachment rejection.
  - Evidence: combined command passed: `go test -count=1 ./internal/architecture
    ./internal/protocol ./internal/attachments ./internal/gatewayapi
    ./internal/gatewayclient ./internal/gateway ./internal/session
    ./internal/clientux`.

## Milestone P0: TUI Vision MVP

- [x] Add TUI attachment state beside the textarea.
  - Support `/attach PATH`.
  - Support pasted/local image paths when practical over SSH.
  - Show compact chips below or near input: `[Image #1 filename.png 640x480]`.
  - Allow removing selected attachment before submit.
  - Evidence: `/attach PATH`, pasted exact local image paths,
    `/attach remove N`, and `/attach clear` use `internal/attachments`;
    pending chips render near the input as `[Image #N file WxH]`.
- [x] Submit text plus attachments through local runtime and gateway mode.
  - Evidence: local TUI runtime and gateway `RunRequest` both pass
    `[]protocol.AttachmentRef` into `protocol.UserMessage`.
- [x] Gate by model capability.
  - Codex model: submit images.
  - DeepSeek model: show a visible unsupported-model message and keep the draft.
  - Evidence: `TestSubmitWithAttachmentRejectsTextOnlyModelAndKeepsDraft`
    verifies DeepSeek/text-only gating preserves draft text and attachments.
- [x] Render user attachment metadata in transcript without trying to render
  bitmap images inside terminal Markdown.
  - Evidence: submitted user transcript cells include compact metadata chips
    such as `[Image #1 screen.png 2x3]`; no terminal bitmap rendering is
    attempted.
- [x] Add tests for attach/remove/submit/gate behavior and transcript rendering.
  - Evidence: `TestAttachSlashAddsAndRemovesImage`,
    `TestPastedImagePathBecomesPendingAttachment`,
    `TestGatewayRunRequestIncludesAttachments`, and
    `TestSubmitWithAttachmentRendersTranscriptChip` cover the MVP. Combined
    command passed: `go test -count=1 ./internal/architecture
    ./internal/protocol ./internal/modelinfo ./internal/attachments
    ./internal/gatewayapi ./internal/gatewayclient ./internal/gateway
    ./internal/session ./internal/clientux ./internal/tui
    ./internal/tui/runtimeclient`.

## Milestone P0: Telegram Vision MVP

- [x] Extend Telegram update DTOs.
  - Parse `photo`, `document`, `caption`, `file_id`, `file_unique_id`,
    `file_size`, MIME type, and thread id.
  - Evidence: `internal/telegrambot/types.go` now parses photo/document
    media, captions, file ids/unique ids, file sizes, MIME type, and message
    thread ids; `TestTelegramUpdateParsesPhotoDocumentCaptionAndThread`
    covers the fixture.
- [x] Add Telegram `getFile` and file download support.
  - Store downloaded media through `internal/attachments`.
  - Advance Telegram offset only after download/admission succeeds or is
    durably rejected.
  - Evidence: `Client.GetFile`/`DownloadFile` fetch media with bounded reads,
    and Telegram admission stores downloaded images through
    `internal/attachments`; tests cover captioned photo/document downloads,
    transient download retry safety, and durable unsupported-model acks.
- [x] Treat captions as text and media as attachments.
  - Evidence: Telegram admission sends captions as `prompt` plus
    `attachments` refs to both `SessionInputRequest` and `RunRequest`.
- [x] Support image-only messages for Codex models.
  - Evidence: `TestTelegramImageOnlyPhotoAdmittedForVisionModel` verifies an
    empty prompt plus one image attachment is admitted and run for `gpt-5.4`.
- [x] For DeepSeek/text-only models, reply with a clean unsupported vision
  message instead of silently ignoring the photo.
  - Evidence: `TestTelegramVisionUnsupportedModelRepliesAndAcks` verifies
    `deepseek-v4-flash` gets a concise unsupported-image reply, no gateway
    admission, and a recorded/acked durable reject.
- [x] Keep progress UI compact; do not dump full tool views or attachment JSON
  into Telegram.
  - Evidence: Telegram progress rendering is unchanged; media metadata is sent
    to the gateway as refs and is not dumped into Telegram messages.
- [x] Add tests with Telegram photo/document fixtures, caption flow, offset
  safety, and concurrent chat isolation.
  - Evidence: `TestTelegramPhotoCaptionAdmissionDownloadsAttachment`,
    `TestTelegramDocumentImageAdmissionDownloadsAttachment`,
    `TestTelegramDownloadFailureDoesNotAdvanceOffsetOrAdmit`, and
    `TestTelegramConcurrentPhotoChatsRemainIsolated` cover the flow. Required
    P0 command passed: `go test -count=1 ./internal/architecture
    ./internal/protocol ./internal/modelinfo ./internal/attachments
    ./internal/provider ./internal/gatewayapi ./internal/gatewayclient
    ./internal/gateway ./internal/session ./internal/tui
    ./internal/tui/runtimeclient ./internal/telegrambot`.

## Milestone P1: Hermes-Compatible Skills

- [x] Add `internal/skills`.
  - Discover `$BILLYHARNESS_HOME/skills`.
  - Discover project `.billyharness/skills`.
  - Add opt-in compatibility sources for `.claude/skills`,
    `/root/.hermes/skills`, `/opt/hermes-agent-src/skills`, and
    `/opt/hermes-agent-src/optional-skills`.
  - Parse YAML frontmatter plus `SKILL.md` body.
  - Track category, source, path, description, tags, and linked support files.
  - Reject path traversal and cap reads.
  - Evidence: `internal/skills` owns local/nested skill discovery,
    frontmatter parsing, compatibility-source gating, linked support-file
    validation, and bounded reads; `TestDiscoverParsesFrontmatterAndSourcePrecedence`
    and `TestListHermesNestedCompatibilityAndSupportFileCaps` cover these
    paths.
- [x] Move existing `skill_list` and `skill_read` implementation out of
  `internal/tools/tools.go` into the skills package.
  - Evidence: `internal/tools` now wraps `internal/skills` for listing and
    reading; the old embedded discovery/read implementation was removed.
- [x] Add Hermes-style `skill_view` alias.
  - `skill_list` returns summaries only.
  - `skill_view` returns full `SKILL.md` content on demand.
  - Optional `file_path` reads bounded linked files from `references/`,
    `templates/`, `scripts/`, or `assets/`.
  - Evidence: `skill_view` is registered as a read-only alias with optional
    bounded `file_path`; `TestSkillsListAndReadAreOnDemandBoundedAndCompatOptional`
    covers the alias and support-file cap.
- [x] Add optional import/sync command.
  - Copy or symlink selected Hermes skills into `$BILLYHARNESS_HOME/skills`.
  - Preserve original path and SHA-256 in metadata.
  - Never auto-import all optional skills into prompt context.
  - Evidence: `skill_import` copies one explicitly selected local
    compatibility skill into `$BILLYHARNESS_HOME/skills` and writes
    `billyharness.skill.json` with source path and SHA-256; optional Hermes
    sources remain opt-in through `include_compat`.
- [x] Add tests for frontmatter parsing, source precedence, duplicate names,
  linked-file caps, Hermes compatibility, and command/tool aliases.
  - Evidence: package and registry tests cover frontmatter parsing,
    home-before-project source precedence, bounded support files, nested
    Hermes-style skills, `skill_view`, and `skill_import`. Combined command
    passed: `go test -count=1 ./internal/architecture ./internal/skills
    ./internal/tools ./internal/tui ./internal/telegrambot`.

## Milestone P1: Tavily and Exa Backends

- [x] Add config fields and diagnostics.
  - `web_search_backend`: `native`, `exa`, `tavily`, or `auto`.
  - `web_extract_backend`: `native`, `exa`, `tavily`, or `auto`.
  - `web_tavily_api_key_env`, default `TAVILY_API_KEY`.
  - `web_exa_api_key_env`, default `EXA_API_KEY`.
  - Optional `web_hermes_env_files`, default empty or explicitly documented.
  - Diagnostics report presence/absence only, never raw key values.
  - Evidence: config now exposes backend selectors, key-env names, optional
    Hermes env files, and diagnostics with key presence/source only; tests
    verify no Tavily/Exa key values leak.
- [x] Add safe credential discovery.
  - Prefer billyharness env/config.
  - Optionally load key names from Hermes dotenv files when enabled.
  - Redact all secret-like strings through `internal/secrets`.
  - Evidence: backend keys resolve from environment/normal dotenv first, then
    only configured Hermes env files; missing-key errors mention env names but
    not values.
- [x] Extend `internal/webtools`.
  - Add normalized search and extract request/response structs.
  - Add Tavily client: `POST https://api.tavily.com/search` and
    `POST https://api.tavily.com/extract` with Bearer auth.
  - Add Exa client: `POST https://api.exa.ai/search` and
    `POST https://api.exa.ai/contents` with `x-api-key`.
  - Do not use Exa `/research`; it was removed on 2026-05-01.
  - Honor `Retry-After` and bounded exponential backoff for 429.
  - Evidence: `internal/webtools` has normalized Tavily/Exa clients with
    bounded response bodies, `Retry-After` handling, and tests for headers,
    success parsing, retry, extraction failures, and no Exa search text leak.
- [x] Adapt `internal/tools/web_handlers.go`.
  - Keep model-facing tool names unchanged.
  - `web_search` returns compact normalized results only.
  - `web_extract` sends provider output through existing compaction,
    output-ref, cache, and web-summary accounting.
  - Cache keys include backend, query, URL, domain filters, depth, and output
    options.
  - Evidence: `web_search` and `web_extract` keep the model-facing names;
    configured Tavily/Exa backends are selected behind those tools, while
    native remains the default. Provider-backed `web_extract` stores full text
    in output refs and includes backend in cache keys.
- [x] Add tests with `httptest`.
  - Missing key errors.
  - Tavily/Exa success and per-URL failures.
  - Retry and timeout behavior.
  - Secret redaction.
  - No raw page text leakage from `web_search`.
  - Provider backend results still produce output refs and helper usage metrics.
  - Evidence: `internal/webtools` and `internal/tools` httptest coverage
    exercises missing keys, Tavily/Exa success, per-URL extract failures,
    `Retry-After`, no raw search text leakage, Hermes dotenv key discovery,
    output refs, cache hits, and helper API/cost metadata. Required P1 command
    passed: `go test -count=1 ./internal/architecture ./internal/skills
    ./internal/tools ./internal/webtools ./internal/config ./internal/secrets
    ./internal/provider ./internal/tui ./internal/telegrambot`.

## Milestone P1: Observability and UX

- [x] Add status/context metrics for attachment count and image submissions.
  - Evidence: gateway status, session summaries/responses, and context
    responses now include `image_submissions` beside existing
    `attachment_count`; `/context` formatting shows both metrics without image
    bytes. Focused command passed: `go test -count=1 ./internal/protocol
    ./internal/gatewayapi ./internal/gatewayclient ./internal/clientux
    ./internal/gateway`.
- [x] Add provider helper usage for Tavily/Exa when their APIs return usage or
  cost metadata.
  - Evidence: Tavily/Exa tool metadata now emits `provider.helper_usage` with
    `kind=web_backend`, `api_calls`, and `cost_usd`; context and Telegram
    projections show backend API usage separately from helper model calls.
    Focused command passed: `go test -count=1 ./internal/protocol
    ./internal/agent ./internal/clientux ./internal/clientux/projector
    ./internal/gatewayapi ./internal/gatewayclient ./internal/telegrambot`.
- [x] Add compact tool labels.
  - `web_search exa "query"`
  - `web_extract tavily example.com/path`
  - `vision image filename.png`
  - Evidence: shared `toolrender` result labels now use backend metadata for
    `web_search exa "query"` and `web_extract tavily example.com/path`;
    TUI image chips/user transcript lines use `vision image filename.png`
    labels without storage refs. Focused command passed: `go test -count=1
    ./internal/toolrender ./internal/tui ./internal/tools ./internal/webtools
    ./internal/telegrambot`.
- [x] Add TUI `/model` capability hint: text-only vs vision-capable.
  - Evidence: `modelinfo.InputCapabilityLabel` supplies shared
    `text-only`/`vision-capable` wording, and TUI `/model ...` status strings
    include the hint. Focused command passed: `go test -count=1
    ./internal/modelinfo ./internal/tui`.
- [x] Add Telegram status line hint when current chat uses a vision-capable
  model.
  - Evidence: Telegram `/model` replies and `/status` model line include the
    shared `text-only`/`vision-capable` hint for the current chat model.
    Focused command passed: `go test -count=1 ./internal/modelinfo
    ./internal/telegrambot`.
  - Required P1 command passed: `go test -count=1 ./internal/architecture
    ./internal/skills ./internal/tools ./internal/webtools ./internal/config
    ./internal/secrets ./internal/provider ./internal/tui
    ./internal/telegrambot`.

## Milestone P2: Polish After MVP

- [ ] Optional remote-image URL ingestion with safe download to local attachment
  storage before provider submission.
- [ ] Optional image resize/compression matching Codex limits.
- [ ] Optional OCR fallback for text-only providers.
- [ ] Optional `/skills import hermes <name>` interactive menu.
- [ ] Optional backend auto-routing:
  - native for free/default,
  - Exa for research search,
  - Tavily for robust extraction.

## Verification Commands

Run focused tests for every touched package. Before marking P0 done:

```bash
go test -count=1 ./internal/architecture ./internal/protocol ./internal/modelinfo ./internal/attachments ./internal/provider ./internal/gatewayapi ./internal/gatewayclient ./internal/gateway ./internal/session ./internal/tui ./internal/tui/runtimeclient ./internal/telegrambot
```

Before marking P1 done:

```bash
go test -count=1 ./internal/architecture ./internal/skills ./internal/tools ./internal/webtools ./internal/config ./internal/secrets ./internal/provider ./internal/tui ./internal/telegrambot
```

Before marking the full roadmap done:

```bash
go test -count=1 ./internal/...
go test -run 'Test.*(Vision|Image|Attachment|Tavily|Exa|Skill|Hermes|Capability|Redact|Replay|Admission|Telegram|TUI).*' -count=1 ./internal/...
```

Final verification on 2026-07-02:

- Passed: `go test -count=1 ./internal/...`
- Passed: `go test -run 'Test.*(Vision|Image|Attachment|Tavily|Exa|Skill|Hermes|Capability|Redact|Replay|Admission|Telegram|TUI).*' -count=1 ./internal/...`

## Completion Criteria

- Codex/OpenAI OAuth models accept image input from TUI and Telegram.
- DeepSeek models fail or fallback clearly for images without silent drops.
- Attachment refs survive gateway/session replay without raw bytes in JSONL.
- Hermes-compatible local skills can be listed and viewed on demand with caps.
- Tavily and Exa can back `web_search`/`web_extract` through normalized adapters.
- Existing native web behavior remains the default unless config selects a
  paid backend.
- Tests pass for changed packages and architecture guard remains green.
