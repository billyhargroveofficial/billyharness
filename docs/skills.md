# Skills

Billyharness skills are loaded on demand through tools. Their contents are not injected into every model prompt.

Skill directories:

```sh
$BILLYHARNESS_HOME/skills/<name>/SKILL.md
<workspace>/.billyharness/skills/<name>/SKILL.md
```

Compatibility input is available only when explicitly requested:

```sh
<workspace>/.claude/skills/<name>/SKILL.md
```

## Tools

List skills:

```json
{"query":"review","limit":20}
```

Read one skill:

```json
{"name":"review","source":"project","max_chars":12000}
```

Use `include_compat=true` to include `.claude/skills` in `skill_list` or `skill_read`.

`skill_read` is bounded by `max_chars` and returns metadata including source, path, content size, returned chars, and truncation status.
