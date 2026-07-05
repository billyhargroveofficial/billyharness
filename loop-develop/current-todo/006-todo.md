# 006 TODO - Real Docsgen: Generated, Test-Enforced Agent Map

Status: current
Created: 2026-07-04
Owner loop: Claude main chat + 3 Sonnet code-anchor readers (tools/config,
gateway/commands, protocol/eventlog/docs), building on the 72-agent 005 research

## Request

Billy tried to build an automatic documentation read/write system — a map of
the repo for agents. The first attempt (agent-index manifest + docguard Stop
hook) grew into hand-maintained metadata with zero implementing Go code, and
005 deletes it. This TODO builds the real thing: a `docsgen` subcommand that
GENERATES reference docs by iterating registries the code already owns, with a
single ordinary Go test enforcing freshness. No hooks, no hand-edited JSON, no
metadata that can lie.

The core trick that makes this not-docguard-2.0: every task that exposes a
data table for the generator also makes the RUNTIME consume that same table
(usage() renders from the subcommand slice, `isKnownEventType` consults the
event table, `routes()` ranges over the route slice). Docs become a projection
of the same data the program runs on, so neither side can drift from the other.

## Relationship To 004 And 005

- Requires 005 P0.1/P0.2 (delete agent-index + docguard) landed first; docsgen
  is their replacement, and its output takes the `llms.txt` "Machine Indexes"
  slot that P0.1/P0.2 empty out. The cleanest rule: start this loop only after
  005 is verified and moved to `loop-develop/history`.
- 005 P1.11 moves eventCallID/metadata decoders into `internal/protocol`; 006
  P1.5 refactors `internal/protocol/envelope.go` in the same area. Land 005
  first so P1.5 edits the post-move shape.
- 005 P2.9 splits `config.Resolve()` into load+apply; 006 P0.2 edits
  `internal/config/resolved.go` too. Same rule: 005 first.
- `agent-index/generated/reference-plan.md` (deleted by 005) was the handwritten
  spec for this exact feature. Its salvageable decisions are absorbed into the
  Design Canon below so nothing is lost with the file: the
  `docsgen -check` / `-write` command shape, generated-file markers, and
  regenerate-into-temp-then-diff verification.

## Source Research Summary

### Where The Facts Come From

The 005 workflow (72 Sonnet agents) established the macro picture: the old
docs layer was ceremony, but the repo already contains two working versions of
"map for agents" — `llms.txt` (loaded prose) and `internal/architecture`
(docs-with-teeth: the Package Map table is parsed and enforced against
`go list` reality). For this TODO, 3 focused Sonnet readers extracted exact
code anchors from the six registries docsgen will consume. Line numbers are
from 2026-07-04 and will drift slightly as 005 lands; every task's first
checklist item is to re-verify its anchors.

### Registry Iterability Map (the load-bearing research result)

| Source of truth | Anchor | Iterable today? | Refactor needed |
|---|---|---|---|
| Config keys | `internal/config/resolved.go:209` `configSpecs()`, 63 entries | Runtime-iterable but unexported, no descriptions | Export `ConfigKeySpecs()`, add per-key descriptions, export built-in defaults |
| Native tools | `internal/tools/tools.go:595` `Registry.Specs()` + `policy.go:22` `PolicyDecision` + `tools.go:640` `ParallelMetadata` | Yes, as-is (`cmd/.../tools_cmd.go:13` `printTools()` is a working prototype) | Only: export the 11 `protocol.Risk` consts as a list; unify duplicated risk normalization |
| Command catalog | `internal/commandregistry/registry.go:60` `Build().Entries()` | **Yes, fully, zero refactor** — the template the others should copy | None |
| TUI actions | `internal/tui/actions.go:43` `actionRegistry()`, 42 specs; 32 exported via `clientux.ActionDefinitions()` | Partial (keybindings unexported) | Export doc-only projection `tui.ActionDocs()` |
| Telegram commands | `internal/telegrambot/commands.go:62` `telegramCommands()`, 21 specs + class enum | No (all unexported) | Export `TelegramCommandDocs()` projection |
| Gateway routes | `internal/gateway/gateway.go:294` `routes()`, 27 uniform `HandleFunc` literals | Source-scan only; ServeMux has no pattern-list API | Refactor to `[]routeSpec` that `routes()` ranges over |
| CLI subcommands | `cmd/fast-agent-harness/main.go:21-59` switch; `usage()` at `:63` hand-duplicates it | No | Refactor to `[]subcommand` slice; `usage()` self-generates |
| Protocol events | `internal/protocol/types.go:94-132`, 37 `EventType` consts in one block | Const block is AST-harvestable; `isKnownEventType` (envelope.go:201) and `ValidateEventEnvelope` (:109) duplicate the list procedurally | Single `eventTypeSpecs` table consumed by both validators and docsgen |
| Event lifecycles | `internal/eventlog/eventlog.go:206` `Observe()`, ~220-line switch over 10 state maps | **No — procedural, not data** | Extract declarative transition rules (hardest task, P2) |
| Doctor checks | `cmd/fast-agent-harness/doctor.go:193` `collectDoctorReport()` append-sequence | No (check identity exists only as a side effect of running) | Split descriptor vs executor |

### Other Facts The Tasks Below Rely On

- Config: `Config` struct has 69 fields (`internal/config/config.go:5`); 63
  are covered by `configSpecs()`; the gap is 2 unexported override-tracking
  bools, 3 sub-registries with their own exported loaders (`MCPServers`,
  `Hooks`, `DiagnosticsCommands`), and `WorkspaceRoots` (computed from cwd at
  `defaults.go:259`, no spec). Env aliases live inline per spec (e.g.
  `resolved.go:213` profile ← `BILLYHARNESS_PROFILE`,`FAST_AGENT_PROFILE`).
  Per-key provenance is `ResolvedValue{Key,Value,Source,SourcePath,SourceKey,
  Redacted,Warning,Error}` (`resolved.go:36`); 9 `Source*` layer constants at
  `resolved.go:17-28`; `config_cmd.go:35,49` already prints
  `SanitizedValues()` — an existing prototype consumer.
- `settings.json` has THREE independent hand-unmarshaled shapes:
  `resolved.go:368` `applyBillySettings()`, `defaults.go:180`
  `ApplyBillySettingsDefaults()`, and `internal/tui/settings.go:36`
  `loadAppSettings()`. No canonical struct (P2.4 fixes this).
- Tools: `protocol.ToolSpec{Name,Description,Parameters json.RawMessage,Risk}`
  (`internal/protocol/types.go:60`); schemas are inline JSON-string constants
  via `raw()` (`tools.go:1536`) — raw JSON, not Go structs. Registry built by
  ~23 hardcoded `add*` calls (`tools.go:214-236`). MCP risk normalization is
  duplicated: `internal/config/mcp.go:356` `normalizeMCPToolRisk` vs
  `internal/mcpclient/catalog.go:142` `normalizeMCPRisk` — adding a risk class
  today means editing both plus the consts plus 4 switches in `policy.go`.
- Gateway: stdlib `net/http.ServeMux` with Go 1.22 `"METHOD /path"` patterns;
  auth is ONE structural middleware (`http_security.go:37`
  `httpSecurityMiddleware`) — `/health` explicit bypass (`:39-42`), `/v1/*`
  detection via `isGatewayV1Route` (`:83-85`), mutation detection via
  `isGatewayMutation` (`:87-97`), bearer check `bearerTokenMatches`
  (`:190-200`), loopback dev-bypass (`:62-66`). No per-route auth table —
  docsgen must call these same predicates, never re-encode the rules.
- DTOs: `internal/gatewayapi/types.go` `<Noun>Request`/`<Noun>Response`
  convention (`RunRequest:18`, `CreateSessionRequest:61`,
  `SessionInputRequest:37`, `HealthResponse:86`, `ReadinessResponse:92`);
  mapping to routes is 1:1 by convention but declared nowhere as data; some
  types are shared (`SessionUndoResponse:399` serves undo AND redo).
- Protocol: `Event` envelope struct at `types.go:220`; 37 event types; 16
  typed payload structs in `types.go` (`TurnEvent:257` … `ContextThresholdEvent:563`);
  `EventSource` consts at `envelope.go:16-25`; required-correlation-ID rules in
  `ValidateEventEnvelope`'s switch at `envelope.go:138-161`.
- Lifecycle: `LifecycleValidator` (`eventlog.go:140`) tracks run/turn/step/
  call/attempt (+user_input existence, +hook opportunistically) via 10 maps
  with `\x00`-joined composite keys (`eventlog.go:673-690`); data-dependent
  rules exist (`lifecycleDataString` `:335`, `allowedPostTerminalProgressPhase`
  `:539`) that must STAY procedural.
- Architecture guard: `architecture_test.go:212` parses `docs/architecture.md`
  strictly between `## Package Map` (`architecture.md:12`) and the next `## `
  heading; 4-column table, 50 package rows (`:16-65`); comparison via
  `go list -json` (`listPackages` at `:157`); a separate hardcoded
  required-imports map at `:60`. `docs/README.md:16-18` warns to keep the
  table shape stable.
- llms.txt: 90 lines, sections `Start Here`(:8) `Agent Rules`(:15)
  `Architecture Canon`(:23) `Machine Indexes`(:53) `Coverage Note`(:64)
  `Operations Runbooks`(:73) `Optional`(:82). The Machine Indexes section dies
  with 005 and gets replaced by a Generated References section here.
- CI: `.github/workflows/ci.yml`, `verify` job runs `git diff --check` →
  `scripts/verify-deps.sh` → `go vet` → `go test -count=1 ./...` → focused
  `-race` set → `govulncheck` → strict hygiene → `scripts/bench-smoke.sh`.
  A new `TestDocsCurrent` rides `go test ./...` for free. `//go:generate` is
  currently used nowhere in the repo.
