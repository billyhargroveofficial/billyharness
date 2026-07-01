# Custom Slash Commands

TUI slash prompt commands can be defined as local Markdown files:

- `$BILLYHARNESS_HOME/commands/*.md`
- `<workspace>/.billyharness/commands/*.md`

The filename becomes the command name. For example, `review.md` creates
`/review`. Built-in commands and aliases cannot be shadowed.

```markdown
---
description: Review a path or diff
argument_hint: [path]
---
Review $ARGUMENTS for correctness, regressions, and missing tests.
```

Placeholders are deterministic text replacement only:

- `$ARGUMENTS` expands to everything after the slash command.
- `$1` through `$9` expand to whitespace-separated arguments.

Expanded prompts are capped before they enter the normal prompt send path.
