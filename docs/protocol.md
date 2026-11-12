# Протокол подписки chat.qwen.ai

Документ фиксирует сетевой контракт, который использует прокси. Источники: официальная реализация Qwen Code (`packages/core/src/qwen/subscription-oauth-client.ts`, `oauth-config.ts`) и живые ответы сервисов.

## OAuth (auth.qwen.ai)

| Назначение | Запрос |
|---|---|
| Device authorization | `POST /api/oauth/device/code`, form `client_id=qwen-code-cli`, `scope=model:invoke profile:read` |
| Token exchange | `POST /api/oauth/token`, form `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `client_id`, `device_code` |
| Refresh | `POST /api/oauth/token`, form `grant_type=refresh_token`, `client_id`, `refresh_token` |
| Identity / membership | `GET /api/oauth/userinfo` с `Authorization: Bearer <access_token>` |
| Revocation | `POST /api/oauth/revoke`, form `client_id`, `token`, `token_type_hint=refresh_token` |

Ответ device authorization: `device_code`, `user_code`, `verification_uri`, `verification_uri_complete`, `expires_in` (600 c), `interval` (5 c).

Ответ обмена: `access_token`, `refresh_token`, `token_type` (`Bearer`), `expires_in`, `scope`.

Ошибки опроса: HTTP 400 `authorization_pending` (пользователь ещё не подтвердил) и HTTP 400 `slow_down` (интервал +5 c). `invalid_grant` означает, что refresh_token больше не действует — нужна повторная авторизация.

Ответ userinfo: `sub`, `name`, `email`, `member_type` (`lite` / `plus` / `pro`).

**User-Agent обязателен.** С дефолтным `curl/8.7.1` WAF Alibaba отвечает `405 Not Allowed` (HTML-блок). Любой другой UA работает, поэтому прокси отправляет честный `qwen-sub-proxy/<version> (darwin; arm64)` и не подделывается под официальный клиент.

## Model gateway (api.qwen.ai)

OpenAI-совместимый базовый URL: `https://api.qwen.ai/compatible-mode/v1`.

- `GET /models` — каталог. Отдаётся и без авторизации. Текущее содержимое: `qwen4.0-max-preview`, `qwen3.8-max`, `qwen3.8-flash`; у всех `context_window_tokens = 1000000`, `input_modalities = [text, image, video]`.
- `POST /chat/completions` — OpenAI Chat Completions, `Authorization: Bearer <access_token>`. Поддерживается SSE при `"stream": true`.

Коды ответов, важные для прокси:

| Код | Значение | Действие прокси |
|---|---|---|
| 401 `PROXY_TOKEN_MISSING` | access_token недействителен | refresh + **один** повтор запроса |
| 402 `insufficient_quota` | недельный кредит исчерпан | пробрасывается клиенту как есть, повторов нет |
| 429, 5xx | перегрузка/сбой | пробрасываются как есть, повторов нет |

Официальный клиент при 401 делает ровно один refresh и один повтор — прокси повторяет эту логику.

## Квоты

У Plus/Pro кредит обнуляется по календарной неделе. Ответ 402 содержит `error.code = insufficient_quota` и необязательные `error.quota.reset_at` (unix-время в секундах) и `error.quota.tier`. Прокси не интерпретирует квоты — только передаёт их клиенту без изменений.