- Skills/hooks surfaces (for the tool catalog's config appendix):
  `internal/skills/skills.go:133` `Discover()` scans `sourceDirs()` (`:295`);
  `internal/config/hooks.go:74` `LoadHooks()` parses
  `$BILLYHARNESS_HOME/hooks.config.toml` into `Hook` (`:14`).

## Design Canon For This Loop

- Generate only "what and where" (registries, routes, schemas, keys, commands).
  "Why" stays hand-written prose in `docs/architecture/`. The generator never
  touches prose files except inside explicit marker blocks.
- A generator may only read data the code already owns. No sidecar metadata
  files, no hand-maintained JSON. If a fact isn't in code, either add it to
  the code's own table (description fields) or leave it to prose.
- Same-table rule: when a task exposes a data table for docsgen, the runtime
  must consume that identical table (usage() from the subcommand slice,
  validators from the event table, `routes()` from the route slice). This is
  what makes generated docs unable to lie.
- Determinism: regeneration is byte-identical on an unchanged tree. Stable
  sort everywhere; NO timestamps, NO commit hashes inside generated files.
  Freshness against a live binary is handled by source-hash footers
  (sha256 of the canonical JSON of the generator's input data) + `doctor -docs`.
- One enforcement point: `TestDocsCurrent` in `internal/docsgen` regenerates
  into memory and byte-compares `docs/generated/*`. It fails with the exact
  command to run. No Stop hooks, no CI-only magic — an ordinary red test.
- Command shape (salvaged from reference-plan.md): `fast-agent-harness docsgen`
  writes, `-check` diffs without writing (exit 1 on drift), `-only <target>`
  scopes to one generator. Every generated file starts with
  `<!-- GENERATED by fast-agent-harness docsgen; do not edit. Source: <pkg> -->`
  and ends with `<!-- source-hash: <hex> -->`.
- Import direction: `internal/docsgen` is a reporting leaf — it may import
  broadly (config, tools, protocol, gateway, clientux, commandregistry,
  telegrambot, eventlog); nothing imports it except `cmd/fast-agent-harness`.
  Its `docs/architecture.md` row must say exactly that.
- Package hygiene: one file per generator inside `internal/docsgen`
  (`config.go`, `tools.go`, `routes.go`, `commands.go`, `events.go`,
  `packages.go`), each well under the hygiene gate; shared rendering helpers
  in `render.go`.
- If a registry resists tabling without changing semantics (the lifecycle
  validator is the known risk), do not force it: document the boundary in
  prose, record the blocker in this file, and move on.

## Task Index And Sequencing

- P0 Milestone 1 — Skeleton, First Generator, Drift Test: P0.1-P0.3
- P1 Milestone 2 — The Registry Generators: P1.1-P1.6
- P2 Milestone 3 — Deep Integration And The Hard Refactors: P2.1-P2.5

Sequencing rules:

- P0.1 → P0.2 → P0.3 strictly in order (skeleton, then first real generator,
  then the test that locks both in).
- Inside P1, tasks are independent EXCEPT: P1.4 (CLI table) should land before
  P2.3 (doctor split) since both edit `cmd/fast-agent-harness`; P1.5 (event
  table) before P2.2 (lifecycle diagram) since the diagram references the
  event-type table.
- P2.5 (final sweep of llms.txt / docs/README / AGENTS.md contract) is LAST —
  it describes the system only after the system exists.
- Every task ends with `go run ./cmd/fast-agent-harness docsgen -check` green,
  because every task either adds a generator or changes a registry a generator
  reads.

## P0 Milestone 1 - Skeleton, First Generator, Drift Test

Goal: stand up `internal/docsgen` + the `docsgen` subcommand, ship the single
highest-value generator (the config/env reference — 63 keys and 86 env aliases
that today appear in no docs at all), and lock the whole loop in with
`TestDocsCurrent`. After this milestone the system exists end to end and every
later generator is just another entry in a slice.

### P0.1 Scaffold internal/docsgen and the docsgen subcommand

Finding: there is no generation infrastructure at all — `//go:generate` is used
nowhere in the repo, and the closest existing prototypes are
`cmd/fast-agent-harness/tools_cmd.go:13` (`printTools()` dumps
`registry.Specs()` as JSON) and `cmd/fast-agent-harness/config_cmd.go:35,49`
(prints `Resolve().SanitizedValues()`). The deleted-by-005
`reference-plan.md` specified the right command shape (`docsgen -check` /
write, generated markers, regenerate-into-temp-then-diff) but with zero code
behind it. Subcommands are dispatched by a string switch at
`cmd/fast-agent-harness/main.go:21-59`.

Target files:

- `internal/docsgen/docsgen.go` (new)
- `internal/docsgen/render.go` (new)
- `internal/docsgen/docsgen_test.go` (new)
- `cmd/fast-agent-harness/docsgen_cmd.go` (new)
- `cmd/fast-agent-harness/main.go`
- `docs/architecture.md`
- `docs/generated/` (new directory)

Checklist:

- Re-verify anchors: `main.go:21-59` switch shape, `usage()` at `main.go:63`.
- Create `internal/docsgen/docsgen.go` with the core contract:
  `type Target struct { Name string; Filename string; Generate func() ([]byte, error) }`
  and `func Targets() []Target` returning the registered generators (empty or
  config-only for now). Keep it a plain slice — no plugin registry ceremony.
- In `render.go`, add shared helpers: markdown table writer with stable column
  widths, a `header(source string) []byte` emitting
  `<!-- GENERATED by fast-agent-harness docsgen; do not edit. Source: <pkg> -->`,
  and `sourceHashFooter(data any) []byte` that canonical-JSON-marshals the
  generator's input data (sorted keys) and appends
  `<!-- source-hash: <sha256 hex> -->`.
- Determinism rules enforced in helpers, not per generator: sort all map keys
  before rendering; forbid `time.Now()`/build info in output (add a unit test
  greping generated bytes for digits that look like dates is overkill — just
  never call them; the byte-compare drift test catches violations anyway).
- Create `cmd/fast-agent-harness/docsgen_cmd.go`: `func docsgenCmd(args []string) error`
  with its own `flag.NewFlagSet` (mirror `doctor.go:132` style), flags
  `-check` (diff, no write, exit non-zero on drift), `-only <name>` (scope to
  one target), `-out docs/generated` (default). Write mode: render each target
  to memory, then write files that changed; print a one-line summary per file
  (`written`, `unchanged`, or in check mode `stale`).
- Wire `case "docsgen": return docsgenCmd(args)` into `main.go`'s switch and
  add one line to `usage()` (`main.go:64-96`). (P1.4 later replaces both with
  the table; keep this edit minimal.)
- Create `docs/generated/` with a short hand-written `README.md` (3-5 lines:
  what this dir is, "never edit by hand", the regenerate command). The README
  is the ONLY non-generated file allowed in the directory.
- Add the `docsgen` row to `docs/architecture.md`'s `## Package Map` table
  (between `## Package Map` at `architecture.md:12` and the next `## `
  heading; 4 columns, package cell in backticks — the parser at
  `internal/architecture/architecture_test.go:212-296` requires this shape).
  Allowed imports for now: `config`, `protocol`, `tools`, `runtimehost`
  (grow the list per generator task, never pre-emptively).
- Basic tests in `docsgen_test.go`: Targets() names are unique; filenames are
  unique and end in `.md`; each target's Generate() is deterministic (call
  twice, byte-compare) — this test alone encodes the core invariant.

Verification:

```sh
go build ./...
go test -count=1 ./internal/docsgen
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen -check
go run ./cmd/fast-agent-harness help
git diff --check
```

### P0.2 Export the config key table with descriptions and ship the config reference generator

Finding: `internal/config/resolved.go:209` `configSpecs()` is a literal slice
of 63 `configSpec{Key, Env []string, Redacted, get, set}` entries
(`resolved.go:61`) built via 6 helper constructors (`stringSpec:277`,
`intSpec:284`, `int64Spec:295`, `boolSpec:306`, `durationSecondsSpec:317`,
`stringListSpec:328`) — exactly the iterable key-descriptor table docsgen
needs, but (a) it is unexported, (b) NO key has a human-readable description
anywhere in the codebase, (c) built-in defaults are only reachable via
unexported `builtInConfig()` (`defaults.go:217`) because later layers
overwrite the `SourceBuiltIn` provenance rows. 86 env alias names exist and
research confirmed almost none appear in README/ops docs. Coverage gap to
document, not to force into the table: 2 unexported override-tracking bools,
`WorkspaceRoots` (computed from cwd, `defaults.go:259`), and the 3 sub-registries
(`MCPServers` via `mcp.go:169`, `Hooks` via `hooks.go:74`,
`DiagnosticsCommands` via `diagnostics_commands.go:106`).

Target files:

- `internal/config/resolved.go`
- `internal/config/defaults.go`
- `internal/config/resolved_test.go`
- `internal/docsgen/config.go` (new)
- `internal/docsgen/config_test.go` (new)
- `docs/generated/config.md` (new, generated)

Checklist:

- Re-verify anchors post-005 (P2.9 may have split `Resolve()`): `configSpecs()`
  location, the 6 helper constructors, `builtInConfig()`.
- Extend the 6 spec helpers with a `desc string` parameter and write one-line
  descriptions for ALL 63 call sites (`resolved.go:209-275`). This is the
  grind part and the whole point: after this, help text exists in exactly one
  place, owned by the code. Style: imperative, ≤90 chars, no trailing period,
  e.g. `provider: which LLM backend serves requests (codex, deepseek, mock)`.
- Add exported projection next to `configSpecs()`:
  `type ConfigKeySpec struct { Key string; Env []string; Type string; Default string; Redacted bool; Description string }`
  and `func ConfigKeySpecs() []ConfigKeySpec` — Type derived from which helper
  built the spec (string/int/int64/bool/duration_seconds/string_list); Default
  rendered via `spec.get(builtInConfig())` formatted with the same logic
  `ResolvedValue` uses; Redacted keys render Default as `(redacted)`.
- Do NOT export `configSpec` itself (it carries `get`/`set` funcs); the
  projection is the public surface.
- Add `func BuiltIn() Config` (thin exported wrapper over `builtInConfig()`)
  only if the projection alone is insufficient — prefer keeping defaults
  inside `ConfigKeySpecs()`.
- Write `internal/docsgen/config.go`: target name `config`, filename
  `config.md`. Sections: (1) intro table of the 9 layer constants
  (`resolved.go:17-28`) in precedence order with one line each — the order is
  procedural in `Resolve()` (`resolved.go:69`: builtin → home config.toml →
  project `.billyharness/config.toml` → settings.json → dotenv → environment →
  CLI/gateway overrides → profile.toml → derived), so encode it here as a
  literal slice in the generator WITH a comment pointing at `Resolve()` and a
  test that fails if the number of Source consts changes (drift tripwire);
  (2) the 63-key table: Key / Type / Default / Env aliases / Redacted /
  Description; (3) a short "outside the table" section listing
  `WorkspaceRoots` (computed from cwd) and the 3 sub-registries with their
  config files (`mcp.config.toml`, `hooks.config.toml`, diagnostics) — sourced
  from code constants (`DefaultMCPConfigFile()` at `mcp.go:124`,
  `DefaultHookConfigFile()` at `hooks.go:70`), not hand-typed paths.
- Register the target in `Targets()`; run `docsgen` to produce
  `docs/generated/config.md`; commit the generated file.
- Tests: `ConfigKeySpecs()` count == number of entries in `configSpecs()`
  (guards forgotten descriptions — assert no Description is empty); every Env
  alias is unique across keys; generator output contains every key exactly
  once.
- Check `config_cmd.go:35,49` still compiles and behaves (it uses
  `SanitizedValues()`, untouched).

Verification:

```sh
go build ./...
go test -count=1 ./internal/config ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -only config
go run ./cmd/fast-agent-harness docsgen -check
go run ./cmd/fast-agent-harness config
git diff --check
```

### P0.3 Add TestDocsCurrent drift enforcement and wire docs/generated into the doc index

Finding: enforcement is the piece the old system got wrong — a 386-line Stop
hook policing hand-written JSON. The repo's real enforcement pattern is
`internal/architecture/architecture_test.go:29`: an ordinary test that fails
with an actionable message, running under the existing CI `verify` job
(`.github/workflows/ci.yml` runs `go test -count=1 ./...`, so a new test needs
zero CI edits). llms.txt's `## Machine Indexes` section (`llms.txt:53`) and
`docs/README.md` currently have no slot for generated references (the old
slot pointed at agent-index, deleted by 005).

Target files:

- `internal/docsgen/drift_test.go` (new)
- `llms.txt`
- `docs/README.md`
- `scripts/verify-local.sh`

Checklist:

- Write `TestDocsCurrent`: for each `Targets()` entry, `Generate()` into
  memory and byte-compare against `docs/generated/<Filename>`. On mismatch,
  fail with exactly:
  `docs/generated/<file> is stale; run: go run ./cmd/fast-agent-harness docsgen`.
  On a missing file, same message. Also fail on ORPHANS: any `*.md` in
  `docs/generated/` (except `README.md`) with no matching target — deleted
  generators must delete their output.
- Keep the test hermetic: generators must not need network, a live gateway, or
  MCP connections (the tool-catalog task P1.1 enforces this on the registry
  side). If a generator needs cwd-dependent data, pin cwd via `t.Chdir` to the
  repo root the way `architecture_test.go` locates files.
- Confirm the test runs in CI by construction (`go test ./...` in the verify
  job of `.github/workflows/ci.yml`) — no workflow edits; state this in the
  commit message.
- Add a `docsgen -check` step to `scripts/verify-local.sh` (its `usage()`
  block at `:15-29` is the authoritative step list — update both the block
  and the implementation) right after the vet step. It is redundant with
  TestDocsCurrent but gives a faster, named failure in the local gate.
- llms.txt: where 005 removed `## Machine Indexes`, add `## Generated
  References` listing `docs/generated/config.md` (and later files as their
  generators land — each P1 generator task appends its own line) plus the
  regenerate command. Keep it to one line per file.
- `docs/README.md`: add a `## Generated` section mirroring the same list, with
  the one rule ("edit code, not these files; TestDocsCurrent enforces").
- Do NOT add generated files to hygiene exclusions or gitignore — they are
  committed, reviewed artifacts; drift shows up in `git diff` like any code.

Verification:

```sh
go test -count=1 ./internal/docsgen -run TestDocsCurrent
go run ./cmd/fast-agent-harness docsgen -check
sh -c 'sed -i.bak "s/^/x/" docs/generated/config.md && ! go test -count=1 ./internal/docsgen -run TestDocsCurrent && mv docs/generated/config.md.bak docs/generated/config.md'
./scripts/verify-local.sh || true  # full local gate; inspect the docsgen step
git diff --check
```

## P1 Milestone 2 - The Registry Generators

Goal: turn the remaining five sources of truth into generated references, doing
the minimal "expose the table, make the runtime range over the same table"
refactor wherever a registry is currently locked inside imperative code. Each
task registers one target, commits its generated file, appends one line to
llms.txt's Generated References and docs/README's Generated section, and adds
any new allowed import to docsgen's row in `docs/architecture.md`.

### P1.1 Ship the tool catalog generator and unify the duplicated risk vocabulary

Finding: the native tool registry is iterable as-is — `Registry.Specs()`
(`internal/tools/tools.go:595`) yields `protocol.ToolSpec{Name, Description,
Parameters json.RawMessage, Risk}` (`internal/protocol/types.go:60`), enriched
per tool by `PolicyDecision(name)` (`internal/tools/policy.go:22`, whose
`Metadata()` at `:141` exposes `risk_class`) and `ParallelMetadata(name)`
(`tools.go:640`); `cmd/fast-agent-harness/tools_cmd.go:13` `printTools()`
already builds a registry and dumps `Specs()` — a working prototype. Schemas
are inline JSON-string literals via `raw()` (`tools.go:1536`), so
pretty-printing preserves authored key order (deterministic). Two warts: the
11 `protocol.Risk` consts (`types.go:14-27`, two tiers: 5 coarse native + 6
granular MCP) have no exported master list and no doc comments; and the MCP
risk-string normalization exists TWICE (`internal/config/mcp.go:356`
`normalizeMCPToolRisk` returning strings vs `internal/mcpclient/catalog.go:142`
`normalizeMCPRisk` returning typed consts) — adding a risk class means editing
both plus 4 switches in `policy.go`. Dynamic MCP catalogs need a live server
connection (`discoveryCandidates` at `tools.go:1083` requires a connected
manager) and are OUT of static docsgen scope.

Target files:

- `internal/protocol/types.go`
- `internal/config/mcp.go`
- `internal/mcpclient/catalog.go`
- `internal/mcpclient/catalog_test.go`
- `internal/docsgen/tools.go` (new)
- `docs/generated/tools.md` (new, generated)
- `docs/architecture.md`

Checklist:

- Re-verify anchors; 005 P1.10 folds `tooloutput` into `tools`, so the tools
  package shape may have shifted.
- In `internal/protocol/types.go`, add doc comments to all 11 `Risk` consts
  and an exported ordered list
  `func RiskClasses() []Risk` (coarse tier first, then granular) — the
  generator's vocabulary section and future switches consume this.
- Unify normalization: add `func ParseRisk(value string) (Risk, bool)` in
  `internal/protocol` (single string→const mapping); rewrite
  `config.normalizeMCPToolRisk` (`mcp.go:356`) and
  `mcpclient.normalizeMCPRisk` (`catalog.go:142`) as thin wrappers over it;
  update the `policy.go` switches to range over `RiskClasses()` where they
  currently hand-enumerate. Existing tests in both packages pin behavior —
  they must pass unchanged.
- Write `internal/docsgen/tools.go`: build the registry exactly the way
  `printTools()` does (via `runtimehost` with MCP disabled — the generator
  must never spawn MCP servers; assert the constructor used cannot connect).
  Emit `docs/generated/tools.md`: (1) risk vocabulary table from
  `RiskClasses()` with the new doc comments; (2) per-tool sections sorted by
  name: description, risk, risk_class from `PolicyDecision`, parallel
  metadata, and the JSON schema in a fenced block via `json.Indent` on
  `Spec.Parameters`; (3) an MCP appendix generated from CONFIG not live
  servers: the `[mcp_servers.<name>]` shape (export the `defaultMCPConfig`
  template at `mcp.go:140` or add a getter), `DefaultToolRisk`/`ToolRisks`
  semantics, and the `enabled_tools` side-effect gate — one paragraph each,
  sourced from the config structs' fields (`MCPServer` at `mcp.go:13`).
- State in the appendix explicitly: live MCP tool catalogs are runtime-only,
  inspect with `tool_search`/`mcp_list_tools`; docsgen documents policy, not
  remote inventories.
- Register target `tools`; generate; commit; append the llms.txt +
  docs/README lines; extend docsgen's allowed imports in
  `docs/architecture.md` (adds `mcpclient` only if actually imported — prefer
  not to; config-level types should suffice).
- Tests: every registered native tool appears exactly once in the output
  (count against `Specs()`); `ParseRisk` round-trips all `RiskClasses()`;
  output contains no `time.` artifacts; generator is deterministic (two runs
  byte-equal — inherited from the P0.1 suite once registered).

Verification:

```sh
go build ./...
go test -count=1 ./internal/protocol ./internal/config ./internal/mcpclient ./internal/tools ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -only tools
go run ./cmd/fast-agent-harness docsgen -check
go run ./cmd/fast-agent-harness tools
go test -count=1 ./internal/architecture
git diff --check
```

### P1.2 Refactor routes() into a ranged []routeSpec and ship the gateway API reference

Finding: `internal/gateway/gateway.go:294` `routes()` is 27 uniform
`s.mux.HandleFunc("METHOD /path", s.handler)` literals (Go 1.22 ServeMux
patterns with `{id}` wildcards) — the ONLY registration site in the package,
but ServeMux exposes no pattern list, so the data is locked in code. Auth is
structural, not per-route: `httpSecurityMiddleware` (`http_security.go:37`)
bypasses `/health` explicitly (`:39-42`), scopes bearer/origin checks to
`/v1/*` via `isGatewayV1Route` (`:83-85`), and requires bearer (or loopback
dev-bypass, `:62-66`) only for mutations via `isGatewayMutation` (`:87-97`).
DTOs follow the `<Noun>Request`/`<Noun>Response` convention in
`internal/gatewayapi/types.go` but the route↔DTO mapping is declared nowhere
(and is not strictly 1:1 — `SessionUndoResponse` at `types.go:399` serves both
undo and redo).

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/http_security.go`
- `internal/gateway/routes_test.go` (new or extend existing route tests)
- `internal/docsgen/routes.go` (new)
- `docs/generated/gateway-api.md` (new, generated)
- `docs/architecture.md`

Checklist:

- Re-verify anchors; 005 P1.9/P1.13/P2.2/P2.9 all touch `gateway.go` — this
  task MUST rebase on their final shape (005 P2.2 deletes the
  `GET /v1/benchmarks` route; do not resurrect it).
- Define in `gateway.go`:
  `type routeSpec struct { Method, Pattern string; Handler http.HandlerFunc; Summary string; Request, Response string }`
  and `func (s *Server) routeSpecs() []routeSpec` returning all routes with
  one-line summaries and DTO type names as strings (`""` where a route has no
  body; document shared DTOs verbatim, e.g. undo and redo both naming
  `SessionUndoResponse`).
- Rewrite `routes()` to a single range loop:
  `for _, r := range s.routeSpecs() { s.mux.HandleFunc(r.Method+" "+r.Pattern, r.Handler) }`.
  Grep the package to confirm no other `HandleFunc` call sites exist.
- Export the auth classification from the REAL predicates — add to
  `http_security.go`:
  `func AuthClassFor(method, pattern string) string` returning one of
  `public` (health bypass), `local-read` (v1 non-mutation), `bearer-mutation`
  (v1 mutation) — implemented by calling `isGatewayV1Route` /
  `isGatewayMutation` / the health bypass condition, NEVER by re-encoding
  their logic. If the predicates need a `*http.Request`, build a synthetic one
  from method+pattern with wildcards filled (`{id}` → `x`).
- Add an exported wrapper `func (s *Server) RouteDocs() []RouteDoc` (doc-only
  projection: Method, Pattern, Summary, Request, Response, AuthClass) for
  docsgen; keep `routeSpec` itself unexported since it carries handlers.
- Write `internal/docsgen/routes.go`: construct a `gateway.Server` the
  cheapest honest way (reuse whatever constructor the gateway's own handler
  tests use — no listener, no session replay; if construction is heavy after
  005 P1.13's lazy-startup work, that work makes this cheap). Emit
  `docs/generated/gateway-api.md`: table Method / Path / Auth / Request /
  Response / Summary, sorted by path then method, plus a hand-written-in-code
  intro paragraph on the middleware model (health bypass, bearer-on-mutation,
  loopback dev bypass) with pointers to `http_security.go`.
- Tests: route count in `RouteDocs()` equals registered pattern count (spy on
  a test mux); `AuthClassFor("GET", "/health") == "public"`;
  `AuthClassFor("POST", "/v1/sessions/{id}/undo") == "bearer-mutation"`;
  existing gateway route/auth tests pass unchanged (they are the semantic
  pin — this refactor must be behavior-neutral).
- Register target `gateway-api`; generate; commit; append index lines; add
  `gateway` + `gatewayapi` to docsgen's allowed imports row.

Verification:

```sh
go build ./...
go test -count=1 ./internal/gateway
go test -race -count=1 ./internal/gateway
go test -count=1 ./internal/docsgen ./internal/architecture
go run ./cmd/fast-agent-harness docsgen -only gateway-api
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P1.3 Export doc projections for TUI actions and Telegram commands; ship the unified commands reference

Finding: three command surfaces share one ancestor but only one is fully
public. `clientux.ActionDefinitions()` (`internal/clientux/actions.go:18`, 32
exported entries) feeds both `tui.actionRegistry()` (`internal/tui/actions.go:43`,
42 unexported `actionSpec`s — the extra 10-11 are keybinding-only actions like
`viewport.page_up`/`palette.open`, and all keybinding/keyAliases/keySummary
detail is unexported) and `telegramCommands()`
(`internal/telegrambot/commands.go:62`, 21 unexported `telegramCommandSpec`s
with a 4-value `telegramCommandClass` policy enum at `command_policy.go:5-12`).
`commandregistry.Build().Entries()` (`registry.go:60,90`) is already fully
exported and JSON-tagged — the model the others should copy. The Telegram
package even contains a tiny hand-rolled docsgen already:
`telegramCommandHelpHTML()` (`commands.go:211-233`) iterates the spec table to
render `/help`.

Target files:

- `internal/tui/actions.go` (plus a new `internal/tui/action_docs.go`)
- `internal/telegrambot/commands.go` (plus a new `internal/telegrambot/command_docs.go`)
- `internal/tui/actions_test.go`, `internal/telegrambot/commands_test.go`
- `internal/docsgen/commands.go` (new)
- `docs/generated/commands.md` (new, generated)
- `docs/architecture.md`

Checklist:

- Re-verify anchors; 005 P2.10/P2.12/P2.13/P2.14 touch both surfaces (P2.14
  adds `/models` to Telegram — it must appear in the generated table).
- Add `internal/tui/action_docs.go`:
  `type ActionDoc struct { ID, Title, Category, Keybinding string; KeyAliases []string; Slash, SlashArgs string; SlashAliases, TelegramAliases []string; Summary string }`
  and `func ActionDocs() []ActionDoc` projecting `actionRegistry()` (which
  already passes through `withSharedActionDefinitions` at `actions.go:740`, so
  titles/slash metadata are the merged truth). Do not export `actionSpec` — it
  carries `run`/`keyRun` funcs and `enabled` closures.
- Add `internal/telegrambot/command_docs.go`:
  `type CommandDoc struct { ActionID string; Aliases []string; Usage, Summary, Class string; BypassRunLock bool }`
  and `func CommandDocs() []CommandDoc` projecting `telegramCommands()`;
  render `Class` via a `String()` on `telegramCommandClass`
  (public/session-scoped/operator-only/owner-only). Refactor
  `telegramCommandHelpHTML()` to consume `CommandDocs()` so `/help` and the
  generated doc share one projection.
- Write `internal/docsgen/commands.go` emitting `docs/generated/commands.md`,
  three sections: (1) cross-surface table keyed by action ID — columns
  ID / TUI slash / TUI keys / Telegram aliases / Class / Summary — built by
  joining `tui.ActionDocs()` × `telegrambot.CommandDocs()` ×
  `clientux.ActionDefinitions()`; rows present on only one surface are marked
  `-` on the other, which makes surface drift VISIBLE in review (this is the
  005-backlog "command drift" checker, for free); (2) keybinding-only TUI
  actions; (3) the command classes with one-line policy meaning each, plus a
  note that `commandregistry.Build()` merges actions + prompt commands +
  profiles + MCP prompts at runtime (`registry.go:49-88`) and those dynamic
  entries are runtime-only (`/commands` lists them live).
- Tests: `ActionDocs()` length equals `actionRegistry()` length;
  `CommandDocs()` length equals `telegramCommands()` length; every
  TelegramAliases entry in clientux appears in some `CommandDoc.Aliases`
  (bidirectional drift guard); `/help` output still contains every command
  alias (pins the helpHTML refactor).
- Register target `commands`; generate; commit; append index lines; add
  `tui`, `telegrambot`, `clientux`, `commandregistry` to docsgen's allowed
  imports (check none of them drags a heavy transitive edge that violates the
  architecture table — if `telegrambot` does, keep the projection in
  `telegrambot` but have docsgen shell nothing: importing is fine, docsgen is
  a leaf).

Verification:

```sh
go build ./...
go test -count=1 ./internal/tui ./internal/telegrambot ./internal/clientux ./internal/commandregistry ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -only commands
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./internal/architecture
git diff --check
```

### P1.4 Replace the CLI switch with a subcommand table so usage() and cli.md generate themselves

Finding: `cmd/fast-agent-harness/main.go:17` `run()` dispatches via a string
switch (`main.go:21-59`, 17 subcommands incl. aliases `serve|gateway`,
`doctor|health`, `sessions|session`, `commands|command`), and `usage()`
(`main.go:63-96`) hand-duplicates the same list as `fmt.Println` lines — two
copies of the truth with no drift guard, and the exact shape (a data table the
runtime ranges over) that every other task in this milestone builds. Each
subcommand already owns its flags via internal `flag.NewFlagSet` (e.g.
`doctor.go:132`), so the table only needs name/aliases/summary/run.

Target files:

- `cmd/fast-agent-harness/main.go`
- `cmd/fast-agent-harness/main_test.go`
- `internal/docsgen/cli.go` (new)
- `docs/generated/cli.md` (new, generated)

Checklist:

- Re-verify anchors (P0.1 added the `docsgen` case to this switch).
- Define in `main.go` (or a new `subcommands.go` in the same package):
  `type subcommand struct { Name string; Aliases []string; Summary string; Run func([]string) error }`
  and `var subcommands = []subcommand{...}` covering all existing cases in the
  current switch order, `docsgen` included. Preserve the no-arg default
  (`serve(nil)`, `main.go:18-20`) and the unknown-command error text exactly
  (`main_test.go` at 769 lines pins much of this behavior — read it first).
- Rewrite `run()` as a lookup over the slice (name or alias match), and
  `usage()` as a range over the same slice — delete every hand-written
  command line from it.
- Docsgen needs the table without importing package `main` (impossible):
  since the table is pure data, move it to `internal/clientux` or a tiny new
  `internal/climeta` ONLY if that stays honest… it does not (Run funcs point
  at main-package code). Instead: emit `cli.md` from a build-tag-free
  `go run ./cmd/fast-agent-harness help -machine` path — add a hidden
  `help -machine` mode that prints the table as JSON, and have
  `internal/docsgen/cli.go` exec the CURRENT binary? No — docsgen must not
  shell out (determinism, hermetic test). Resolution: put the DOC HALF of the
  table (Name/Aliases/Summary — no Run funcs) in `internal/clientux` as
  `func CLICommandDocs() []CLICommandDoc`, and have `main.go`'s `subcommands`
  slice be built FROM it by attaching Run funcs, failing at init if any doc
  entry lacks a Run or vice versa. One data source, runtime attaches behavior,
  docsgen imports `clientux` only.
- Write `internal/docsgen/cli.go` emitting `docs/generated/cli.md`: table
  Name / Aliases / Summary, plus a fixed intro line pointing at
  `<cmd> -h` for per-command flags (flags stay owned by each FlagSet; do NOT
  try to enumerate them — that is runtime behavior, not registry data).
- Tests: init-time cross-check main↔clientux table (every doc has a Run,
  every Run has a doc) expressed as a normal test in `main_test.go`; `usage()`
  output contains every Name exactly once; unknown-command error text
  unchanged.
- Register target `cli`; generate; commit; append index lines.

Verification:

```sh
go build ./...
go test -count=1 ./cmd/fast-agent-harness ./internal/clientux ./internal/docsgen
go run ./cmd/fast-agent-harness help
go run ./cmd/fast-agent-harness nonsense-command; test $? -ne 0
go run ./cmd/fast-agent-harness docsgen -only cli
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P1.5 Table-drive the event-type vocabulary and ship the protocol events reference

Finding: the 37 `EventType` string consts live in one flat block
(`internal/protocol/types.go:94-132`) — trivially enumerable — but the
knowledge ABOUT them is smeared across two hidden switches:
`isKnownEventType` (`envelope.go:201`, membership) and
`ValidateEventEnvelope`'s required-correlation-ID cases
(`envelope.go:109`, cases at `:138-161`: which of run_id/turn_id/step_id/
call_id/attempt_id each type must carry). The 16 typed payload structs
(`TurnEvent:257` … `ContextThresholdEvent:563`) map to event types only inside
decoder/projector code. `EventSource` consts sit at `envelope.go:16-25`.
Adding an event type today means editing 2+ switches with no compiler help;
docsgen wants the same facts as data.

Target files:

- `internal/protocol/types.go`
- `internal/protocol/envelope.go`
- `internal/protocol/envelope_test.go`
- `internal/docsgen/events.go` (new)
- `docs/generated/events.md` (new, generated)

Checklist:

- Re-verify anchors AFTER 005 P1.11 (it moves decoders INTO protocol — the
  payload↔type mapping may already be materializing there; reuse it if so).
- Add to `internal/protocol`:
  `type EventTypeSpec struct { Type EventType; RequiredIDs []string; Payload string; Doc string }`
  and `var eventTypeSpecs = [...]EventTypeSpec{...}` covering all 37 types —
  RequiredIDs transcribed case-by-case from `ValidateEventEnvelope`'s switch;
  Payload naming the types.go struct (empty where Data is ad-hoc); Doc a
  one-liner per type.
- Rewrite `isKnownEventType` as a map lookup built once from
  `eventTypeSpecs`; rewrite the required-ID portion of
  `ValidateEventEnvelope` to consult `spec.RequiredIDs` generically. Any case
  with logic BEYOND flat required-IDs (conditional combos) stays as code —
  transcribe faithfully, do not simplify semantics; the existing
  `envelope_test.go` cases are the pin and must pass unchanged. If a case
  cannot be expressed as flat RequiredIDs, keep it procedural and mark the
  spec entry `RequiredIDs: nil` with a Doc note — do not force.
- Export `func EventTypeDocs() []EventTypeSpec` (copy).
- Write `internal/docsgen/events.go` emitting `docs/generated/events.md`:
  (1) envelope field reference from the `Event` struct (`types.go:220`) —
  hand-listed in the generator with a struct-field-count tripwire test;
  (2) the 37-type table: Type / Required IDs / Payload struct / Doc;
  (3) `EventSource` values; (4) a pointer to `docs/architecture/runtime-event-system.md`
  for semantics (prose stays prose).
- Tests: `len(EventTypeDocs()) == 37` (update alongside any new type — that
  is the point); every `EventType` const appears in specs (AST-free check:
  call `isKnownEventType` for each spec and assert true, plus a
  known-vs-spec-count equality); `ValidateEventEnvelope` table-driven cases
  from the existing test file pass byte-identically.
- Register target `events`; generate; commit; append index lines; add
  `protocol` to docsgen imports row (likely already there from P0).

Verification:

```sh
go build ./...
go test -count=1 ./internal/protocol ./internal/eventlog ./internal/gateway
go test -count=1 ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -only events
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P1.6 Ship the package map + reverse import index generator (descriptive, not the allowlist)

Finding: `docs/architecture.md`'s `## Package Map` (heading at `:12`, 4
columns, 50 rows at `:16-65`) is the hand-maintained INTENT table — the
allowlist `internal/architecture` enforces (`architecture_test.go:212` parses
it line-wise; `:29` compares against `go list -json ./internal/...` via
`listPackages` at `:157`). Intent must stay hand-written (a generated
allowlist would always pass — it would be reality approving reality). What is
MISSING is the descriptive layer: actual package purposes (Go doc comments),
actual import edges, and the reverse index ("who imports checkpoint?") that
agents currently reconstruct with grep every time. The repo has no
`//go:generate` and no package-doc extraction anywhere.

