# Модуль результатов покерной игры

## Назначение

Модуль отвечает за:

- разбор входных сообщений с бай-инами и результатами;
- построение итоговой модели игры;
- расчет профита игроков и списка переводов;
- сохранение игры в MongoDB;
- пересчет последней игры после редактирования исходных Telegram-сообщений;
- выдачу истории и статистики.

## Основные сущности

Ключевые типы описаны в [source/internal/domain/game.go](/E:/gohome/pocker-bot-app/source/internal/domain/game.go):

- `AllowedChat` — зарегистрированная игровая группа и ее настройки;
- `Game` — сохраненная игра;
- `PlayerInput` — строка входных данных `<имя> <число>`;
- `PlayerResult` — итог игрока по игре;
- `Settlement` — перевод от проигравшего к выигравшему;
- `Stats`, `PlayerStats`, `GamePlayerHistory`.

## Основные сервисы

### `GameService`

Файл: [source/internal/service/game.go](/E:/gohome/pocker-bot-app/source/internal/service/game.go)

Отвечает за:

- проверку, что чат зарегистрирован;
- разбор цепочки `/game`;
- вызов калькулятора расчетов;
- выдачу следующего номера игры;
- сохранение результата в репозиторий.

Ключевые методы:

- `ParseInputs(buyInsText, resultsText)`
- `SaveGame(ctx, request)`
- `LatestGame(ctx, chatID)`
- `RecalculateLatestGame(ctx, chatID, sourceCommandMessageID, ...)`
- `History(ctx, chatID, limit)`

### `StatsService`

Файл: [source/internal/service/stats.go](/E:/gohome/pocker-bot-app/source/internal/service/stats.go)

Отвечает за чтение и агрегацию уже сохраненных игр:

- общая статистика группы;
- статистика игроков;
- история игр;
- история конкретного игрока;
- топы игроков по разным метрикам.

Ключевые методы:

- `BuildStats(ctx, chatID)`
- `BuildPlayerStats(ctx, chatID)`
- `History(ctx, chatID)`
- `Game(ctx, chatID, gameNumber)`
- `Player(ctx, chatID, name)`
- `Top(ctx, chatID, metric, limit)`

### `ChatSettingsService`

Файл: [source/internal/service/chat_settings.go](/E:/gohome/pocker-bot-app/source/internal/service/chat_settings.go)

Отвечает за:

- проверку, зарегистрирован ли чат;
- регистрацию чата через `/reg`;
- список активных игровых чатов;
- изменение цены бай-ина.

Ключевые методы:

- `IsAllowed(ctx, chatID)`
- `RegisterChat(ctx, chatID, title)`
- `ListAllowedChats(ctx)`
- `UpdateBuyInPrice(ctx, chatID, title, price)`

## Как создается игра

Основной пользовательский сценарий:

1. Сообщение с бай-инами.
2. Reply-сообщение с итогами победителей.
3. Команда `/game` reply на сообщение с итогами.

Бот извлекает:

- текст бай-инов;
- текст итогов;
- `chat_id`, `chat_title`;
- `message_id` исходных сообщений;
- пользователя, создавшего запись.

Дальше:

1. `MessageParser` разбирает игроков и числа.
2. `SettlementCalculator` строит итоговую модель:
   - общий банк;
   - результаты игроков;
   - список settlement-переводов.
3. `GameService.SaveGame` сохраняет результат в MongoDB.

## Как пересчитывается последняя игра

1. Пользователь редактирует исходное сообщение с бай-инами и/или результатами.
2. Бот получает `edited_message` и обновляет его текст в ограниченном in-memory `messageStore`.
3. Пользователь отправляет `/regame` как reply на команду `/game`, которой была сохранена игра.
4. Бот разрешает пересчет, только если эта `/game` относится к последней игре группы.
5. Актуальные тексты повторно парсятся, результат строится заново и заменяет существующую запись в MongoDB.

При замене сохраняются номер игры, дата сессии, исходная цена бай-ина, автор и время первоначального создания. Если исходные сообщения уже вытеснены из `messageStore` (лимит — 500 сообщений на чат), пересчет недоступен.

## Хранимые данные

В `Game` сохраняются:

- номер игры в группе;
- дата сессии;
- цена бай-ина;
- тексты исходных сообщений;
- количество игроков;
- список победителей;
- суммы по игрокам;
- settlement-переводы;
- итоговый банк;
- автор записи.

Это позволяет:

- пересчитывать статистику без повторного парсинга чата;
- показывать историю;
- отлаживать разбор входных сообщений.

## Что считается результатом игры

На уровне `PlayerResult` для каждого игрока хранятся:

- `BuyIns`
- `WonBuyIns`
- `ProfitBuyIns`
- `ProfitKZT`

На уровне `Settlement` хранится уже практический итог:

- кто платит;
- кому платит;
- сколько в бай-инах;
- сколько в тенге.

## Ограничения и допущения

- Группа должна быть зарегистрирована через `/reg`.
- Цена бай-ина берется из настроек группы.
- Формат входа остается текстовым, через reply-цепочку.
- Имена матчятся в статистике по нормализованной строке.
- История и статистика строятся только по сохраненным играм, а не по сообщениям Telegram напрямую.
- `/regame` работает только для последней сохраненной игры и только reply на ее исходную команду `/game`.
- В личке статистика и история доступны только по тем группам, membership в которых бот реально подтверждает через Telegram API.

## Связанные файлы

- [source/internal/domain/game.go](/E:/gohome/pocker-bot-app/source/internal/domain/game.go)
- [source/internal/service/game.go](/E:/gohome/pocker-bot-app/source/internal/service/game.go)
- [source/internal/service/stats.go](/E:/gohome/pocker-bot-app/source/internal/service/stats.go)
- [source/internal/service/chat_settings.go](/E:/gohome/pocker-bot-app/source/internal/service/chat_settings.go)
- [source/internal/repository/mongo/games.go](/E:/gohome/pocker-bot-app/source/internal/repository/mongo/games.go)
- [source/internal/telegram/bot.go](/E:/gohome/pocker-bot-app/source/internal/telegram/bot.go)
