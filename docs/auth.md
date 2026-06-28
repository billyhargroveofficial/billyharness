# Auth

Billyharness supports two provider auth paths:

- DeepSeek API key for `deepseek-v4-flash` and `deepseek-v4-pro`.
- Codex/OpenAI OAuth import for ChatGPT subscription-backed models such as `gpt-5.5`.

Auth status is intentionally redacted everywhere: TUI, Telegram, gateway API, `config inspect`, and `doctor` show metadata and file paths, not raw keys or tokens.

## DeepSeek Key

Interactive setup:

```text
/auth deepseek
```

Telegram setup:

```text
/auth deepseek sk-...
```

Gateway API setup:

```sh
curl -X POST http://127.0.0.1:8765/v1/auth/deepseek \
  -H 'Content-Type: application/json' \
  -d '{"api_key":"sk-..."}'
```

The key is stored under `$BILLYHARNESS_HOME`, normally in `.env` or the configured credential file. Environment variables still win when explicitly set.

Useful environment fallback:

```sh
DEEPSEEK_API_KEY=sk-...
```

## Codex OAuth

First log in with the official Codex CLI on the same server:

```sh
codex login
```

Then import the local Codex auth into billyharness:

```text
/auth codex
```

or through the gateway:

```sh
curl -X POST http://127.0.0.1:8765/v1/auth/codex/import \
  -H 'Content-Type: application/json' \
  -d '{}'
```

Billyharness copies the usable OAuth metadata into:

```text
$BILLYHARNESS_HOME/auth/codex.json
```

The refresh path is serialized so concurrent runs do not race token refresh.

## Inspect

TUI and Telegram:

```text
/auth
/config
```

CLI:

```sh
./bin/fast-agent-harness config inspect
./bin/fast-agent-harness config inspect -json
./bin/fast-agent-harness doctor
```

Gateway:

```sh
curl -fsS http://127.0.0.1:8765/v1/auth/status
curl -fsS http://127.0.0.1:8765/v1/config
```

`config inspect` explains where provider/model/profile/reasoning/auth-related settings came from: built-in defaults, `$BILLYHARNESS_HOME/config.toml`, project config, `.env`, environment variables, CLI flags, or runtime overrides.

## Redaction

Never expect raw secrets in status output. Redacted fields show only metadata such as:

- configured or missing;
- source type;
- path;
- account id;
- expiry;
- refresh state.

If a secret appears in logs, JSONL, Telegram, TUI, or trace bundles, treat it as a bug.