Target files:

- `internal/docsgen/packages.go` (new)
- `internal/docsgen/packages_test.go` (new)
- `docs/generated/packages.md` (new, generated)
- `docs/architecture.md` (one paragraph, not the table)

Checklist:

- Implement package enumeration WITHOUT shelling out if possible: prefer
  `golang.org/x/tools/go/packages`… which would add a dependency — instead
  reuse the repo's own proven pattern: shell `go list -json ./internal/... ./cmd/...`
  exactly like `architecture_test.go:157` does. Determinism note: `go list`
  output is stable for a fixed tree; TestDocsCurrent runs it too — measure
  once, and if the drift test slows past ~2s, cache the JSON parse within the
  process (both consumers run in one test binary).
- Extract each package's doc comment via stdlib `go/parser` (`ParseDir`,
  `ParseComments`) + `go/doc` on the package's directory — first sentence
  only. Packages with NO doc comment render `-`; then ADD one-sentence doc
  comments to every currently-undocumented internal package (this is the
  grind half: ~50 packages, one `// Package x ...` line each — after this,
  `go doc` works repo-wide for the first time).
- Emit `docs/generated/packages.md`: (1) table Package / Doc sentence /
  Direct internal imports (from `go list` Deps filtered to module-internal,
  DIRECT only); (2) reverse index: for each package, who imports it —
  computed by inverting (1); (3) totals line (package count, edge count) —
  a cheap sprawl metric to watch shrink as 005's collapses land.
