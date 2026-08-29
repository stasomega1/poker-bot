# poker-bot

Телеграм-бот для учета покерных игр, статистики по играм и разделения счета по чеку.   
В проекте есть два пользовательских интерфейса для счета:

- обычные inline-кнопки в Telegram;
- Telegram Mini App поверх того же `BillService`.

## Документация

- [Mini App spec](/E:/gohome/pocker-bot-app/docs/mini-app-spec.md)
- [Модуль результатов игры](/E:/gohome/pocker-bot-app/docs/game-results-module.md)
- [Модуль расчета счета](/E:/gohome/pocker-bot-app/docs/bill-module.md)

## Переменные окружения

Перед запуском заполните `.env`:

- `BOT_TOKEN` — токен Telegram-бота
- `MONGODB_URI` — строка подключения к MongoDB
- `MONGODB_DB` — имя базы данных, по умолчанию `poker_bot`
- `DEFAULT_BUYIN_PRICE` — цена бай-ина по умолчанию, по умолчанию `2000`
- `REGISTRAR_USER_ID` — `telegram user id` пользователя, который может вызывать `/reg`
- `OPENAI_API_KEY` или `OPEN_API_KEY` — ключ OpenAI для распознавания чеков через `/bill`
- `OPENAI_RECEIPT_MODEL` — модель OpenAI для OCR чека, по умолчанию `gpt-4.1-mini`
- `HTTP_ADDR` — адрес HTTP-сервера для Mini App и API, по умолчанию `:8080`
- `WEBAPP_BASE_URL` — публичный HTTPS URL приложения, например `https://example.com`
- `TELEGRAM_INIT_DATA_MAX_AGE` — срок жизни Telegram Mini App init data, по умолчанию `24h`
- `WEBAPP_DEV_MODE` — локальный dev-режим Mini App без Telegram init data
- `WEBAPP_DEV_USER_ID` — `telegram user id` пользователя для dev-режима
- `WEBAPP_DEV_USERNAME` — username для dev-режима, необязательно

Если OpenAI-ключ не указан:

- `/bill` с OCR работать не будет;
- `/debug` для тестового счета продолжит работать.

## Команды в группе

- `/start` `/help` — показать справку
- `/reg` — зарегистрировать группу для игр
- `/game` — сохранить игру из цепочки reply-сообщений
- `/setbuyin 2500` — изменить цену бай-ина для группы
- `/bill` — отправить фото чека с подписью `/bill` или `/bill @payer`
- `/debug` — создать тестовый счет без OCR
- `/app` — отправить кнопку открытия Mini App для активного счета
- `/history` — показать последние игры текущей группы
- `/stats` — показать краткую статистику текущей группы
- `/players` — показать статистику игроков текущей группы
- `/archive` — показать сообщение о переносе архива в обычную статистику

### Расширенные команды статистики в группе

- `/stats_history` — показать всю историю игр группы
- `/stats_stats` — показать статистику группы
- `/stats_players` — показать расширенную статистику игроков
- `/stats_game <номер>` — показать конкретную игру
- `/stats_player <имя>` — показать статистику и историю игрока
- `/stats_top` — показать топ игроков по выбранному параметру

Поддерживаются alias-команды:

- `/stats_game_54`
- `/stats_player_anelya`

## Команды в личке

- `/start` `/help`
- `/groups`
- `/stats`
- `/history`
- `/players`
- `/bill` — показать подсказку по использованию счета
- `/archive`
- `/stats_history`
- `/stats_stats`
- `/stats_players`
- `/stats_game <номер>`
- `/stats_player <имя>`
- `/stats_top`
- `/archive_history`
- `/archive_stats`
- `/archive_players`
- `/archive_game`
- `/archive_player`
- `/archive_top`

Особенности:

- если пользователь состоит в нескольких зарегистрированных группах, бот предлагает выбрать группу кнопками;
- архивные команды в личке сейчас не открывают отдельный архивный UI, а отдают справку о переносе архива в обычные stats-команды.

## Покерные игры

Базовый сценарий:

1. Зарегистрировать группу:

```text
/reg
```

2. Отправить сообщение с бай-инами:

```text
Стас 4
Адильхан 1
Игорь 3
Анеля 3
```

3. Ответом на него отправить сообщение с результатами:

```text
Игорь 26
Жаник 21
```

4. Ответом на сообщение с результатами отправить:

```text
/game
```

5. Посмотреть историю и статистику:

```text
/history
/stats
/players
```

Подробности по модели игры и статистике вынесены в [docs/game-results-module.md](/E:/gohome/pocker-bot-app/docs/game-results-module.md).

