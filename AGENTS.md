# AGENTS.md

## Назначение файла

Этот файл нужен как стартовая техническая памятка для новой сессии по репозиторию `pocker-bot-app`, чтобы не тратить токены на повторный первичный ресерч структуры, сценариев запуска и доменной логики.

## Обязательное правило сопровождения

При любом изменении кода, архитектуры, бизнес-логики, API, структуры каталогов, переменных окружения, сценариев запуска, тестирования или деплоя этот файл необходимо актуализировать в той же задаче.

Минимум, что нужно проверить после изменений:

- поменялись ли точки входа или команды запуска;
- поменялись ли env-переменные или их дефолты;
- поменялись ли HTTP routes, Telegram-команды или callback-flow;
- поменялась ли доменная логика игр, счетов, статистики или архива;
- появились ли новые ограничения, миграции, cron-like скрипты или operational caveats;
- изменился ли рекомендуемый способ тестирования.

## Кратко о проекте

Проект представляет собой Go-приложение с двумя пользовательскими поверхностями:

- Telegram bot для учета покерных игр, статистики и расчета счетов;
- Telegram Mini App + HTTP API для работы с тем же счетом (`BillService`) через web UI.

Данные хранятся в MongoDB. Приложение поднимает одновременно:

- long polling Telegram bot;
- HTTP server для Mini App и API.

## Точка входа и жизненный цикл

- Главная точка входа: `source/main.go`
- Сборка приложения: `source/internal/app/app.go`
- Конфиг: `source/internal/config/config.go`

Последовательность запуска:

1. `config.Load()` читает `.env` и env.
2. `app.New(cfg)` поднимает Mongo client, репозитории, сервисы, Telegram bot и web server.
3. `App.Run(ctx)` запускает одновременно bot и HTTP server.
4. При `SIGINT`/`SIGTERM` вызывается graceful shutdown с таймаутом 10 секунд.

## Стек и зависимости

