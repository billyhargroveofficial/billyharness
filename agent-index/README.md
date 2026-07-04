# Agent Index

`agent-index/` is machine-oriented navigation for Codex and other agents. It is
not architecture canon and should not become prose documentation.

Use it to answer:

- which docs exist;
- which docs are canonical, generated, legacy, or source material;
- when a doc should be read;
- which code areas a doc claims to cover;
- how generated maps were produced.

## Files

- [docs-manifest.json](docs-manifest.json): hand-maintained metadata for the
  initial documentation system.
- [generated/reference-plan.md](generated/reference-plan.md): handwritten
  design for future generated references, docsgen metadata, and docguard
  checks.
- [generated/repo-map.md](generated/repo-map.md): compact generated-style repo
  orientation map. This is a seed file until a real generator exists.

## Policy

Generated files must include a generated marker and source command. Handwritten
metadata must include a `last_reviewed` date and a trust level.

Generated-reference strategy lives in `generated/reference-plan.md`; generated
output should carry source globs, source hash, source commit, and
`dirty_at_generation` metadata.

Do not put active TODO state here. Link to `loop-develop/README.md` and let the
agent inspect `loop-develop/current-todo/` when doing loop work.
