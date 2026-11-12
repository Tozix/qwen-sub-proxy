# План: qwen-sub-proxy

## Контекст

Подписка chat.qwen.ai (Qwen Plus/Pro) не имеет API-ключа: доступ выдаётся через OAuth. Inference отдаётся на OpenAI-совместимом шлюзе `https://api.qwen.ai/compatible-mode/v1` с Bearer-токеном. Чтобы использовать подписку в ZCode (и других клиентах с API-ключом), нужен локальный прокси: он сам проходит device-flow, обновляет токен и проксирует запросы.

Официальная альтернатива без серой зоны — Alibaba Cloud ModelStudio Coding Plan / QwenCloud Token Plan (`sk-sp-…`), где ключ выдаётся для сторонних клиентов. Основным путём здесь не делается.

## Приёмочные критерии

1. `make build` собирает бинарник; `make test` и `make vet` проходят; внешних зависимостей нет (`go.sum` отсутствует).
2. `serve` слушает только `127.0.0.1:8791`; запрос с не-loopback `RemoteAddr` отклоняется 403.
3. Запрос без корректного локального ключа → 401 `invalid_local_key`, обращений к шлюзу нет.
4. Ответ шлюза пробрасывается байт-в-байт вместе со статусом (включая не-200).
5. SSE: `Content-Type: text/event-stream`, первый чанк приходит до завершения генерации, поток завершается `data: [DONE]`.
6. Upstream 401 → ровно один refresh и ровно один повтор запроса; клиент получает 200.
7. 20 параллельных запросов при истёкшем токене → ровно один вызов token endpoint.
8. `invalid_grant` → 401 `reauthentication_required`, креды не удаляются, `/health` показывает `logged_in:false`.
9. 402 `insufficient_quota` пробрасывается как есть, повторов нет.
10. `/v1/models` отдаёт каталог из кэша (TTL 10 минут); при недоступности шлюза — stale-кэш.
11. `/health` не содержит ни фрагмента токенов, ни локального ключа.
12. Креды: директория 0700, файлы 0600, атомарная запись, вне git-репозитория.
13. `login` печатает URL и код, открывает браузер, соблюдает `interval` и `slow_down`, сохраняет креды, печатает план (`plus`/`pro`).
14. `logout` отзывает refresh_token и удаляет креды.
15. Исходящий User-Agent — `qwen-sub-proxy/<version> (darwin; arm64)`; строки `QwenCode` в бинарнике нет.

## Карта реализации

| Файл | Ответственность |
|---|---|
| `cmd/qwen-sub-proxy/main.go` | подкоманды `serve`, `login`, `status`, `models`, `logout`, `healthcheck` |
| `internal/config/config.go` | пути, User-Agent, значения по умолчанию |
| `internal/creds/store.go` | хранение кредов: атомарная запись, 0600/0700 |
| `internal/creds/localkey.go` | генерация и проверка локального ключа (constant-time) |
| `internal/oauth/*.go` | device flow, обмен, refresh, userinfo, revoke, разбор ошибок |
| `internal/tokenmgr/tokenmgr.go` | выдача access_token, single-flight refresh, `ErrReauthentication` |
| `internal/gateway/gateway.go` | HTTP-клиент шлюза, каталог моделей |
| `internal/proxy/server.go` | mux, loopback-проверка, аутентификация, recover |
| `internal/proxy/chat.go` | `/v1/chat/completions`, refresh+ретрай, проброс ошибок |
| `internal/proxy/stream.go` | SSE без буферизации |
| `internal/proxy/models.go` | кэш каталога моделей |
| `internal/proxy/health.go` | `/health` без секретов |
| `scripts/install.sh` | LaunchAgent `com.qwen-sub-proxy` |
| `scripts/smoke.sh` | дымовой сценарий по HTTP |
| `scripts/zcode-provider.py` | патч `~/.zcode/v2/provider_config.json` с бэкапом |

## Порядок работ

1. Каркас: `go.mod`, `config`, `creds` → критерий 12.
2. `oauth` + подкоманды `login`/`status`/`logout` → критерии 13, 14.
3. `tokenmgr` + тесты конкуренции → критерии 6, 7, 8.
4. `gateway` + `proxy` (health, models, non-stream chat) → критерии 2, 3, 4, 9, 10, 11.
5. `stream.go` → критерий 5.
6. `scripts/` + документация → критерий 15.
7. Реальный логин на машине, `make install`, `make smoke`.
8. Патч конфига ZCode с бэкапом → проверка реальным ходом в чате.

## Риски

| Риск | Митигация |
|---|---|
| Ротация refresh_token и гонки refresh | single-flight, персист до подмены в памяти, один ретрай, `invalid_grant` без циклов |
| Буферизация SSE и обрыв длинных стримов | flush на чанк, нет общего таймаута клиента, отмена по контексту запроса |
| Изменение правил Qwen для неофициальных клиентов | честный User-Agent, один пользователь, интерактивный режим, без шаринга и автоматизации; в README указан официальный путь через `sk-sp-…` |
| Сжигание квоты ретраями | нет повторов на 402/429/5xx, полный проброс ответа |
| Утечка токенов | 0600/0700, отсутствие токенов в логах и в `/health`, тест на утечку |
| Размер тела с base64-картинками | лимит 32 MiB с внятным 413, vision пока не рекламируется провайдеру |