- `docs/architecture.md`: add one paragraph under the Package Map intro
  stating the division of labor — this table = hand-written intent/allowlist
  (enforced by `internal/architecture`); `docs/generated/packages.md` =
  generated reality (purposes + actual edges); change either side's world and
  the corresponding guard (architecture test / drift test) goes red.
- Do NOT modify `architecture_test.go` — its markdown parsing contract is
  untouched by this task.
- Tests: generated package set equals the architecture test's `go list` set
  (same patterns → same result — cross-pin the two guards); reverse index is
  the exact inverse of the forward edges; every package row has a non-empty
  doc sentence after the doc-comment pass.
- Register target `packages`; generate; commit; append index lines.

Verification:

```sh
go build ./...
go vet ./...
go test -count=1 ./internal/docsgen ./internal/architecture
go run ./cmd/fast-agent-harness docsgen -only packages
go run ./cmd/fast-agent-harness docsgen -check
go doc ./internal/checkpoint | head -3
git diff --check
```

## P2 Milestone 3 - Deep Integration And The Hard Refactors

Goal: connect docsgen to production reality (`doctor -docs`), take on the two
refactors that need real surgery (lifecycle transition table, doctor check
descriptors), fix the settings.json triple-shape so the config reference tells
the whole truth, and finish by rewriting the documentation contract docs to
describe the system that now exists. P2.5 is last on purpose.

