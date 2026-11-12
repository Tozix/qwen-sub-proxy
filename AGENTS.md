# Workspace Instructions for qwen-sub-proxy

Локальный OpenAI-совместимый прокси для подписки **chat.qwen.ai (Qwen Plus/Pro)**, чтобы использовать её в ZCode и других клиентах, умеющих работать с API-ключом.

## Назначение и структура

- `cmd/qwen-sub-proxy`: точка входа, подкоманды `serve | login | status | models | logout | healthcheck`.
- `internal/`: пакеты по зонам ответственности:
  - `config`: пути конфигурации, User-Agent, значения по умолчанию.
  - `creds`: хранение OAuth-кредов (директория 0700, файлы 0600, атомарная запись) и локальный ключ доступа к прокси.
  - `oauth`: device flow, обмен и refresh токенов, userinfo, revoke.
  - `tokenmgr`: единая выдача access_token, single-flight refresh, отметка `ErrReauthentication`.
  - `gateway`: HTTP-клиент шлюза `api.qwen.ai/compatible-mode/v1`.
  - `proxy`: HTTP-сервер — `/v1/chat/completions`, `/v1/models`, `/health`.
- `scripts/`: `install.sh` (LaunchAgent), `uninstall.sh`, `smoke.sh`, `zcode-provider.py` (патч конфига ZCode).
- `docs/protocol.md`: контракт OAuth и шлюза.

## Команды

- **Сборка**: `make build` (бинарник `./qwen-sub-proxy`).
- **Тесты**: `make test` (`go test ./...`).
- **Проверка**: `make vet` (`go vet ./...`).
- **Дымовой тест**: `make smoke` (нужен запущенный прокси).
- **Живость**: `make healthcheck`.
- **Установка в автозапуск**: `make install` (LaunchAgent `com.qwen-sub-proxy`, лог в `~/Library/Logs/qwen-sub-proxy.log`).

## Правила реализации

- **Только стандартная библиотека Go.** Внешние зависимости не допускаются, `go.sum` не должен появляться.
- **Креды не попадают в логи и в ответы.** `/health` отдаёт только план, срок и список моделей. Проверяется тестом `TestHealthDoesNotLeakCredentials`.
- **Refresh только на 401 и при истечении токена.** На 402/429/5xx повторов нет — иначе сгорает кредит и маскируется причина.
- **Ровно один повтор запроса** после успешного refresh.
- **Single-flight:** параллельные запросы при протухшем токене вызывают refresh ровно один раз (`TestConcurrentAccessTokenRefreshesOnce`).
- **SSE не буферизуется:** flush после каждого чанка, отмена upstream-запроса при обрыве клиента.
- **Прокси не имитирует официальный клиент.** Исходящий User-Agent — `qwen-sub-proxy/<version> (darwin; arm64)`. Строка `QwenCode` в бинарнике отсутствует.
- **Сервер слушает только loopback** и требует локальный Bearer-ключ.

## Границы использования

Прокси существует для личного интерактивного использования в одном клиенте. Никаких пулов аккаунтов, шаринга кредов, автоматизированного сбора, обхода квот и агрессивных ретраев — это прямо запрещено ToS Qwen.

## Вне границ

Не менять код ZCode. Не ставить Qwen Code. Не добавлять веб-интерфейс, пул аккаунтов, кэш completions, трансляцию tool-calls или vision-конвертацию, systemd/Linux-вариант автозапуска. Не трогать существующие провайдеры в `~/.zcode/v2/provider_config.json` — только добавить запись.
