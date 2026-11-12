# qwen-sub-proxy

**Use your [chat.qwen.ai](https://chat.qwen.ai) Plus/Pro subscription from any OpenAI-compatible client** — ZCode, Cline, Cursor, Continue, OpenCode, Roo, Aider, or your own script.

Russian version: [README.ru.md](README.ru.md)

---

## Why this exists

A chat.qwen.ai subscription (Qwen Plus / Pro) has **no API key**. The account is authorized through OAuth, and model inference is served by an OpenAI-compatible gateway at `https://api.qwen.ai/compatible-mode/v1` that expects the OAuth access token as a `Bearer` token.

Official API keys for third-party clients are only issued by other products — Alibaba Cloud ModelStudio **Coding Plan** and QwenCloud **Token Plan** (`sk-sp-…` keys). If you already pay for chat.qwen.ai, there is no supported way to point another tool at it.

`qwen-sub-proxy` closes that gap. It performs the OAuth device flow, keeps the access token alive on its own, and exposes a local OpenAI-compatible endpoint:

```
ZCode ──HTTP/OpenAI──▶ 127.0.0.1:8791 ──HTTPS/OpenAI + Bearer──▶ api.qwen.ai
                            │
                            └── OAuth device flow ──▶ auth.qwen.ai
```

This is **not** web scraping. There are no cookies, no HTML parsing, no DOM automation and no browser in the request path. The browser is used exactly once, to approve the OAuth login. Every model call goes to the official inference gateway over HTTPS.

---

## Requirements

- macOS or Linux, Go 1.26+ (to build), a chat.qwen.ai account with an active Plus or Pro subscription
- An OpenAI-compatible client

## Install

```bash
git clone https://github.com/Tozix/qwen-sub-proxy.git
cd qwen-sub-proxy
make build          # ./qwen-sub-proxy
make test           # unit tests
make vet            # go vet
```

The project uses **only the Go standard library**. There is no `go.sum` and no third-party dependency.

### 1. Authorize

```bash
./qwen-sub-proxy login
```

The command opens `https://chat.qwen.ai/authorize?user_code=…` in your browser. Approve the request; the terminal prints the plan it found:

```
authorized: plan=plus email=you@example.com
credentials stored in /Users/you/.config/qwen-sub-proxy/credentials.json
```

The device code is valid for 10 minutes. If the browser shows the regular chat instead of a confirmation page, use the URL printed by the command — it carries the user code.

### 2. Run the proxy

```bash
./qwen-sub-proxy serve
```

```
qwen-sub-proxy 0.1.0 listening on http://127.0.0.1:8791
local api key file: /Users/you/.config/qwen-sub-proxy/client-key
use it in your client as base URL http://127.0.0.1:8791/v1
```

On macOS you can let launchd keep it running:

```bash
make install        # LaunchAgent com.qwen-sub-proxy
make healthcheck    # probe the running instance
make smoke          # health + models + a real completion
make uninstall      # remove the LaunchAgent
```

The service listens on loopback only, requires a per-installation bearer key, and restarts automatically if it dies.

### 3. Point your client at it

| Setting | Value |
|---|---|
| Base URL | `http://127.0.0.1:8791/v1` |
| API key | contents of `~/.config/qwen-sub-proxy/client-key` (`./qwen-sub-proxy status` prints it) |
| Protocol | OpenAI Chat Completions |

```bash
curl http://127.0.0.1:8791/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.config/qwen-sub-proxy/client-key)" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.8-max","messages":[{"role":"user","content":"hello"}]}'
```

<details>
<summary>ZCode</summary>

Add a custom provider in **Settings → Model Provider** (or edit `~/.zcode/v2/provider_config.json`):

- Protocol: `openai-chat-completions`
- Base URL: `http://127.0.0.1:8791/v1`
- API key: the local key
- Models: `qwen3.8-max`, `qwen3.8-flash`, `qwen4.0-max-preview`
- Context window: `1000000`
- Max output tokens: `131072`
- Vision: enabled

A helper that patches the ZCode config with a backup is included:

```bash
./scripts/zcode-provider.py --apply
```
</details>

<details>
<summary>Cline / Cursor / Roo / Continue / OpenCode</summary>

All of these have an "OpenAI Compatible" provider mode. Use the base URL and key above, then add the model names from the table below. Depending on the client you may have to switch the provider to an OpenAI-compatible one instead of "OpenAI" proper.
</details>

<details>
<summary>Claude Code</summary>

Claude Code speaks the Anthropic protocol; this proxy speaks the OpenAI one. Use a bridge in front of it, or skip this proxy and use the official Alibaba Coding Plan endpoint instead, which exposes an Anthropic-compatible route (`https://coding.dashscope.aliyuncs.com/apps/anthropic`).
</details>

---

## Models and limits

The live catalog comes from `GET /v1/models` and is cached for 10 minutes. As of the last check:

| Model id | Context window | Input | Max output (`max_tokens`) |
|---|---|---|---|
| `qwen3.8-max` | 1,000,000 | text, image, video | 1 – 131,072 |
| `qwen3.8-flash` | 1,000,000 | text, image, video | 1 – 131,072 |
| `qwen4.0-max-preview` | 1,000,000 | text, image, video | not enforced by the gateway |

## Input formats

All requests are forwarded to the gateway byte-for-byte, so anything the gateway understands works here.

| Input | Content part | Notes |
|---|---|---|
| Text | `{"type":"text","text":"…"}` | |
| Image | `{"type":"image_url","image_url":{"url":"data:image/png;base64,…"}}` | Also accepts `https://` URLs. The gateway requires width and height greater than 10 px. |
| Video | `{"type":"video_url","video_url":{"url":"https://…"}}` | The gateway downloads the URL. |
| Audio | `{"type":"input_audio","input_audio":{"data":"…","format":"wav"}}` | |

## Request options

| Option | Behaviour |
|---|---|
| `stream: true` | Server-sent events, forwarded unbuffered — the first token reaches the client immediately |
| `max_tokens` | `1` – `131072` for the 3.8 models; the gateway rejects anything else with `invalid_parameter_error` |
| `enable_thinking: false` | Disables the reasoning pass; `reasoning_content` is then absent |
| `tools` / `tool_choice` | Function calling works and returns standard OpenAI `tool_calls` |

### Reasoning output

Qwen returns its reasoning in a **non-standard field**: `message.reasoning_content` for a full answer and `delta.reasoning_content` for a streamed chunk. It is not the OpenAI `reasoning` field, so many clients ignore it and show only the final text. That is expected, not a bug.

## Commands

| Command | What it does |
|---|---|
| `login` | Runs the OAuth device flow, stores the credentials, prints the plan |
| `serve` | Runs the local proxy (`--host`, `--port`) |
| `status` | Shows the plan, the remaining token lifetime and the local key |
| `models` | Prints the gateway model catalog |
| `logout` | Revokes the refresh token server-side and deletes the local credentials |
| `healthcheck` | Probes a running instance |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `QWEN_SUB_PROXY_HOME` | `~/.config/qwen-sub-proxy` | Configuration directory |
| `QWEN_SUB_PROXY_KEY` | generated | Fixed local API key instead of the generated one |
| `QWEN_SUB_PROXY_LABEL` | `com.qwen-sub-proxy` | launchd label used by `scripts/install.sh` |

| File | Contents |
|---|---|
| `~/.config/qwen-sub-proxy/credentials.json` | OAuth token pair, mode `0600` |
| `~/.config/qwen-sub-proxy/client-key` | Local API key for the proxy, mode `0600` |

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions, streaming and non-streaming |
| `GET` | `/v1/models` | Model catalog (cached) |
| `GET` | `/health` | Service state: plan, token lifetime, cached models — never a token |

## How token refresh works

- The access token is short-lived. The proxy refreshes it 60 seconds before it expires, and once more if the gateway answers `401`.
- A refresh rotates the token pair. The new refresh token is written to disk **before** the in-memory copy is replaced.
- Concurrent requests share a single refresh: a burst of parallel `401`s causes exactly one token exchange.
- A failed refresh is reported to every waiting request, so a burst never turns into a storm of token calls.
- If the refresh token itself is rejected (`invalid_grant`), the proxy answers `401 reauthentication_required`, keeps the credentials on disk and reports `logged_in:false` in `/health`. Run `qwen-sub-proxy login` again.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `405` from `auth.qwen.ai` during login | The upstream WAF rejects the default `curl` User-Agent. The proxy always sends its own; if you scripted the flow yourself, set a non-curl `User-Agent`. |
| The browser opens the normal chat instead of a confirmation page | Use the exact URL printed by `login`; it carries the `user_code`. |
| `401 invalid_local_key` | The client sends the wrong key. Copy it from `~/.config/qwen-sub-proxy/client-key`. |
| `401 reauthentication_required` | The refresh token expired. Run `./qwen-sub-proxy login`. |
| `402 insufficient_quota` | The weekly credit of the subscription is used up. `error.quota.reset_at` carries the reset timestamp. The proxy forwards the error unchanged and never retries it. |
| `invalid_parameter_error: Range of max_tokens should be [1, 131072]` | Lower `max_tokens`. |
| Nothing arrives while streaming | Check `~/Library/Logs/qwen-sub-proxy.log` for the request status. |

## Legal and terms of service

This project is **not affiliated with Alibaba, Qwen or any of their subsidiaries**. It is an independent client of publicly reachable endpoints of a service you already pay for.

- The Qwen terms of service describe the service as for **private and non-commercial use only**, and prohibit automated or programmatic bulk extraction, circumventing rate limits, sharing account credentials and circumventing protections. This tool is meant for personal, interactive use in a single client: no account pooling, no credential sharing, no scraping, no limit circumvention.
- The proxy identifies itself honestly: `User-Agent: qwen-sub-proxy/<version> (darwin; arm64)`. It does **not** impersonate the official Qwen Code client.
- Using a subscription in a client that is not officially supported is **not an endorsed scenario**. Qwen may change its terms or block unknown clients at any time, and that risk stays with the user.
- If you want the same models with an officially supported integration, use **Alibaba Cloud ModelStudio Coding Plan** or **QwenCloud Token Plan**: they issue an `sk-sp-…` API key and their documentation explicitly lists third-party clients (Claude Code, Cline, Cursor, Codex, OpenCode, Cherry Studio, Qoder, Kilo CLI). No proxy required.

You are responsible for how you use your own subscription.

## Development

```
cmd/qwen-sub-proxy   CLI entry point: serve | login | status | models | logout | healthcheck
internal/config      Paths, User-Agent, defaults
internal/creds       Credential storage (0600/0700, atomic writes) and the local API key
internal/oauth       Device flow, token exchange, refresh, userinfo, revoke
internal/tokenmgr    Access token lifecycle, single-flight refresh
internal/gateway     HTTP client for api.qwen.ai
internal/proxy       HTTP server: /v1/chat/completions, /v1/models, /health
docs/protocol.md     The OAuth and gateway contract, as observed
scripts/             install.sh, uninstall.sh, smoke.sh, zcode-provider.py
```

```bash
make build && make test && make vet
```

See [PLAN.md](PLAN.md) for the design and the acceptance criteria, and [AGENTS.md](AGENTS.md) for the rules this codebase follows.

## License

[MIT](LICENSE)
