# billyharness

Fast Go agent harness with a gateway API, TUI chat, native tools, MCP server, and benchmark runner.

## Quick start

```bash
cp .env.example .env # if you add an example later
go test ./...
go build -buildvcs=false -o fast-agent-harness ./cmd/fast-agent-harness
./fast-agent-harness serve -addr 127.0.0.1:8765
./fast-agent-harness tui -gateway http://127.0.0.1:8765 -model deepseek-v4-flash
```

For SSH terminals with broken alt-screen or key handling:

```bash
stty -ixon
./fast-agent-harness tui -plain -gateway http://127.0.0.1:8765 -model deepseek-v4-flash
```