### P2.1 Add doctor -docs: compare the live binary's registries against committed generated docs

Finding: TestDocsCurrent catches drift at development time, but the VPS runs a
BINARY — after a deploy, the docs in the repo working tree can describe a
different build than the one systemd is running (the same skew class the
dated production-inventory snapshot tried and failed to solve by hand, deleted
in 005 P1.1). Every generated file already carries
`<!-- source-hash: <hex> -->` (P0.1's `sourceHashFooter`: sha256 over the
canonical JSON of the generator's INPUT data, not the markdown), so a running
binary can recompute its own hashes and compare without regenerating markdown.
Doctor's report is assembled in `collectDoctorReport`
(`cmd/fast-agent-harness/doctor.go:193-231`) and rendered from `doctorCheck`
structs (`doctor.go:111-116`).

Target files:

- `internal/docsgen/docsgen.go`
- `internal/docsgen/fingerprint.go` (new)
- `cmd/fast-agent-harness/doctor.go`
- `cmd/fast-agent-harness/doctor_test.go`
- `docs/generated/README.md`

Checklist:

- Split each Target's pipeline so the input-data fingerprint is computable
  without rendering: extend `Target` with `Fingerprint func() (string, error)`
  (P0.1 designed `sourceHashFooter` around canonical JSON — reuse the same
  bytes; add a test asserting `Fingerprint()` equals the hash embedded by
  `Generate()`).
- Add `func VerifyAgainst(dir string) []TargetStatus` in
  `internal/docsgen/fingerprint.go`: for each target, parse the
  `source-hash` footer from `dir/<Filename>` and compare with the live
  `Fingerprint()`; statuses `ok / stale / missing / unreadable`.
- Wire a `-docs` flag into `doctorCmd` (`doctor.go:131`) adding one
  `doctorCheck` per target (`Name: "docs:" + target.Name`), plus include it in
  `-deep` mode. Failure detail names the stale file and the regenerate
  command. Skip (status `n/a`, not fail) when `docs/generated/` is absent —
  production deploys the binary, not necessarily the repo; the check is for
  repo-checkout hosts like the VPS's `/root/billyharness`.
- Note the boundary honestly in `docs/generated/README.md`: `doctor -docs`
  proves registry-data freshness, not prose freshness.
- Tests: fingerprint round-trip per target; `VerifyAgainst` on a doctored
  stale file reports `stale`; doctor `-docs` output includes every target
  name.

Verification:

```sh
go build ./...
go test -count=1 ./internal/docsgen ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness doctor -docs -build=false -services=false -gateway=false
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P2.2 Extract a declarative lifecycle transition table and generate the state diagrams

Finding: this is the hardest and most valuable refactor, flagged as
NOT data-iterable by research. `LifecycleValidator.Observe`
(`internal/eventlog/eventlog.go:206`, switch from `:218` to ~`:438`) encodes
every legality rule imperatively against 10 state maps
(`eventlog.go:140-158`: runs/terminalRun/turns/terminalTurn/steps/
terminalStep/calls/attempts/attemptCalls/terminalAttempts/userInputs, with
composite `\x00` keys at `:673-690`). Entities: run, turn, step, call,
attempt, user_input (existence-only), hook (opportunistic). Two rule classes
must be separated: STRUCTURAL (parent must exist, no duplicate start, no
events after terminal, which types terminate which entity — tableable) and
DATA-DEPENDENT (`lifecycleDataString` phase inspection at `:335`,
`allowedPostTerminalProgressPhase` at `:539` — stays procedural). The eventlog
test suite is large and is the semantic pin. 005 verification history warns
against superseding ADR-anchored behavior — this task changes REPRESENTATION,
not semantics, and must prove it.

Target files:

- `internal/eventlog/lifecycle_rules.go` (new)
- `internal/eventlog/eventlog.go`
- `internal/eventlog/eventlog_test.go`
- `internal/docsgen/events.go`
- `docs/generated/events.md` (regenerated)

Checklist:

- Re-verify anchors; read the ENTIRE Observe switch and the existing tests
  before writing any code; inventory every case into structural vs
  data-dependent on paper (in this file, as a checklist edit) first.
- Define in `lifecycle_rules.go`:
  `type lifecycleRule struct { Event protocol.EventType; Entity string; Kind ruleKind; Parent string; Terminal bool }`
  with `ruleKind` ∈ {starts, progresses, terminates} — derive the exact field
  set from the paper inventory; the shape above is the starting hypothesis,
  and the task owner may adjust it, but the END state is fixed: one exported
  `func LifecycleRules() []lifecycleRuleDoc` and an `Observe` whose STRUCTURAL
  checks run by consulting the table.
- Refactor `Observe` incrementally, one entity at a time (run → turn → step →
  call/attempt), keeping the full test suite green after each entity; keep
  data-dependent branches inline where they are.
- `ValidateClosed` (`eventlog.go:442`) should need no change; if it does, stop
  and re-derive — that is a semantics smell.
- Extend `internal/docsgen/events.go`: append a section to
  `docs/generated/events.md` rendering one mermaid `stateDiagram-v2` per
  entity from `LifecycleRules()` (start edges, progress self-loops collapsed,
  terminal states), plus a one-line note that phase-conditional rules
  (post-terminal progress) are procedural and tested, not diagrammed.
- Tests: a generated-rules sanity test — every `EventType` consumed by
  `Observe`'s structural paths appears in exactly one rule; replay a golden
  session JSONL through old-vs-new validator behavior by keeping the OLD
  switch temporarily behind a test-only build tag OR (simpler) trusting the
  existing suite plus new table-driven negative cases (event before parent,
  double terminal, post-terminal event) per entity.
- ESCAPE HATCH (canon: do not force): if after the paper inventory more than
  ~30% of structural cases cannot be expressed in one rule shape, STOP —
  record the inventory and the blocker in this file under this task, ship
  only the diagram for the entities that DID table cleanly, and leave Observe
  alone for the rest. A partially generated diagram from a real table beats a
  fully generated diagram from a lying one.

Verification:

```sh
go build ./...
go test -count=1 ./internal/eventlog
go test -race -count=1 ./internal/eventlog
go test -count=1 ./internal/gateway ./internal/trace
go run ./cmd/fast-agent-harness docsgen -only events
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P2.3 Split doctor checks into descriptor + executor and generate doctor.md

Finding: doctor check identity exists only as a side effect of execution —
`collectDoctorReport` (`doctor.go:193-231`) appends results from a fixed call
sequence (`doctorConfigChecks:263` wrapping effective-config/gateway-bind/
active-auth, `doctorToolCatalogStatus:322`, `doctorSessionStoreAccessCheck:331`,
`doctorGitStatus:515`, `doctorBuildStatus:538`, `doctorServiceStatuses:559`
iterating `doctorManagedServices():598`, `doctorGatewayStatuses:851`), each
producing `doctorCheck{Name, Status, Detail, DurationMS}` (`doctor.go:111-116`)
with Name literals buried inside. Nobody can answer "what does doctor check?"
without running it or reading 1,152 lines — doctor.go is also the ops-ci
reader's top grown-not-designed file. Same medicine as P1.4: descriptor table,
runtime ranges over it.

Target files:

- `cmd/fast-agent-harness/doctor.go` (plus new `doctor_checks.go` in the same package)
- `cmd/fast-agent-harness/doctor_test.go`
- `internal/clientux/cli_docs.go` (or wherever P1.4 put `CLICommandDocs`)
- `internal/docsgen/cli.go`
- `docs/generated/cli.md` (regenerated, gains a doctor section)

Checklist:

- Re-verify anchors; land AFTER P1.4 (same package, same table philosophy) and
  after any 005 doctor changes.
- Define `type doctorCheckSpec struct { Name, Description string; Modes []string; Run func(*doctorContext) []doctorCheck }`
  (Modes ∈ local/production/deep — transcribe from today's `-mode`/`-deep`
  conditionals in `collectDoctorReport`); build
  `var doctorCheckSpecs = []doctorCheckSpec{...}` and make
  `collectDoctorReport` a range that honors Modes exactly as the current
  conditionals do. One spec may emit several `doctorCheck` results (services
  iterate units) — that is why Run returns a slice.
- Extract the doc half (`Name, Description, Modes`) to the same exported home
  as P1.4's CLI docs (`clientux.DoctorCheckDocs()`), cross-pinned by an
  init-consistency test like P1.4's.
- Extend `internal/docsgen/cli.go`: `cli.md` gains a "What doctor checks"
  table (Name / Modes / Description). P2.1's `docs:*` checks must appear —
  their spec entries come from `docsgen.Targets()` at init.
- Behavior pin: `doctor -json` output shape and check Names must be
  byte-compatible before/after (existing `doctor_test.go:347` cases + add a
  golden Names list test).
- Opportunistic but in-scope: move the specs and their Run funcs into
  `doctor_checks.go`, leaving doctor.go with CLI/rendering — a first slice off
  the 1,152-line file without a redesign.

Verification:

```sh
go build ./...
go test -count=1 ./cmd/fast-agent-harness ./internal/clientux ./internal/docsgen
go run ./cmd/fast-agent-harness doctor -build=false -services=false -gateway=false
go run ./cmd/fast-agent-harness doctor -json -build=false -services=false -gateway=false | head -20
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P2.4 Unify the three settings.json shapes behind one canonical struct and document it

Finding: `settings.json` — the file that stores Billy's model/reasoning/profile
picks — is hand-unmarshaled into three independent anonymous/private shapes:
`resolveState.applyBillySettings()` (`internal/config/resolved.go:368`),
`Config.ApplyBillySettingsDefaults()` (`internal/config/defaults.go:180`, the
`MustResolve` fallback path), and the TUI's own
`appSettings`/`loadAppSettings()`/`saveAppSettings()`
(`internal/tui/settings.go:36,98`) which also WRITES the file. Three copies of
key names (`last_selected_model`, `last_reasoning_kind`,
`last_reasoning_effort`, `last_profile`, `context_window_tokens`,
`context_compact_tokens`) can silently diverge — a new key added to the TUI
writer but not the config readers just vanishes. The generated config.md
(P0.2) documents the settings layer, so its truthfulness depends on this fix.

Target files:

- `internal/config/settings.go` (new)
- `internal/config/resolved.go`
- `internal/config/defaults.go`
- `internal/tui/settings.go`
- `internal/config/settings_test.go` (new), `internal/tui/settings_test.go`
- `internal/docsgen/config.go`
- `docs/generated/config.md` (regenerated)

Checklist:

- Re-verify anchors post-005 (P2.9 splits Resolve; the settings-apply site may
  have moved).
- Define `config.BillySettings` (exported struct, json tags, all six keys +
  any the TUI shape has that the config shapes lack — diff all three first
  and reconcile the union; a TUI-only display key stays TUI-only ONLY if it
  is genuinely presentation state, and then it must be documented as such).
- Add `config.LoadBillySettings(path) (BillySettings, error)` and
  `config.SaveBillySettings(path, BillySettings) error` — atomic write
  (temp+rename), preserving unknown fields is NOT required (settings.json is
  owned by this program alone; document that ownership in the struct comment).
- Rewrite all three sites to consume the canonical struct; the TUI keeps its
  own load/save call sites but not its own struct or key strings; delete the
  legacy magic (`legacySettingsContextWindowTokens` handling from
  `config_test.go`'s pinned edge cases stays working — those tests are the
  pin, do not delete them).
- Extend the config generator: a `settings.json` section in config.md listing
  each `BillySettings` field (json tag, type, what writes it) — sourced by
  reflecting over the exported struct, no hand-typed key list.
- Tests: round-trip Load(Save(x)) == x; the three former shapes' test cases
  all pass against the unified struct; generated config.md contains every
  BillySettings json tag.

Verification:

```sh
go build ./...
go test -count=1 ./internal/config ./internal/tui
go run ./cmd/fast-agent-harness docsgen -only config
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

### P2.5 Rewrite the documentation contract to describe the system that now exists

Finding: after P0-P2.4, the repo has a real generated layer, but the contract
docs still describe the pre-006 world: `AGENTS.md`'s Documentation System
section and `.agents/rules/documentation.md` were rewritten by 005 P0.1 into a
short no-ceremony paragraph (good, but it predates docsgen);
`llms.txt` accumulated Generated References lines task-by-task and needs one
coherent pass; `docs/README.md`'s `## Known Documentation Gaps` (`:74`) still
lists gaps that generators now close. This task is LAST because contract docs
describing an unfinished system are how reference-plan.md happened.

Target files:

- `AGENTS.md`
- `.agents/rules/documentation.md`
- `llms.txt`
- `docs/README.md`
- `docs/generated/README.md`
- `loop-develop/current-todo/006-todo.md` (this file — evidence log)

Checklist:

- AGENTS.md Documentation System section: state the final contract in ≤10
  lines — prose (docs/architecture, ADRs) is hand-written; inventories
  (docs/generated) are generated by `fast-agent-harness docsgen`; freshness is
  enforced by `TestDocsCurrent` and inspected in production by
  `doctor -docs`; the ONLY docs rule for agents is "if you changed a registry,
  run docsgen; if you changed behavior/prose surfaces, update the prose doc" —
  no manifests, no hooks.
- `.agents/rules/documentation.md`: align with the same contract; delete any
  surviving read-order references to deleted metadata files.
- llms.txt: consolidate `## Generated References` (one line per generated
  file with its one-phrase purpose), verify the section count and links are
  coherent top to bottom, and confirm `## Coverage Note` no longer references
  reference-plan.md (005 should have caught it; verify).
- docs/README.md: prune `## Known Documentation Gaps` entries that generators
  closed (config keys, tool catalog, routes, commands, CLI, packages); leave
  real remaining gaps (e.g. lifecycle diagram partial coverage if P2.2 hit
  its escape hatch — copy the recorded blocker here).
- `grep -rn "docsgen" AGENTS.md .agents llms.txt docs/README.md` — every
  mention names the same command and the same test; no invented variants.
- Append the loop's evidence log to this TODO file: per task, commands run and
  outcomes, plus any escape hatches taken (P2.2 especially).

Verification:

```sh
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./internal/docsgen ./internal/architecture
grep -rn "agent-index\|reference-plan\|documentation-system.md" AGENTS.md .agents llms.txt docs/README.md; test $? -ne 0
git diff --check
```

## Backlog For 007 (out of scope here, recorded so it is not lost)

- Audit `internal/agent`'s core turn loop (~8.1k lines: `runtime_loop.go`,
  `compaction.go`, `model_call.go`, `tool_attempt.go`) — carried over from the
  005 completeness critic; still the highest-blast-radius package no TODO has
  examined.
