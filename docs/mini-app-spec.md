# Poker Bot Mini App Spec

## Goal

Add a Telegram Mini App as a second UI for the existing poker bot.

The Mini App should reuse the current Go services and MongoDB repositories. The current Telegram bot remains responsible for group commands, receipt creation, and posting final messages back to the chat.

The first Mini App version should focus on bill splitting because this is the flow that suffers most from Telegram inline button limits.

## Current Bot Features

### Group Setup

- `/reg` registers a Telegram group for poker games.
- `/setbuyin <amount>` changes the default buy-in price for the group.
- Unregistered groups cannot use game and bill commands.

### Poker Games

- `/game` saves a game from a reply chain:
  - one message with buy-ins;
  - one reply message with results;
  - `/game` as a reply to the results message.
- The bot parses player names and amounts.
- The bot calculates winners, losses, total buy-ins, and settlements.
- Game history and stats are stored by `chat_id`.

### Game Stats

- `/history` shows recent games.
- `/stats` shows short group stats.
- `/players` shows player stats.
- Extended commands:
  - `/stats_history`
  - `/stats_stats`
  - `/stats_players`
  - `/stats_game <number>`
  - `/stats_player <name>`
  - `/stats_top`
- Alias commands are supported, for example:
  - `/stats_game_54`
  - `/stats_player_anelya`

### Bill Splitting

- `/bill` creates a bill from a receipt photo.
- `/bill @payer` sets a payer explicitly.
- `/debug` creates a test bill without OCR.
- The bot recognizes receipt items using OCR when OpenAI credentials are configured.
- The bot creates one active bill session per chat.
- Participants assign and unassign item quantities with inline buttons.
- Multi-quantity items can be split into single-unit items.
- A participant can request their current bill in private messages.
- A bill can be closed only after all positions are assigned unless force close is used.
- After closing, the bot posts transfer amounts to the payer.

### Private Chat

- `/groups` shows available groups.
- Stats and history commands can be used in private chat.
- If a user has access to multiple groups, the bot asks them to choose a group.

## Existing Backend Building Blocks

The Mini App should reuse these existing services:

- `GameService`
- `StatsService`
- `BillService`
- `ChatSettingsService`

The first MVP should primarily use `BillService`.

Relevant bill methods already exist:

- `GetActive(ctx, chatID)`
- `AdjustItem(ctx, sessionID, userID, userName, itemIndex, delta)`
- `SplitItemIntoSingles(ctx, sessionID, itemIndex)`
- `Summary(ctx, chatID)`
- `MySummary(ctx, chatID, userID)`
- `Finish(ctx, chatID, force)`
- `Cancel(ctx, chatID)`

Relevant bill domain models:

- `BillSession`
- `BillItem`
- `BillAssignment`
- `BillParticipantSummary`

## MVP Scope

### Entry Point

The bot should send a Telegram Mini App button in the group when there is an active bill.

Possible entry points:

- add an "Open bill" Mini App button to the `/bill` message;
- add a new `/app` command that opens the active bill;
- add a bot menu button later.

For the MVP, the preferred entry point is an inline button under the active bill message.

### Bill Screen

The Mini App should show:

- merchant name;
- bill status;
- payer name;
- created date;
- total amount;
- service amount;
- assigned units count;
- remaining units count;
- list of receipt items.

Each item should show:

- item index;
- name;
- quantity;
- unit price;
- line total;
- assigned quantity;
- remaining quantity;
- current assignments by user.

### Participant Actions

The current Telegram user should be able to:

- add one unit of an item to themselves;
- remove one unit of an item from themselves;
- see their current selected items;
- see their current total;
- see their service share;
- see their grand total.

### Organizer Actions

For the MVP, bill creator and payer should be allowed to:

- split a multi-quantity item into single-unit items;
- cancel the bill;
- finish the bill;
- force finish the bill if unassigned positions remain.

Access rules can be tightened later.

### Sync

For the MVP, polling is enough.

Recommended behavior:

- fetch bill state on app open;
- refetch after every mutation;
- poll active bill state every 1-2 seconds while the app is visible.

WebSocket or SSE can be added later if polling becomes painful.

## Out Of Scope For MVP

- Replacing the `/game` reply-chain flow.
- Creating poker games from the Mini App.
- Editing historical games.
- Full stats dashboard.
- WebSocket/SSE live sync.
- Admin panel.
- OCR upload from the Mini App.
- Payment links.
- Multi-language UI.

## Authentication

The frontend must send `Telegram.WebApp.initData` to the backend.

The backend must:

- validate `initData` using `BOT_TOKEN`;
- reject missing or invalid `initData`;
- extract Telegram user id, name, username, and auth date;
- reject stale auth data if the configured max age is exceeded;
- never trust `user_id`, `chat_id`, or `session_id` from the frontend without server-side checks.

The backend should use the Telegram user from validated `initData` for all participant actions.

## Chat And Session Access

The current bot stores bill sessions by `chat_id`.

The Mini App needs a reliable way to open the correct bill:

- preferred: open the Mini App with `session_id` in the start parameter or URL parameter;
- backend loads the session by `session_id`;
- backend verifies that the session is active or returns a read-only closed state;
- backend verifies that the user can access the chat/session.

Access verification options:

- check Telegram chat membership through Bot API;
- reuse the bot's existing available-groups logic where possible;
- for MVP, allow access if the user opened a valid session link and the backend can verify membership.

Open question: decide whether session links are enough for MVP or whether every request must call Telegram membership APIs.