- Go `1.22`
- Telegram Bot API: `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- MongoDB driver: `go.mongodb.org/mongo-driver`
- Decimal arithmetic: `github.com/shopspring/decimal`
- `.env` loading: `github.com/joho/godotenv`

## Структура репозитория

- `source/` — весь Go-код проекта
- `source/main.go` — главный entrypoint
- `source/internal/app/` — wiring приложения
- `source/internal/config/` — загрузка конфигурации
- `source/internal/domain/` — доменные модели
- `source/internal/service/` — бизнес-логика
- `source/internal/repository/` — repository interfaces
- `source/internal/repository/mongo/` — MongoDB реализации
- `source/internal/telegram/` — Telegram bot, handlers, rendering, callbacks
- `source/internal/webapp/` — HTTP server, auth, presenter
- `source/internal/webapp/static/` — frontend Mini App (`index.html`, `app.js`, `styles.css`)
- `source/scripts/` — вспомогательные standalone Go-скрипты
- `docs/` — продуктовая и модульная документация
- `deploy/` — legacy nginx/certbot конфиги и скрипты старого production deploy
- `docker-compose.yml` / `Dockerfile` — контейнеризация

## Доменные модули

### 1. Покерные игры

Основная логика находится в:

- `source/internal/service/game.go`
- `source/internal/service/parser.go`
- `source/internal/service/calculator.go`
- `source/internal/service/stats.go`

Сценарий:

1. Группа регистрируется через `/reg`.
2. Бот сохраняет сообщения с бай-инами и результатами.
3. Команда `/game`, отправленная reply-цепочкой, собирает входные данные.
4. `GameService` строит игру и сохраняет ее с очередным `GameNumber`.
5. Статистика и история читаются из Mongo.

Ключевые факты:

- незарегистрированный чат не может использовать игровые сценарии;
- цена бай-ина берется из настроек чата;
- номер игры генерируется через `GameRepository.NextGameNumber`.

### 2. Счета / Bill

Основная логика находится в:

- `source/internal/service/bill.go`
- `source/internal/service/openai_receipt.go`
- `source/internal/telegram/render_bill.go`
- `source/internal/webapp/server.go`

Важные правила текущей логики:

- в одном чате может быть только один активный счет;
- чек можно создать из OCR или через debug-сценарий;
- multi-quantity позиции можно разбивать на одиночные;
- одиночную позицию можно делить по `ExpectedParticipants`;
- для shared-singleton сумма делится по фактическому максимуму между expected и assigned;
- переполнение допустимо: участники могут набрать больше, чем исходное количество, и стоимость тогда перераспределяется пропорционально текущему фактическому назначению;
- закрытие счета без `force` запрещено, пока есть `Remaining != 0`;
- завершать/отменять счет может создатель счета или payer;
- Mini App и Telegram-кнопки работают через один и тот же `BillService`.
- при `BILL_AUTO_CLOSE_AFTER > 0` активные счета автоматически завершаются, если есть распределённые позиции, или отменяются, если распределений нет;
- OCR выполняет не более двух попыток: изображение экономно усиливается и поворачивается сначала на −15°, затем на +15°; после второй неудачи бот просит переснять чек.

Технические детали:

- `BillService` защищает мутабельные операции `sync.Mutex`;
- active session ищется либо по `chatID`, либо по `sessionID`;
- при изменениях Mini App может инициировать обновление группового сообщения через notifier.

### 3. Архив

Сейчас архивный UX по сути свернут в справку о переносе старого архива в обычные stats-команды.

Архивный код все еще присутствует:

- `source/internal/service/archive.go`
- `source/internal/telegram/render_archive.go`
- `source/internal/repository/mongo/archive.go`

Но пользовательский смысл на текущий момент:

- `/archive` и `archive_*` команды отдают информацию о переносе;
- часть archive-specific access logic и whitelist chat IDs все еще находится в `source/internal/telegram/bot.go`.

## Telegram bot

Основной файл:

- `source/internal/telegram/bot.go`

Что важно знать:

- используется long polling, перед стартом бот удаляет webhook;
- бот обрабатывает `message`, `edited_message`, `callback_query`;
- для групповых игровых сценариев чат должен быть зарегистрирован;
- бот хранит ограниченный in-memory `messageStore` на `500` сообщений на чат;
- есть membership cache с TTL `2h`;
- `WEBAPP_BASE_URL` влияет на тип кнопки открытия Mini App;
- если URL HTTPS и не локальный, предпочтителен deep link / Mini App flow;
- если URL локальный (`localhost`/`127.0.0.1`) или не HTTPS, используются обычные URL-кнопки/локальный режим.

Поддерживаемые ключевые команды:

- групповые: `/reg`, `/game`, `/setbuyin`, `/bill`, `/debug`, `/app`, `/history`, `/stats`, `/players`, `stats_*`, `/archive`
- личка: `/groups`, `/stats`, `/history`, `/players`, `/bill`, archive/stats команды

## Mini App и HTTP API

Основной сервер:

- `source/internal/webapp/server.go`

Статика:

- `source/internal/webapp/static/index.html`
- `source/internal/webapp/static/app.js`
- `source/internal/webapp/static/styles.css`

Маршруты:

- `GET /healthz`
- `GET /app/`
- `GET /api/webapp/bills/{sessionId}`
- `POST /api/webapp/bills/{sessionId}/items/{itemIndex}/adjust`
- `POST /api/webapp/bills/{sessionId}/items/{itemIndex}/expected-participants`
- `POST /api/webapp/bills/{sessionId}/items/{itemIndex}/split`
- `POST /api/webapp/bills/{sessionId}/finish`
- `POST /api/webapp/bills/{sessionId}/cancel`

Auth:

- production: проверка Telegram `initData` через `InitDataValidator`;
- fallback: если HMAC hash не совпал, валидируется Telegram signature;
- local dev: при `WEBAPP_DEV_MODE=true` и локальном host разрешается dev auth без Telegram init data.

## Конфигурация и env

Source of truth по конфигу:

- `source/internal/config/config.go`

Ключевые env:

- `BOT_TOKEN`
- `MONGODB_URI`
- `MONGODB_DB` default: `poker_bot`
- `DEFAULT_BUYIN_PRICE` default: `2000`
- `REGISTRAR_USER_ID`
- `OPENAI_API_KEY` или legacy `OPEN_API_KEY`
- `OPENAI_RECEIPT_MODEL` default в коде: `gpt-5.4`
- `HTTP_ADDR` default: `:8080`
- `WEBAPP_BASE_URL`
- `TELEGRAM_INIT_DATA_MAX_AGE` default: `24h`
- `BILL_AUTO_CLOSE_AFTER` default: `0` (автозакрытие отключено)
- `BILL_SWEEP_INTERVAL` default: `5m`
- `WEBAPP_DEV_MODE`
- `WEBAPP_DEV_USER_ID`
- `WEBAPP_DEV_USERNAME`
- `WEBAPP_DEV_FIRST_NAME` default: `Local`
- `WEBAPP_DEV_LAST_NAME` default: `Dev`

Критично:

- без `OPENAI_API_KEY` OCR чеков не работает;
- при `WEBAPP_DEV_MODE=true` и пустом `WEBAPP_DEV_USER_ID` код подставляет `REGISTRAR_USER_ID`;
- `DEFAULT_BUYIN_PRICE` должен быть `> 0`;
- `BOT_TOKEN`, `MONGODB_URI`, `REGISTRAR_USER_ID`, `HTTP_ADDR` обязательны.

## Документация в репозитории

Если задача касается бизнес-логики, сначала стоит смотреть:

- `docs/mini-app-spec.md`
- `docs/game-results-module.md`
- `docs/bill-module.md`
- `README.md`

## Репозитории и хранилище

Repository interfaces:

- `source/internal/repository/interfaces.go`

Mongo реализации:

- `source/internal/repository/mongo/allowed_chats.go`
- `source/internal/repository/mongo/games.go`
- `source/internal/repository/mongo/bills.go`
- `source/internal/repository/mongo/archive.go`
- `source/internal/repository/mongo/client.go`

Базовые сущности хранилища:

- allowed chats
- games
- bill sessions
- archive data

## Скрипты

В `source/scripts/` лежат standalone Go-программы:

- `backfill_game_numbers.go`
- `migrate_archive_to_games.go`

Это не библиотечный пакет. Из-за двух `main` в одной директории команда `go test ./...` сейчас падает на пакете `poker-bot/scripts`.

Практический вывод:

- для обычной регрессии лучше запускать `go test ./internal/...`;
- либо исключать `scripts` из общего тестового обхода.

## Тестирование

Что проверено по текущему состоянию:

- `go test ./...` из `source/` падает на `scripts` из-за `main redeclared`;
- `internal/service` тесты проходят;
- `internal/webapp` тесты проходят.

Если нужна безопасная базовая проверка после изменений, сначала запускать:

```powershell
cd source
go test ./internal/...
```

Если задача затрагивает wiring или entrypoint, дополнительно имеет смысл:

```powershell
cd source
go test .
go test ./internal/...
```

## Локальный запуск

Типовой локальный запуск Mini App dev mode:

```text
HTTP_ADDR=127.0.0.1:8080
WEBAPP_BASE_URL=http://127.0.0.1:8080
WEBAPP_DEV_MODE=true
WEBAPP_DEV_USER_ID=<telegram_user_id>
WEBAPP_DEV_USERNAME=<username>
```

Запуск:

```powershell
cd source
go run .
```

Что поднимется:

- Telegram bot
- HTTP server на `HTTP_ADDR`
- frontend на `/app/`
- API на `/api/webapp/bills/...`

## Docker / deploy

Production контур приложения:

- `app`
- внешний shared reverse proxy в отдельном проекте
- сертификаты и renew вынесены в отдельный proxy stack

Файлы deploy:

- `docker-compose.yml`
- `Dockerfile`
- `deploy/nginx/*` и `deploy/certbot/*` сохранены как legacy reference

Текущее ожидание по сети:

- приложение подключается к внешней Docker-сети `stas-nginx_proxy`;
- внешний nginx/certbot живет в отдельном проекте и владеет `80/443`;
- домен и TLS теперь настраиваются в отдельном proxy-репозитории, а не в `pocker-bot-app`.

В production нужен публичный HTTPS URL для нормального Mini App UX.

## Практические правила для будущих сессий

Перед изменениями сначала проверять:

- какие слои затронуты: `telegram`, `webapp`, `service`, `repository`, `config`;
- есть ли связанная документация в `docs/`;
- не нарушается ли паритет между Telegram UI и Mini App, если меняется `BillService`;
- не нужно ли обновить тексты help/rendering после изменения пользовательского поведения;
- не нужно ли обновить этот `AGENTS.md`.

Если меняется логика счетов, обязательно проверить:

- Telegram callbacks;
- Mini App API handlers;
- summary calculation;
- права creator/payer;
- состояния `active/finished/cancelled`.

Если меняется логика игр/статистики, обязательно проверить:

- parser;
- calculator;
- stats rendering;
- команды в группе и в личке;
- документацию в `docs/game-results-module.md`.

Если меняется конфиг или deploy:

- синхронизировать `README.md` и этот файл;
- проверить значения по умолчанию в `source/internal/config/config.go`;
- проверить docker/nginx сценарий, если он затронут.

## Быстрый чеклист новой сессии

1. Прочитать `AGENTS.md`.
2. При необходимости сверить `README.md` и релевантный файл в `docs/`.
3. Посмотреть `git status`.
4. Определить затрагиваемые слои.
5. После изменений обновить `AGENTS.md`, если поменялось хоть что-то из описанного выше.