- Decide the fate of the Terminal-Bench eval harness (`internal/bench`,
  ~3.9k lines) — trim or delete; 005 only removed one HTTP route.
- Consolidate the two parallel TUI projection layers
  (`clientux/projector` 1,621 lines vs `tui/transcript` 4,771 lines).
- Retention/prune sweep for the remaining unbounded stores (session JSONL,
  checkpoint snapshots, web cache) — 005 P1.19 covered attachments only.
- Docsgen candidates that did not make this cut: hooks reference generated
  from `hooks.config.toml` schema; skills catalog from `Discover()`; session
  JSONL examples extracted from golden test data; generating the
  `required-imports` half of `architecture_test.go:60`'s hardcoded map.

## Implementation Evidence Log

### P0.1 Scaffold internal/docsgen and docsgen subcommand

- 2026-07-05: Re-verified anchors after 005 moved: `cmd/fast-agent-harness/main.go` dispatch switch is at lines 35-78 and `usage()` is at lines 81-113 before the docsgen edit.
- 2026-07-05: Added `internal/docsgen` target contract/render helpers, `cmd/fast-agent-harness docsgen`, `docs/generated/README.md`, and the initial `internal/docsgen` architecture row.
- 2026-07-05: Verification green:
  - `go test -count=1 ./internal/config ./internal/docsgen ./internal/architecture` -> ok.
  - `git diff --check` -> ok.

