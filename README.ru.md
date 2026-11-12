# qwen-sub-proxy

**Используй подписку [chat.qwen.ai](https://chat.qwen.ai) Plus/Pro из любого OpenAI-совместимого клиента** — ZCode, Cline, Cursor, Continue, OpenCode, Roo, Aider или из своего скрипта.

English version: [README.md](README.md)

---

## Зачем это нужно

У подписки chat.qwen.ai (Qwen Plus / Pro) **нет API-ключа**. Аккаунт авторизуется через OAuth, а inference отдаётся OpenAI-совместимым шлюзом `https://api.qwen.ai/compatible-mode/v1`, который ждёт OAuth access token в заголовке `Bearer`.

Официальные ключи для сторонних клиентов выдают только другие продукты — Alibaba Cloud ModelStudio **Coding Plan** и QwenCloud **Token Plan** (ключи `sk-sp-…`). Если подписка на chat.qwen.ai уже оплачена, штадного способа направить туда другой инструмент нет.

`qwen-sub-proxy` закрывает этот зазор: сам проходит OAuth device flow, сам поддерживает access_token в актуальном состоянии и поднимает локальный OpenAI-совместимый эндпоинт.

```
ZCode ──HTTP/OpenAI──▶ 127.0.0.1:8791 ──HTTPS/OpenAI + Bearer──▶ api.qwen.ai
                            │
                            └── OAuth device flow ──▶ auth.qwen.ai
```

Это **не** веб-скрейпинг. Ни cookie, ни парсинга HTML, ни DOM, ни браузера в пути запроса. Браузер нужен ровно один раз — чтобы подтвердить OAuth-логин. Все вызовы моделей идут на официальный inference-шлюз по HTTPS.

---

## Требования

- macOS или Linux, Go 1.26+ (для сборки), аккаунт chat.qwen.ai с активной подпиской Plus или Pro
- Любой OpenAI-совместимый клиент

## Установка

```bash
git clone https://github.com/Tozix/qwen-sub-proxy.git
cd qwen-sub-proxy
make build          # ./qwen-sub-proxy
make test           # юнит-тесты
make vet            # go vet
```

Проект использует **только стандартную библиотеку Go**: нет `go.sum`, нет внешних зависимостей.

### 1. Авторизация

```bash
./qwen-sub-proxy login
```

Команда открывает в браузере `https://chat.qwen.ai/authorize?user_code=…`. Подтверди запрос — в терминале появится найденный план:

```
authorized: plan=plus email=you@example.com
credentials stored in /Users/you/.config/qwen-sub-proxy/credentials.json
```

Код устройства живёт 10 минут. Если вместо страницы подтверждения открылся обычный чат — используй ссылку, напечатанную командой: в ней есть `user_code`.

### 2. Запуск прокси

```bash
./qwen-sub-proxy serve
```

```
qwen-sub-proxy 0.1.0 listening on http://127.0.0.1:8791
local api key file: /Users/you/.config/qwen-sub-proxy/client-key
use it in your client as base URL http://127.0.0.1:8791/v1
```

На macOS сервис можно оставить под управлением launchd:

```bash
make install        # LaunchAgent com.qwen-sub-proxy
make healthcheck    # проверка запущенного экземпляра
make smoke          # health + модели + реальный запрос
make uninstall      # удаление LaunchAgent
```

Сервис слушает только loopback, требует локальный bearer-ключ и сам перезапускается при падении.

### 3. Подключение клиента

| Параметр | Значение |
|---|---|
| Base URL | `http://127.0.0.1:8791/v1` |
| API key | содержимое `~/.config/qwen-sub-proxy/client-key` (печатает `./qwen-sub-proxy status`) |
| Протокол | OpenAI Chat Completions |

```bash
curl http://127.0.0.1:8791/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.config/qwen-sub-proxy/client-key)" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.8-max","messages":[{"role":"user","content":"привет"}]}'
```

<details>
<summary>ZCode</summary>

Добавь кастомный провайдер в **Settings → Model Provider** (или отредактируй `~/.zcode/v2/provider_config.json`):

- Протокол: `openai-chat-completions`
- Base URL: `http://127.0.0.1:8791/v1`
- API key: локальный ключ
- Модели: `qwen3.8-max`, `qwen3.8-flash`, `qwen4.0-max-preview`
- Context window: `1000000`
- Max output tokens: `131072`
- Vision: включить

Готовый скрипт, который патчит конфиг ZCode с бэкапом:

```bash
./scripts/zcode-provider.py --apply
```
</details>

<details>
<summary>Cline / Cursor / Roo / Continue / OpenCode</summary>

У всех есть режим провайдера «OpenAI Compatible». Укажи base URL и ключ выше, затем добавь модели из таблицы ниже. В зависимости от клиента может понадобиться переключить провайдер с «OpenAI» на OpenAI-совместимый.
</details>

<details>
<summary>Claude Code</summary>

Claude Code говорит по протоколу Anthropic, а прокси — по OpenAI. Нужен мост перед ним, либо используй официальный эндпоинт Alibaba Coding Plan: у него есть Anthropic-совместимый маршрут (`https://coding.dashscope.aliyuncs.com/apps/anthropic`).
</details>

---

## Модели и лимиты

Живой каталог берётся из `GET /v1/models` и кэшируется на 10 минут. На момент последней проверки:

| Model id | Контекст | Вход | Максимум вывода (`max_tokens`) |
|---|---|---|---|
| `qwen3.8-max` | 1 000 000 | text, image, video | 1 – 131 072 |
| `qwen3.8-flash` | 1 000 000 | text, image, video | 1 – 131 072 |
| `qwen4.0-max-preview` | 1 000 000 | text, image, video | шлюз не проверяет |

## Форматы входа

Тело запроса передаётся на шлюз байт-в-байт, поэтому работает всё, что понимает шлюз.

| Вход | Content part | Примечания |
|---|---|---|
| Текст | `{"type":"text","text":"…"}` | |
| Картинка | `{"type":"image_url","image_url":{"url":"data:image/png;base64,…"}}` | Принимаются и `https://` URL. Шлюз требует ширину и высоту больше 10 px. |
| Видео | `{"type":"video_url","video_url":{"url":"https://…"}}` | Шлюз скачивает файл по URL. |
| Аудио | `{"type":"input_audio","input_audio":{"data":"…","format":"wav"}}` | |

## Параметры запроса

| Параметр | Поведение |
|---|---|
| `stream: true` | Server-sent events без буферизации — первый токен доходит до клиента сразу |
| `max_tokens` | `1` – `131072` для моделей 3.8; шлюз отвергает остальное с `invalid_parameter_error` |
| `enable_thinking: false` | Отключает reasoning; `reasoning_content` пропадает |
| `tools` / `tool_choice` | Function calling работает, возвращаются стандартные OpenAI `tool_calls` |

### Размышления модели

Qwen возвращает reasoning в **нестандартном поле**: `message.reasoning_content` для полного ответа и `delta.reasoning_content` для потокового чанка. Это не поле OpenAI `reasoning`, поэтому многие клиенты его игнорируют и показывают только финальный текст. Так и задумано, это не баг.

## Команды

| Команда | Что делает |
|---|---|
| `login` | OAuth device flow, сохранение кредов, печать плана |
| `serve` | Запуск локального прокси (`--host`, `--port`) |
| `status` | План, остаток жизни токена, локальный ключ |
| `models` | Каталог моделей шлюза |
| `logout` | Отзыв refresh_token на сервере и удаление локальных кредов |
| `healthcheck` | Проверка запущенного экземпляра |

## Конфигурация

| Переменная | По умолчанию | Значение |
|---|---|---|
| `QWEN_SUB_PROXY_HOME` | `~/.config/qwen-sub-proxy` | Каталог конфигурации |
| `QWEN_SUB_PROXY_KEY` | генерируется | Фиксированный локальный ключ вместо сгенерированного |
| `QWEN_SUB_PROXY_LABEL` | `com.qwen-sub-proxy` | label launchd для `scripts/install.sh` |

| Файл | Содержимое |
|---|---|
| `~/.config/qwen-sub-proxy/credentials.json` | Пара OAuth-токенов, права `0600` |
| `~/.config/qwen-sub-proxy/client-key` | Локальный API-ключ прокси, права `0600` |

## Эндпоинты

| Метод | Путь | Назначение |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions, со стримингом и без |
| `GET` | `/v1/models` | Каталог моделей (кэш) |
| `GET` | `/health` | Состояние сервиса: план, жизнь токена, кэш моделей — без единого токена |

## Как работает обновление токена

- Access token короткоживущий. Прокси обновляет его за 60 секунд до истечения и ещё раз, если шлюз ответил `401`.
- При обновлении пара токенов ротируется. Новый refresh_token пишется на диск **до** подмены в памяти.
- Параллельные запросы делят одно обновление: всплеск одновременных `401` вызывает ровно один обмен токенов.
- Ошибка обновления возвращается всем ожидающим запросам, поэтому всплеск не превращается в шторм запросов токенов.
- Если отклонён сам refresh_token (`invalid_grant`), прокси отвечает `401 reauthentication_required`, сохраняет креды на диске и показывает `logged_in:false` в `/health`. Нужно снова выполнить `qwen-sub-proxy login`.

## Устранение неполадок

| Симптом | Причина и решение |
|---|---|
| `405` от `auth.qwen.ai` при логине | WAF отсекает дефолтный `curl` User-Agent. Прокси всегда шлёт свой; если делаешь флоу руками — поставь не-curl User-Agent. |
| Вместо страницы подтверждения открылся чат | Используй точную ссылку из вывода `login`: в ней есть `user_code`. |
| `401 invalid_local_key` | Клиент шлёт не тот ключ. Скопируй из `~/.config/qwen-sub-proxy/client-key`. |
| `401 reauthentication_required` | Истёк refresh_token. Выполни `./qwen-sub-proxy login`. |
| `402 insufficient_quota` | Исчерпан недельный кредит подписки. `error.quota.reset_at` содержит время сброса. Прокси пробрасывает ошибку без изменений и никогда её не повторяет. |
| `invalid_parameter_error: Range of max_tokens should be [1, 131072]` | Уменьши `max_tokens`. |
| В стриминге ничего не приходит | Посмотри `~/Library/Logs/qwen-sub-proxy.log` — там статус каждого запроса. |

## Юридические рамки и условия использования

Проект **не связан с Alibaba, Qwen и их дочерними компаниями**. Это независимый клиент публично доступных эндпоинтов сервиса, за который платишь ты.

- Условия Qwen описывают сервис как предназначенный для **частного некоммерческого использования** и запрещают автоматизированный или программный массовый сбор, обход rate limit, шаринг учётных данных и обход защит. Этот инструмент рассчитан на личное интерактивное использование в одном клиенте: без пула аккаунтов, без шаринга кредов, без скрейпинга, без обхода лимитов.
- Прокси называет себя честно: `User-Agent: qwen-sub-proxy/<version> (darwin; arm64)`. Он **не** выдаёт себя за официальный клиент Qwen Code.
- Использование подписки в неофициально поддерживаемом клиенте — **не одобренный** сценарий. Qwen может в любой момент изменить условия или заблокировать неизвестные клиенты, и этот риск остаётся на пользователе.
- Если нужны те же модели с официальной интеграцией — используй **Alibaba Cloud ModelStudio Coding Plan** или **QwenCloud Token Plan**: там выдаётся ключ `sk-sp-…`, а документация прямо перечисляет сторонние клиенты (Claude Code, Cline, Cursor, Codex, OpenCode, Cherry Studio, Qoder, Kilo CLI). Прокси не нужен.

Ответственность за то, как ты используешь свою подписку, лежит на тебе.

## Разработка

```
cmd/qwen-sub-proxy   CLI: serve | login | status | models | logout | healthcheck
internal/config      Пути, User-Agent, значения по умолчанию
internal/creds       Хранение кредов (0600/0700, атомарная запись) и локальный ключ
internal/oauth       Device flow, обмен и refresh токенов, userinfo, revoke
internal/tokenmgr    Жизненный цикл access token, single-flight refresh
internal/gateway     HTTP-клиент api.qwen.ai
internal/proxy       HTTP-сервер: /v1/chat/completions, /v1/models, /health
docs/protocol.md     Контракт OAuth и шлюза, как он наблюдается
scripts/             install.sh, uninstall.sh, smoke.sh, zcode-provider.py
```

```bash
make build && make test && make vet
```

Дизайн и приёмочные критерии — в [PLAN.md](PLAN.md), правила репозитория — в [AGENTS.md](AGENTS.md).

## Лицензия

[MIT](LICENSE)