## Расчет счета

Базовый сценарий:

1. Отправить фото чека с подписью:

```text
/bill
```

или указать плательщика сразу:

```text
/bill @stasninja
```

2. Бот распознает чек и создаст одно активное сообщение счета в группе.

3. Участники распределяют позиции:

- через inline-кнопки в Telegram;
- или через Mini App.

4. После закрытия бот публикует, кто сколько должен перевести плательщику.

### Особенности текущей логики счета

- В одном чате может быть только один активный счет.
- Multi-quantity позицию можно разбить по одной.
- Для одиночной позиции можно выставить `делят на N человек`.
- Для таких позиций сумма считается по долям:
  - если позиция делится на `2`, а выбрал пока только один человек, он видит половину;
  - если потом присоединяется третий, доля пересчитывается уже на троих.
- Переполнение разрешено:
  - можно дойти до `3/2`, `4/2` и т.д.;
  - общий прогресс счета при этом тоже расширяется динамически.

Подробности вынесены в [docs/bill-module.md](/E:/gohome/pocker-bot-app/docs/bill-module.md).

## Telegram Mini App

Бот поднимает HTTP-сервер:

- frontend: `/app/`
- API: `/api/webapp/bills/...`
- healthcheck: `/healthz`

Если `WEBAPP_BASE_URL` заполнен, бот добавляет кнопку `Открыть счет`.
Для production используется Telegram deep link на Mini App, а не прямой `web_app` inline-button under group message.

Mini App работает с тем же `BillService`, что и Telegram-кнопки:

- показывает активный счет;
- позволяет выбирать и убирать позиции;
- показывает личный итог;
- позволяет менять `делят на N человек` для одиночных позиций;
- позволяет создателю счета или плательщику разбивать позиции и закрывать счет;
- обновляет сообщение счета в группе после изменений.

Текущее состояние Mini App UI:

- есть кнопка `Закрыть счет`;
- есть кнопка `Закрыть бота`;
- отдельной кнопки `Отменить счет` в текущем UI нет, хотя cancel остается в чатовых inline-кнопках и в HTTP API.

Для production нужен публичный HTTPS URL.  
Backend проверяет `Telegram.WebApp.initData` на каждом API-запросе.

Для локальной разработки можно включить dev-режим:

```text
HTTP_ADDR=127.0.0.1:8080
WEBAPP_BASE_URL=http://127.0.0.1:8080
WEBAPP_DEV_MODE=true
WEBAPP_DEV_USER_ID=202999546
WEBAPP_DEV_USERNAME=your_username
BILL_AUTO_CLOSE_AFTER=0
BILL_SWEEP_INTERVAL=5m
```

`BILL_AUTO_CLOSE_AFTER` задаёт срок жизни активного счёта (`72h`, например). Значение `0` отключает автозакрытие. `BILL_SWEEP_INTERVAL` задаёт период проверки просроченных счетов.

В этом режиме страница `/app/` на `127.0.0.1` работает без Telegram init data.  
Это только для локальной отладки.

## Docker deploy

Production-контур приложения:

- `app` — Go-приложение
- внешний shared reverse proxy — отдельный проект, который владеет `80/443` и TLS
- сертификаты и renew живут в отдельном proxy stack

Нужны env-переменные:

```text
WEBAPP_BASE_URL=https://bot.example.com
HTTP_ADDR=0.0.0.0:8080
```

В этом репозитории для контейнеризации приложения используются:

- [docker-compose.yml](/E:/gohome/pocker-bot-app/docker-compose.yml)
- [Dockerfile](/E:/gohome/pocker-bot-app/Dockerfile)

Legacy nginx/certbot-файлы в `deploy/` сохранены только как reference.

Порядок production-запуска теперь такой:

1. Поднять shared reverse proxy в отдельном проекте.
2. Убедиться, что создана внешняя Docker-сеть `stas-nginx_proxy`.
3. Поднять приложение:

```bash
docker compose up -d --build app
```

4. Добавить доменный конфиг в shared proxy и направить upstream на контейнер `poker-bot-app:8080`.
5. Выпустить сертификат Let's Encrypt уже из proxy-проекта.

После этого Mini App должен открываться по `https://<domain>/app/`.

## Архив

Игры из старого архива перенесены в обычные команды.
Текущие `/archive` и `archive_*` команды по факту отдают справку о переносе:

- `/stats`
- `/history`
- `/players`
- `/stats_history`
- `/stats_players`
- `/stats_game <номер>`
- `/stats_player <имя>`
- `/stats_top`