### P0.2 Export config key table and generate config reference

- 2026-07-05: Re-verified anchors after 005 moved: `configSpecs()` is at `internal/config/resolved.go:273`, helper constructors are at `resolved.go:405-456`, and `builtInConfig()` is at `internal/config/defaults.go:217`.
- 2026-07-05: Extended the runtime-owned config spec table with type and description fields; exported `ConfigKeySpecs()` as a projection while keeping `configSpec` and setter funcs private.
- 2026-07-05: Generated `docs/generated/config.md` from the same config table; machine-local Billy home paths are rendered as `$BILLYHARNESS_HOME/...`.
- 2026-07-05: Verification green:
  - `go run ./cmd/fast-agent-harness docsgen -only config` -> wrote `docs/generated/config.md`.
  - `go test -count=1 ./internal/config ./internal/docsgen ./internal/architecture` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `docs/generated/config.md`.
  - `git diff --check` -> ok.

### P0.3 Add TestDocsCurrent and generated reference indexes

- 2026-07-05: Re-verified anchors: `internal/architecture/architecture_test.go` enforces docs from ordinary tests; `scripts/verify-local.sh` step list and implementation are both in one file; `llms.txt` had no Generated References section yet.
- 2026-07-05: Added `TestDocsCurrent`, orphan detection for `docs/generated/*.md`, `docsgen -check` in `scripts/verify-local.sh`, and generated-reference index lines in `llms.txt`/`docs/README.md`.
- 2026-07-05: Verification green:
  - `go test -count=1 ./internal/docsgen -run TestDocsCurrent` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `docs/generated/config.md`.
  - `git diff --check` -> ok.

### P1.1 Ship tool catalog generator and unify risk vocabulary

- 2026-07-05: Re-verified anchors after 005 moved: `internal/tools/tools.go:604` exposes `Registry.Specs()`, `internal/tools/policy.go:22` exposes `PolicyDecision`, `cmd/fast-agent-harness/tools_cmd.go:13` builds the live tool dump, `internal/config/mcp.go:356` and `internal/mcpclient/catalog.go:147` held duplicate MCP risk normalization.
- 2026-07-05: Added `protocol.RiskClassSpecs()`, `RiskClasses()`, `ParseRisk()`, and `RiskClass()`; rewired config/mcpclient MCP risk normalization to the protocol parser while preserving conservative MCP aliases (`external` -> `external_mutation`).
- 2026-07-05: Added `docs/generated/tools.md` from the static native tool registry via `tools.NewRegistry(config.BuiltIn())`; no live MCP manager is constructed.
- 2026-07-05: Updated the architecture map for the intentional `config -> protocol` risk-vocabulary dependency and for `docsgen` imports.
- 2026-07-05: Verification green:
  - `go test -count=1 ./internal/protocol ./internal/config ./internal/mcpclient ./internal/tools ./internal/docsgen ./internal/architecture` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only tools` -> wrote `docs/generated/tools.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `docs/generated/config.md`, unchanged `docs/generated/tools.md`.
  - `go run ./cmd/fast-agent-harness tools >/tmp/bh-tools.json` -> ok.
  - `git diff --check` -> ok.

### P1.2 Refactor routes() into []routeSpec and generate gateway API reference

- 2026-07-05: Re-verified anchors after 005 moved: route registration now lives in `internal/gateway/routes.go:3-29`; `gateway.go` constructs the server at `NewServerWithOptionsFromSettings`; auth predicates remain in `internal/gateway/http_security.go:37-97`. The deleted `GET /v1/benchmarks` route is not present and was not resurrected.
- 2026-07-05: Replaced literal `HandleFunc` calls with `routeSpecs()` and a range in `routes()`; added `RouteDocs()` and `AuthClassFor()` implemented through the existing gateway route/mutation predicates.
- 2026-07-05: Added `docs/generated/gateway-api.md` from `gateway.RouteDocs()` and indexed it in `llms.txt`/`docs/README.md`.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/gateway` -> ok.
  - `go test -race -count=1 ./internal/gateway` -> ok.
  - `go test -count=1 ./internal/docsgen ./internal/architecture` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only gateway-api` -> wrote `docs/generated/gateway-api.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `docs/generated/config.md`, `gateway-api.md`, and `tools.md`.
  - `git diff --check` -> ok.

### P1.3 Export doc projections for TUI actions and Telegram commands; generate commands reference

- 2026-07-05: Re-verified anchors after 005 moved: `internal/clientux/actions.go:18` exposes `ActionDefinitions()`, `internal/tui/actions.go:43` owns `actionRegistry()`, `internal/tui/actions.go:779` hydrates shared definitions, `internal/telegrambot/commands.go:63` owns `telegramCommands()`, `internal/telegrambot/commands.go:216` renders `/help`, and `internal/telegrambot/command_policy.go:5` owns the command class enum.
- 2026-07-05: Added `tui.ActionDocs()` and `telegrambot.CommandDocs()` as docs-safe projections of the runtime action/command tables; refactored Telegram `/help` to consume `CommandDocs()` and added a stable string form for command classes.
- 2026-07-05: Added `docs/generated/commands.md` from `clientux.ActionDefinitions()`, `tui.ActionDocs()`, `telegrambot.CommandDocs()`, and built-in `commandregistry.Build()` entries only; prompt commands, profiles, and MCP prompts stay runtime-only.
- 2026-07-05: Updated `llms.txt`, `docs/README.md`, and the `internal/docsgen` architecture row for the new commands target.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/tui ./internal/telegrambot ./internal/clientux ./internal/commandregistry ./internal/docsgen` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only commands` -> unchanged `docs/generated/commands.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `commands.md`, `config.md`, `gateway-api.md`, and `tools.md`.
  - `go test -count=1 ./internal/architecture` -> ok.
  - `git diff --check` -> ok.

### P1.4 Replace the CLI switch with a subcommand table and generate CLI reference

- 2026-07-05: Re-verified anchors after 005 moved: `cmd/fast-agent-harness/main.go:35-79` still held the dispatch switch with `docsgen`, `usage()` was at `main.go:83-115`, and `cmd/fast-agent-harness/main_test.go:26-39` pinned the help path plus the unknown-command error string.
- 2026-07-05: Added `clientux.CLICommandDocs()` as the doc half of the top-level command table, rewrote dispatch to attach run funcs by primary command name, and rewrote `usage()` to render from `CLICommandDocs()`.
- 2026-07-05: Added `docs/generated/cli.md` from `clientux.CLICommandDocs()`; command-specific flags remain owned by each subcommand's `FlagSet` and are intentionally not enumerated by docsgen.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./cmd/fast-agent-harness ./internal/clientux ./internal/docsgen` -> ok.
  - `go run ./cmd/fast-agent-harness help` -> ok; printed generated top-level command list.
  - `go run ./cmd/fast-agent-harness nonsense-command; test $? -ne 0` -> ok; stderr kept `unknown command "nonsense-command"`.
  - `go run ./cmd/fast-agent-harness docsgen -only cli` -> unchanged `docs/generated/cli.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `cli.md`, `commands.md`, `config.md`, `gateway-api.md`, and `tools.md`.
  - `git diff --check` -> ok.

### P1.5 Table-drive event-type vocabulary and generate protocol events reference

- 2026-07-05: Re-verified anchors after 005 moved: `internal/protocol/types.go:202-242` owns the 37 `EventType` constants, `internal/protocol/envelope.go:109-161` held required-ID validation, `envelope.go:201-239` held type membership, and `internal/protocol/envelope_test.go` still pins enrichment/validation behavior.
- 2026-07-05: Added `protocol.EventTypeDocs()` and `protocol.EventSourceDocs()`; `ValidateEventEnvelope`, `isKnownEventType`, and `isKnownEventSource` now consult the protocol-owned tables.
- 2026-07-05: Added `docs/generated/events.md` with envelope fields, event sources, event types, required IDs, payload names, and descriptions; lifecycle semantics remain in runtime prose/eventlog.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/protocol ./internal/eventlog ./internal/gateway` -> ok.
  - `go test -count=1 ./internal/docsgen` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only events` -> unchanged `docs/generated/events.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `cli.md`, `commands.md`, `config.md`, `events.md`, `gateway-api.md`, and `tools.md`.
  - `git diff --check` -> ok.

### P1.6 Generate package map and reverse import index

- 2026-07-05: Re-verified anchors: `docs/architecture.md:12` still starts the hand-written Package Map, `internal/architecture/architecture_test.go:212-296` still parses only table rows between `## Package Map` and the next `##`, and `go list -json ./internal/... ./cmd/...` currently yields 48 internal packages plus `cmd/fast-agent-harness`.
- 2026-07-05: Added package `doc.go` comments for every generated package, added `docs/generated/packages.md` from `go list -json` plus stdlib `go/parser`/`go/doc`, and added a short architecture paragraph explaining intent table vs generated reality.
- 2026-07-05: Updated the command-package architecture allowlist for the intentional P1.4 `cmd/fast-agent-harness -> internal/clientux` metadata dependency.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go vet ./...` -> ok.
  - `go test -count=1 ./internal/docsgen ./internal/architecture` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only packages` -> unchanged `docs/generated/packages.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `cli.md`, `commands.md`, `config.md`, `events.md`, `gateway-api.md`, `packages.md`, and `tools.md`.
  - `go doc ./internal/checkpoint | head -3` -> printed the new package sentence.
  - `git diff --check` -> ok.

### P2.1 Add doctor -docs generated-reference fingerprint checks

- 2026-07-05: Re-verified anchors after P1 targets landed: `internal/docsgen.Target` still lived in `internal/docsgen/docsgen.go`, source hashes still come from `sourceHashFooter()`/`sourceHash()` in `render.go`, `doctorCmd` flags are in `cmd/fast-agent-harness/doctor.go:136-170`, and `collectDoctorReport()` appends checks after repo resolution.
- 2026-07-05: Extended each docsgen target with `Fingerprint()`, added `docsgen.VerifyAgainst(dir)`, and made fingerprint tests compare live target fingerprints with generated footer hashes.
- 2026-07-05: Added `doctor -docs` / `doctorOptions.CheckDocs`; doctor emits one `docs:<target>` check per docsgen target and reports `n/a` when `docs/generated` is absent.
- 2026-07-05: Updated `docs/generated/README.md` to state that `doctor -docs` proves generated registry-data freshness, not prose freshness.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/docsgen ./cmd/fast-agent-harness` -> ok.
  - `go run ./cmd/fast-agent-harness doctor -docs -build=false -services=false -gateway=false` -> exited 0; all `docs:*` checks were `ok` (local auth/MCP checks failed separately with strict mode off).
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged `cli.md`, `commands.md`, `config.md`, `events.md`, `gateway-api.md`, `packages.md`, and `tools.md`.
  - `git diff --check` -> ok.

