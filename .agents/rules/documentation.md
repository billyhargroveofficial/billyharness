# Documentation Rule

Use this rule when a task touches documentation or when code changes may make
documentation stale.

## Read Order

1. Read `AGENTS.md`.
2. Read `llms.txt` if you need a repo/documentation map.
3. Read `docs/README.md` for architecture docs routing.
4. Read the specific canonical doc, ADR, active TODO, or operator runbook that
   owns the area you are changing.

Do not load every doc by default. Pick the smallest set that matches the task.

## Where Content Belongs

- `AGENTS.md`: short durable agent contract and routing.
- `.agents/rules/`: detailed reusable agent behavior rules.
- `llms.txt`: compact agent-readable navigation.
- `docs/`: architecture canon, contracts, ADRs, and clean-room research.
- `loop-develop/current-todo/`: active implementation plans and evidence.
- `loop-develop/history/`: completed loop records.
- `ops/`: dated production runbooks and verified operator procedures.

## Update Triggers

Update docs in the same task when code changes alter:

- CLI commands, flags, output, or validation errors;
- gateway APIs, event protocol, or session replay semantics;
- auth, security, permissions, or owner/scope behavior;
- config keys, env vars, profiles, provider/model behavior;
- TUI or Telegram user-facing workflows;
- tools, MCP schemas, discovery, trust, or output behavior;
- deployment/service/operator procedures;
- package ownership, imports, boundaries, or architecture invariants;
- examples users or agents copy.

If no docs update is needed, final output should state what docs were checked
and why they stayed unchanged.

## Do Not

- Do not put active TODOs, goal prompts, implementation checklists, runbooks,
  temporary investigation logs, feature notes, or completion evidence in
  `docs/`.
- Do not document unimplemented or unverified behavior as current truth.
- Do not duplicate large generated tables in handwritten docs.
- Do not copy competitor source or proprietary text into Billyharness docs.
- Do not move `docs/architecture.md` unless the architecture guard is updated.

## Verification

For documentation-only changes:

```sh
git diff --check
go test -count=1 ./internal/architecture
```

For code changes that also update docs, run the code verification required by
`AGENTS.md` plus the focused docs checks above.

For generated docs, regenerate to a temp directory and compare output instead of
dirtying the worktree in-place unless the task explicitly updates generated
files.
