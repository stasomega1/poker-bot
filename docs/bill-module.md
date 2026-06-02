# Модуль расчета счета

## Назначение

Модуль отвечает за:

- создание активного счета из OCR-чека или debug-данных;
- хранение активной bill session в MongoDB;
- распределение позиций между участниками;
- расчет промежуточного и финального долга каждого участника;
- публикацию итогового сообщения в группу;
- работу Telegram Mini App поверх той же бизнес-логики.

## Основные сущности

Ключевые типы описаны в [source/internal/domain/bill.go](/E:/gohome/pocker-bot-app/source/internal/domain/bill.go):

- `BillSession` — активный или закрытый счет по чату;
- `BillItem` — позиция чека;
- `BillAssignment` — назначение позиции пользователю;
- `BillParticipantSummary` — итог пользователя по счету;
- `ParsedReceipt` — результат OCR.

## Основной сервис

Файл: [source/internal/service/bill.go](/E:/gohome/pocker-bot-app/source/internal/service/bill.go)

`BillService` отвечает за весь жизненный цикл счета.

### Создание

- `CreateFromReceipt(...)`
- `CreateDebugReceipt(...)`

При создании:

- проверяется, что в чате нет другого активного счета;
- OCR превращает чек в набор позиций;
- считается `ItemsSubtotal`;
- создается `BillSession` со статусом `active`.

### Мутации

- `AdjustItem(...)` — `+1` / `-1` для позиции
- `SetExpectedParticipants(...)` — для одиночной позиции выставить `делят на N человек`
- `SplitItemIntoSingles(...)` — разбить multi-quantity позицию на одиночные
- `Finish(...)`, `FinishBySessionID(...)`
- `Cancel(...)`, `CancelBySessionID(...)`

### Чтение

- `GetActive(...)`
- `GetByID(...)`
- `Summary(...)`
- `SummaryBySessionID(...)`
- `MySummary(...)`
- `LatestUserSummary(...)`

## Модель распределения

### Обычные позиции

Для обычной позиции логика простая:

- `quantity` — физическое количество в чеке;
- `assigned` — сколько уже разобрано;
- `remaining` — сколько еще осталось.

### Разбитые multi-quantity позиции

Если позиция с количеством больше `1` разбивается по одной:

- одна строка заменяется несколькими `BillItem` с `quantity = 1`;
- старые назначения перераскладываются по новым item index.

### Одиночная позиция, которую делят несколько человек

Для такого сценария используется поле `ExpectedParticipants`.

Пример:

- кальян `1 шт`, `12 000`
- `делят на 2 человек`

Тогда:

- `quantity` остается `1`
- `expectedParticipants = 2`
- `effectiveQuantity = 2`
- `remaining` считается уже по слотам участия

Если потом участников становится больше, чем ожидалось, переполнение разрешено:

- `assigned` может стать `3` при `expectedParticipants = 2`
- прогресс счета расширяется динамически
- денежная доля пересчитывается на фактический `assigned`

## Расчет суммы пользователя

`calculateSummary(...)` строит `BillParticipantSummary` по всем `BillAssignment`.

### Стоимость позиций

- Для обычных позиций:
  - цена считается пропорционально выбранному количеству.
- Для shared-singleton:
  - сумма считается как `lineTotal / max(expectedParticipants, assigned) * quantity`.

Это означает:

- если позиция делится на `2`, а выбрал пока только один человек, он видит половину;
- если позже выбрал второй, у обоих остается половина;
- если появляется третий, сумма автоматически пересчитывается уже на троих.

### Сервис

Сервис распределяется не по числу людей и не по уже выбранным позициям, а по доле пользователя в полной сумме позиций чека:

`serviceShare = session.ServiceAmount * userItemsTotal / session.ItemsSubtotal`

Это делает сервис стабильным и не дает первому выбранному участнику временно получить весь сервис.

## Прогресс счета

Верхний прогресс считает:

- `assignedUnits` — сумму фактических назначений;
- `totalUnits` — сумму `ProgressCapacity()` по всем позициям;
- `remainingUnits` — сумму `remaining`.

Для обычной позиции `ProgressCapacity()` совпадает с обычной емкостью.
Для переполненной позиции прогресс расширяется динамически.

Пример:

- было `2 из 45`
- пользователь сделал `3/2`
- стало `3 из 46`

## Telegram UI

Telegram-сообщение счета:

- показывает общий статус и список позиций;
- обновляется после изменений;
- поддерживает inline-кнопки `- / +`;
- умеет отправлять личный счет;
- умеет закрывать и отменять счет;
- показывает пометку `разбито на N человек` только для одиночных позиций с `expectedParticipants > 1`.

Файлы:

- [source/internal/telegram/render_bill.go](/E:/gohome/pocker-bot-app/source/internal/telegram/render_bill.go)
- [source/internal/telegram/bot.go](/E:/gohome/pocker-bot-app/source/internal/telegram/bot.go)

## Mini App

Mini App использует тот же `BillService` через HTTP API.

Что умеет:

- открыть счет по `session_id`;
- выбрать или убрать позицию;
- менять `делят на N человек` для одиночных позиций;
- разбивать multi-quantity позиции;
- закрывать счет;
- показывать персональный итог и прогресс.

### Что есть в API, но не выведено в текущий UI

HTTP API умеет:

- `finish`
- `cancel`

Но текущий frontend Mini App выводит:

- `Закрыть счет`
- `Закрыть бота`

Отмена счета сейчас доступна в чатовых inline-кнопках и через API, но не отдельной кнопкой в текущем Mini App UI.

Файлы:

- [source/internal/webapp/server.go](/E:/gohome/pocker-bot-app/source/internal/webapp/server.go)
- [source/internal/webapp/presenter.go](/E:/gohome/pocker-bot-app/source/internal/webapp/presenter.go)
- [source/internal/webapp/static/app.js](/E:/gohome/pocker-bot-app/source/internal/webapp/static/app.js)
- [source/internal/webapp/static/styles.css](/E:/gohome/pocker-bot-app/source/internal/webapp/static/styles.css)

## Ограничения текущей реализации

- Деление общей одиночной позиции все еще называется в UI `человек`, хотя фактически допускается и переполнение.
- Переполнение intentionally разрешено и в Telegram, и в Mini App.
- `remaining` для переполненной позиции остается `0`; расширяется именно общий знаменатель прогресса.
- В Mini App polling используется вместо websocket/SSE.

## Связанные файлы

- [source/internal/domain/bill.go](/E:/gohome/pocker-bot-app/source/internal/domain/bill.go)
- [source/internal/service/bill.go](/E:/gohome/pocker-bot-app/source/internal/service/bill.go)
- [source/internal/repository/mongo/bills.go](/E:/gohome/pocker-bot-app/source/internal/repository/mongo/bills.go)
- [source/internal/telegram/render_bill.go](/E:/gohome/pocker-bot-app/source/internal/telegram/render_bill.go)
- [source/internal/webapp/server.go](/E:/gohome/pocker-bot-app/source/internal/webapp/server.go)