### P2.2 Extract lifecycle rules where the table is honest

- 2026-07-05: Re-verified anchors before editing: `LifecycleValidator` state maps are at `internal/eventlog/eventlog.go:140-153`; `Observe()` is at `eventlog.go:208` with the event-type switch at `eventlog.go:220-446`; data-dependent helpers are at `eventlog.go:547-675`; lifecycle ordering tests are at `internal/eventlog/eventlog_test.go:266-523`.
- 2026-07-05: Paper inventory before code:
  - `run`: structural and tableable. `run.started` creates the run; `run.completed` and `run.failed` terminate it after requiring an existing run and rejecting duplicate terminal events.
  - `turn`: partly structural, but `turn.change_recorded` / `turn.change_reverted` tolerate a missing turn id after enrichment and only validate the turn if one is present, so a single simple start/progress/terminate shape would need a special optional-child rule.
  - `step`: partly structural, but `step.started` has conditional `parent_step_id` validation and map-batch behavior is pinned by tests; model/assistant/provider usage events are known-step progress checks rather than step transitions.
  - `call`: `tool.call_requested` is a clean start, but permission/audit/progress share the call while `tool.call_progress` also drives attempt pre-registration from payload phase.
  - `attempt`: structurally coupled to call ownership and the `attemptCalls` map; `tool.call_progress` can seed an attempt on `phase=attempt_started`, and post-terminal progress is allowed only for specific payload phases.
  - `output_ref`, `user_input`, and `hook`: existence-only or opportunistic checks, all driven by payload ids or optional call/attempt ids rather than one lifecycle state shape.
- 2026-07-05: Escape hatch used. More than 30% of the structural cases would require hidden special fields in the proposed one-shape rule table; forcing them would recreate a second procedural validator in metadata. This loop tables only the clean `run` entity and makes `Observe()` consume that same table. The generated event reference will render the partial runtime table and state clearly that the rest of `Observe()` remains procedural and test-pinned.
- 2026-07-05: Added `internal/eventlog/lifecycle_rules.go`; `Observe()` now routes `run.started`, `run.completed`, and `run.failed` through that same table, while the turn/step/tool/user-input/hook switch branches remain procedural.
- 2026-07-05: Extended `docs/generated/events.md` with a partial lifecycle rules table and Mermaid diagram from `eventlog.LifecycleRules()`; regenerated `docs/generated/packages.md` because `docsgen` now imports `eventlog`.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/eventlog` -> ok.
  - `go test -race -count=1 ./internal/eventlog` -> ok.
  - `go test -count=1 ./internal/gateway ./internal/trace` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only events` -> unchanged `docs/generated/events.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged all generated references.
  - `git diff --check` -> ok.

### P2.3 Split doctor checks into descriptor + executor and generate the doctor reference table

- 2026-07-05: Re-verified anchors after P2.1/P2.2: `doctorCmd` flags are at `cmd/fast-agent-harness/doctor.go:138-172`; `collectDoctorReport()` still resolved repo/mode/runtime before appending checks at `doctor.go:203-246`; check helpers start at `doctor.go:324`; service checks at `doctor.go:723`; gateway probes at `doctor.go:1015`; CLI docs projection is in `internal/clientux/cli_docs.go`.
- 2026-07-05: Added `cmd/fast-agent-harness/doctor_checks.go` with `doctorCheckSpec`, `doctorContext`, a descriptor builder backed by `clientux.DoctorCheckDocs()`, and run funcs attached by check name. `collectDoctorReport()` now ranges the descriptor list after runtime collection; `git root` remains the preflight repo-resolution check.
- 2026-07-05: Extended `clientux.DoctorCheckDocs()` to expand docsgen targets and managed services from the code-owned tables; `docs/generated/cli.md` now includes a "What Doctor Checks" table with `docs:*`, service, process, PID, unit, journal, and gateway checks.
- 2026-07-05: Regenerated `docs/generated/cli.md` and `docs/generated/packages.md` (packages changed because docsgen now imports `serviceops`).
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./cmd/fast-agent-harness ./internal/clientux ./internal/docsgen` -> ok.
  - `go run ./cmd/fast-agent-harness doctor -build=false -services=false -gateway=false` -> exited 0; descriptor-ordered checks printed, with expected local auth/MCP/git warnings/fails because strict mode was off.
  - `go run ./cmd/fast-agent-harness doctor -json -build=false -services=false -gateway=false | head -20` -> exited 0 and printed valid JSON header fields.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged all generated references.
  - `git diff --check` -> ok.

### P2.4 Unify settings.json behind config.BillySettings

- 2026-07-05: Re-verified anchors: `resolveState.applyBillySettings()` was at `internal/config/resolved.go:465-501`; `Config.ApplyBillySettingsDefaults()` was at `internal/config/defaults.go:184-219`; the TUI private `appSettings` shape plus load/save lived at `internal/tui/settings.go:17-108`; existing settings edge-case tests were in `internal/config/config_test.go:221-267` and `:617-677`.
- 2026-07-05: Added `config.BillySettings`, `LoadBillySettings`, `LoadBillySettingsWithDefaults`, `SaveBillySettings`, and reflected `BillySettingsFieldSpecs()`. Saving is temp+rename with mode `0600`; unknown fields are intentionally not preserved because `settings.json` is Billyharness-owned.
- 2026-07-05: Rewired config resolution/defaults and TUI load/save to the canonical struct. TUI keeps display defaults and normalization locally, while config reads only the saved model/profile/reasoning/context fields it already consumed.
- 2026-07-05: Extended `docs/generated/config.md` with a reflected `settings.json` table listing every `BillySettings` JSON key, Go field, type, optional marker, and writer tag.
- 2026-07-05: Verification green:
  - `go build ./...` -> ok.
  - `go test -count=1 ./internal/config ./internal/tui` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -only config` -> unchanged `docs/generated/config.md`.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged all generated references.
  - `git diff --check` -> ok.

### P2.5 Rewrite the documentation contract around the real generated layer

- 2026-07-05: Re-verified target docs after all generators landed: `AGENTS.md` Documentation System starts at line 107, `.agents/rules/documentation.md` still owned the detailed docs rule, `llms.txt` had a Generated References section at line 26, `docs/README.md` had generated refs plus stale Known Documentation Gaps, and `docs/generated/README.md` only mentioned basic regeneration plus `doctor -docs`.
- 2026-07-05: Updated the root and detailed docs rules: hand-written prose owns architecture/rationale, `docs/generated` owns code-generated inventories, generated files are never hand-edited, `TestDocsCurrent` is the committed freshness guard, and `doctor -docs` is the live-binary fingerprint check.
- 2026-07-05: Consolidated `llms.txt`, `docs/README.md`, and `docs/generated/README.md` so generated references mention the final CLI doctor table, settings.json table, partial lifecycle table, and remaining gaps honestly. P2.2 escape hatch is copied into the Known Documentation Gaps as partial lifecycle coverage.
- 2026-07-05: Verification green:
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged all generated references.
  - `go test -count=1 ./internal/docsgen ./internal/architecture` -> ok.
  - `grep -rn "agent-index\|reference-plan\|documentation-system.md" AGENTS.md .agents llms.txt docs/README.md; test $? -ne 0` -> ok, no matches.
  - `grep -rn "docsgen" AGENTS.md .agents llms.txt docs/README.md` -> only the canonical `go run ./cmd/fast-agent-harness docsgen`, `docsgen -check`, `-only <target>`, and docsgen test references.
  - `git diff --check` -> ok.

### Global verification and strict hygiene cleanup

- 2026-07-05: First `./scripts/verify-local.sh` run passed diff, dependency metadata, vet, docsgen freshness, full tests, focused race tests, `govulncheck`, binary rebuild, and bench smoke, but failed strict hygiene because `internal/tui/transcript_runtime.go` had grown to 1541 LOC over the 1500 source limit.
- 2026-07-05: Split the bottom rendering/string helper block into `internal/tui/transcript_runtime_render_helpers.go` without behavior changes. Post-split counts: `transcript_runtime.go` 1307 LOC, helper file 244 LOC; `go run ./cmd/fast-agent-harness hygiene -strict` -> ok.
- 2026-07-05: Final global verification green:
  - `go build ./...` -> ok.
  - `go vet ./...` -> ok.
  - `go test -count=1 ./...` -> ok.
  - `go test -count=1 ./internal/architecture` -> ok.
  - `go run ./cmd/fast-agent-harness docsgen -check` -> unchanged all generated references.
  - `git diff --check` -> ok.
  - `go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness` -> ok.
  - `./scripts/verify-local.sh` -> verification passed; full-race step skipped because `--full-race` was not requested.

## Global Verification For The Implementation Loop

Run after every milestone, and again before declaring the loop done:

```sh
go build ./...
go vet ./...
go test -count=1 ./...
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
./scripts/verify-local.sh
```

Notes:

- Rebuild the binary whenever CLI/gateway/TUI/Telegram/provider/tool/agent
  code changes:
  `go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.
- Every task that adds a target or touches a consumed registry must leave
  `docsgen -check` green and the regenerated file committed in the SAME
  change — a stale generated file in a merge is this system failing at its
  one job.
- `docs/architecture.md`'s Package Map row for `docsgen` grows allowed imports
  strictly task-by-task (P0.1 seeds it; P1.2 adds gateway/gatewayapi; P1.3
  adds tui/telegrambot/clientux/commandregistry). Never pre-add.
- The behavior pins are non-negotiable: existing tests in `internal/config`,
  `internal/gateway`, `internal/eventlog`, `internal/tui`,
  `internal/telegrambot`, and `cmd/fast-agent-harness` must pass UNCHANGED by
  the table refactors (P1.2, P1.4, P1.5, P2.2, P2.3, P2.4). A table refactor
  that needs a test edit is changing semantics — stop and re-derive.
- Docs rule for every task: generated files regenerate; prose files update
  only when behavior they describe changed; state which docs were checked
  when none changed.