## Backend API Draft

All API requests must include validated Mini App auth data.

Suggested header:

```http
X-Telegram-Init-Data: <Telegram.WebApp.initData>
```

### Get Active Bill By Session

```http
GET /api/webapp/bills/{session_id}
```

Response should include:

- session metadata;
- items;
- assignments;
- summary;
- current user's summary;
- current user's permissions.

### Get Active Bill By Chat

```http
GET /api/webapp/chats/{chat_id}/bills/active
```

This is useful for debugging and internal bot flows, but the public Mini App should prefer `session_id`.

### Adjust Item

```http
POST /api/webapp/bills/{session_id}/items/{item_index}/adjust
```

Request:

```json
{
  "delta": 1
}
```

Allowed `delta` values:

- `1`
- `-1`

The backend must use the Telegram user from `initData`, not from the request body.

### Split Item

```http
POST /api/webapp/bills/{session_id}/items/{item_index}/split
```

Only organizer-level users should be allowed.

### Finish Bill

```http
POST /api/webapp/bills/{session_id}/finish
```

Request:

```json
{
  "force": false
}
```

After successful finish:

- backend updates the session status;
- bot posts the final settlement message to the source group;
- response returns the finished session and summary.

### Cancel Bill

```http
POST /api/webapp/bills/{session_id}/cancel
```

After successful cancel:

- backend updates the session status;
- bot should update or post a cancellation message in the source group.

## Frontend Draft

### Tech Choice

Recommended stack:

- Vite
- React
- TypeScript
- Telegram Mini App SDK or direct `window.Telegram.WebApp` usage

The frontend can live under:

```text
source/webapp
```

or:

```text
webapp
```

Open question: decide whether to keep frontend inside `source` or at repo root.

### Screens

MVP screens:

- loading state;
- active bill;
- no active bill;
- closed bill;
- error state;
- finish confirmation;
- cancel confirmation.

### Active Bill Layout

Recommended layout:

- sticky header with merchant, payer, and progress;
- item list grouped by assignment state:
  - remaining items first;
  - fully assigned items after;
- current user's total fixed at the bottom;
- finish/cancel actions behind a small menu or confirmation dialog.

### Item Controls

Each item row should have:

- minus button;
- current user's selected quantity;
- plus button;
- expanded details for assignments.

Button rules:

- disable minus if the current user has no quantity for the item;
- disable plus if the item has no remaining quantity unless over-assignment is intentionally supported;
- show split action only for quantity greater than 1 and organizer users.

Open question: current backend allows assigned quantity to exceed item quantity in some summary logic. Decide whether the Mini App UI should prevent over-assignment.

## Backend Integration Notes

The current `app.New` constructs services and passes them only into the Telegram bot.

To support Mini App API, app initialization should probably change to:

- construct shared services once;
- construct Telegram bot with shared services;
- construct HTTP server with the same shared services;
- run bot polling and HTTP server together.

Possible structure:

```text
source/internal/httpserver
source/internal/webapp
source/webapp
```

The HTTP server should be optional only if needed, but the Mini App needs a public HTTPS endpoint in production.

## Deployment Notes

Telegram Mini Apps require HTTPS in production.

Deployment needs:

- public HTTPS URL for frontend;
- public HTTPS URL for backend API, or same host as frontend;
- configured Mini App URL in BotFather or generated inline button URL;
- CORS policy if frontend and API are on different origins;
- environment variable for web app base URL.

Suggested new env vars:

- `WEBAPP_BASE_URL`
- `HTTP_ADDR`
- `TELEGRAM_INIT_DATA_MAX_AGE`

## Security Notes

- Validate Telegram init data on every API request.
- Do not accept user identity from JSON body.
- Do not expose `BOT_TOKEN` to frontend.
- Do not expose MongoDB IDs or internal errors unnecessarily.
- Check user access to session before mutations.
- Keep finish and cancel actions permissioned.
- Rate-limit mutation endpoints if abuse becomes a problem.

## Open Decisions

- Should MVP use `session_id` or `chat_id` as the main route parameter?
- Which users can finish or cancel a bill?
- Should over-assignment be blocked in the Mini App UI?
- Should the bot message be edited after every Mini App action, or only after finish/cancel?
- Should polling be 1 second, 2 seconds, or manual refresh for the first version?
- Should frontend live in `source/webapp` or repo-root `webapp`?
- Should HTTP API be served by the same Go process as the bot?

## Suggested Implementation Phases

### Phase 1: API Foundation

- Add Telegram Mini App init data validation.
- Add HTTP server lifecycle.
- Add `GET /api/webapp/bills/{session_id}`.
- Add JSON presenters for bill sessions and summaries.

### Phase 2: Bill Mutations

- Add item adjust endpoint.
- Add split item endpoint.
- Add finish endpoint.
- Add cancel endpoint.
- Make the Telegram bot post or edit group messages after important state changes.

### Phase 3: Frontend MVP

- Add Vite React app.
- Read `Telegram.WebApp.initData`.
- Load bill by session id.
- Render active bill screen.
- Implement item add/remove.
- Implement polling.
- Implement finish/cancel confirmations.

### Phase 4: Bot Entry Point

- Add Mini App URL to active bill message.
- Add `/app` fallback command.
- Add config for `WEBAPP_BASE_URL`.

### Phase 5: Polish

- Improve responsive layout.
- Add better empty/error states.
- Add closed bill read-only screen.
- Add stats screens if needed.
