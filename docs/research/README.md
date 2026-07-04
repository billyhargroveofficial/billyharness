# Research Routing

`docs/research/` is the routing surface for historical and clean-room research
material. It is not architecture canon. Current architecture truth lives in
`docs/architecture.md`, `docs/architecture/`, `docs/adr/`, and
`docs/documentation-system.md`.

Existing legacy research files still live at the top of `docs/` until a
dedicated move/delete pass can update every link and metadata entry safely.

## Historical Sources

- [Codex research roadmap](../codex-research-roadmap.md): legacy historical
  source material. It still contains `Active Backlog` P0/P1/P2 language, so do
  not treat it as the current implementation backlog.
- [Competitive architecture analysis](../competitive-architecture-analysis.md):
  clean-room competitor source material and design pressure. Treat it as
  historical input, not current Billyharness truth.

## Cleanup Policy

- Extract stable current rules into canonical architecture docs or ADRs before
  deleting, moving, or de-emphasizing a research file.
- Extract actionable implementation work into `loop-develop/current-todo`, not
  into research docs.
- Do not copy long research sections into indexes. Keep routers short and link
  to the source material.
- Move legacy files only when every repository link and
  `agent-index/docs-manifest.json` entry is updated in the same change.
- Keep competitor material clean-room: architecture, contracts, tests, UX, and
  capability gaps only; never copy competitor source.
