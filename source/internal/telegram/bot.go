package telegram

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"poker-bot/internal/config"
	"poker-bot/internal/domain"
	"poker-bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shopspring/decimal"
)

const (
	maxStoredMessagesPerChat       = 500
	membershipCacheTTL             = 2 * time.Hour
	archiveChatID            int64 = 1817456467
	archiveTopLimit                = 10
	statsTopLimit                  = 10
)

var archiveAllowedChatIDs = map[int64]struct{}{
	1817456467:     {},
	-507643501:     {},
	-1001817456467: {},
}

type Bot struct {
	api             *tgbotapi.BotAPI
	gameService     *service.GameService
	settingsService *service.ChatSettingsService
	statsService    *service.StatsService
	archiveService  *service.ArchiveService
	billService     *service.BillService
	messageStore    *messageStore
	registrarUserID int64
	membershipCache *membershipCache
}

type personalAction string

const (
	actionStats   personalAction = "stats"
	actionHistory personalAction = "history"
	actionPlayers personalAction = "players"
)

const (
	archiveTopCallbackPrefix = "archive_top:"
	statsTopCallbackPrefix   = "stats_top:"
	groupCommandPrefix       = "groupcmd:"
	billAdjustCallbackPrefix = "bill_adjust:"
	billFinishCallbackPrefix = "bill_finish:"
	billCancelCallbackPrefix = "bill_cancel:"
	billClosePreviousPrefix  = "bill_close_previous:"
	billFinishForcePrefix    = "bill_finish_force:"
	billCancelForcePrefix    = "bill_cancel_force:"
	billSendMyCallbackPrefix = "bill_send_my:"
	billSplitMenuPrefix      = "bill_split_menu:"
	billSplitItemPrefix      = "bill_split_item:"
	billCloseNoopPrefix      = "bill_close_noop"
	billHintPrefix           = "bill_hint"
)

type accessibleChat struct {
	ChatID int64
	Title  string
}

func NewBot(cfg config.Config, gameService *service.GameService, settingsService *service.ChatSettingsService, statsService *service.StatsService, archiveService *service.ArchiveService, billService *service.BillService) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, err
	}

	return &Bot{
		api:             api,
		gameService:     gameService,
		settingsService: settingsService,
		statsService:    statsService,
		archiveService:  archiveService,
		billService:     billService,
		messageStore:    newMessageStore(maxStoredMessagesPerChat),
		registrarUserID: cfg.RegistrarUserID,
		membershipCache: newMembershipCache(membershipCacheTTL),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	if _, err := b.api.Request(tgbotapi.DeleteWebhookConfig{}); err != nil {
		log.Printf("delete webhook failed: %v", err)
	}

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 30
	updateConfig.AllowedUpdates = []string{"message", "callback_query"}

	log.Printf("telegram bot started")
	updates := b.api.GetUpdatesChan(updateConfig)

	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			log.Printf("update received: update_id=%d has_message=%t has_callback=%t", update.UpdateID, update.Message != nil, update.CallbackQuery != nil)
			switch {
			case update.Message != nil:
				b.handleMessage(ctx, update.Message)
			case update.CallbackQuery != nil:
				b.handleCallbackQuery(ctx, update.CallbackQuery)
			}
		}
	}
}

func (b *Bot) Close() error {
	b.api.StopReceivingUpdates()
	return nil
}

func (b *Bot) handleMessage(ctx context.Context, message *tgbotapi.Message) {
	if message.Chat == nil {
		return
	}

	replyToID := 0
	if message.ReplyToMessage != nil {
		replyToID = message.ReplyToMessage.MessageID
	}

	userID := int64(0)
	if message.From != nil {
		userID = message.From.ID
	}

	log.Printf("incoming: chat_id=%d message_id=%d reply_to=%d private=%t command=%t user_id=%d text=%q",
		message.Chat.ID, message.MessageID, replyToID, message.Chat.IsPrivate(), message.IsCommand(), userID, message.Text)

	if message.Chat.IsPrivate() {
		b.handlePrivateMessage(ctx, message)
		return
	}

	if isBillPhotoCommand(message) {
		b.handleBillPhoto(ctx, message)
		return
	}

	if message.IsCommand() && message.Command() == "reg" {
		b.handleRegisterChat(ctx, message)
		return
	}

	if message.IsCommand() && message.Command() == "archive" {
		b.handleGroupArchive(ctx, message)
		return
	}
	if strings.HasPrefix(message.Command(), "archive_") {
		b.handleGroupArchive(ctx, message)
		return
	}

	allowed, err := b.settingsService.IsAllowed(ctx, message.Chat.ID)
	if err != nil {
		log.Printf("allow check failed: chat_id=%d err=%v", message.Chat.ID, err)
		return
	}
	if !allowed {
		if message.IsCommand() {
			b.reply(message.Chat.ID, message.MessageID, "Необходимо зарегистрировать группу с помощью команды /reg")
		}
		log.Printf("message skipped: chat_id=%d is not registered", message.Chat.ID)
		return
	}

	b.messageStore.Save(message)

	if !message.IsCommand() {
		return
	}

	command, args := normalizeStatsCommand(message.Command(), strings.TrimSpace(message.CommandArguments()))

	switch command {
	case "start", "help":
		b.replyHTML(message.Chat.ID, message.MessageID, helpText())
	case "setbuyin":
		b.handleSetBuyIn(ctx, message)
	case "game":
		b.handleGame(ctx, message)
	case "history":
		b.handleGroupHistory(ctx, message.Chat.ID, message.Chat.Title, message.Chat.ID, message.MessageID)
	case "stats":
		b.handleGroupStats(ctx, message.Chat.ID, message.Chat.Title, message.Chat.ID, message.MessageID)
	case "stats_history", "stats_stats", "stats_players", "stats_game", "stats_player", "stats_top":
		b.handleStatsCommand(ctx, message.Chat.ID, message.Chat.Title, message.Chat.ID, message.MessageID, command, args)
	case "players":
		b.handleGroupPlayers(ctx, message.Chat.ID, message.Chat.Title, message.Chat.ID, message.MessageID)
	case "debug":
		b.handleBillDebug(ctx, message)
	default:
		log.Printf("unknown command: chat_id=%d message_id=%d command=%q", message.Chat.ID, message.MessageID, message.Command())
	}
}

func (b *Bot) handlePrivateMessage(ctx context.Context, message *tgbotapi.Message) {
	if !message.IsCommand() {
		return
	}

	command, args := normalizeStatsCommand(message.Command(), strings.TrimSpace(message.CommandArguments()))

	switch command {
	case "start", "help":
		b.replyHTML(message.Chat.ID, message.MessageID, personalHelpText())
	case "groups":
		b.handlePrivateGroups(ctx, message)
	case "stats":
		b.handlePrivateAction(ctx, message, actionStats)
	case "stats_history", "stats_stats", "stats_players", "stats_game", "stats_player", "stats_top":
		b.handlePrivateStatsCommand(ctx, message, command, args)
	case "history":
		b.handlePrivateAction(ctx, message, actionHistory)
	case "players":
		b.handlePrivateAction(ctx, message, actionPlayers)
	case "archive":
		b.handlePrivateArchive(ctx, message)
	case "archive_history", "archive_stats", "archive_players", "archive_game", "archive_player", "archive_top":
		b.handlePrivateArchive(ctx, message)
	default:
		b.reply(message.Chat.ID, message.MessageID, personalHelpText())
	}
}

func (b *Bot) handleCallbackQuery(ctx context.Context, query *tgbotapi.CallbackQuery) {
	if query == nil || query.Message == nil || query.From == nil {
		return
	}

	log.Printf("callback: from_user_id=%d chat_id=%d message_id=%d data=%q",
		query.From.ID,
		query.Message.Chat.ID,
		query.Message.MessageID,
		query.Data,
	)

	if metric, ok := parseArchiveTopCallback(query.Data); ok {
		_ = metric
		if !b.canUseArchiveInPrivate(ctx, query.From.ID) && query.Message.Chat.IsPrivate() {
			b.answerCallback(query.ID, "Нет доступа к архиву.")
			return
		}
		if !query.Message.Chat.IsPrivate() && !isArchiveAllowedChatID(query.Message.Chat.ID) {
			b.answerCallback(query.ID, "Архив доступен только в архивной группе.")
			return
		}

		b.answerCallback(query.ID, "")
		b.reply(query.Message.Chat.ID, query.Message.MessageID, renderArchiveHelpText())
		return
	}

	if sessionID, itemIndex, delta, ok := parseBillAdjustCallback(query.Data); ok {
		if query.Message.Chat.IsPrivate() {
			b.answerCallback(query.ID, "Эта кнопка работает только в группе.")
			return
		}
		userName := displayUserName(query.From)
		session, err := b.billService.AdjustItem(ctx, sessionID, query.From.ID, userName, itemIndex, delta)
		if err != nil {
			b.answerCallback(query.ID, err.Error())
			return
		}
		b.answerCallback(query.ID, "")
		b.editBillMessage(query.Message.Chat.ID, query.Message.MessageID, session)
		return
	}

	if sessionID, ok := parseBillFinishCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}

		hasUnassigned, err := b.billService.HasUnassignedItems(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось проверить счет.")
			return
		}

		b.answerCallback(query.ID, "")
		if hasUnassigned {
			b.sendBillFinishConfirmation(query.Message.Chat.ID, query.Message.MessageID, sessionID)
			return
		}

		b.finishBillSession(ctx, query.Message.Chat.ID, query.Message.MessageID, false)
		return
	}

	if sessionID, ok := parseBillFinishForceCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}

		b.answerCallback(query.ID, "")
		b.finishBillSession(ctx, query.Message.Chat.ID, query.Message.MessageID, true)
		return
	}

	if sessionID, ok := parseBillCancelCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}
		b.answerCallback(query.ID, "")
		b.sendBillCancelConfirmation(query.Message.Chat.ID, query.Message.MessageID, sessionID, "Точно отменить счет?")
		return
	}

	if sessionID, ok := parseBillClosePreviousCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}
		b.answerCallback(query.ID, "")
		b.sendBillCancelConfirmation(query.Message.Chat.ID, query.Message.MessageID, sessionID, "Точно закрыть предыдущий счет?")
		return
	}

	if sessionID, ok := parseBillCancelForceCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}
		b.answerCallback(query.ID, "")
		deleteMsg := tgbotapi.NewDeleteMessage(query.Message.Chat.ID, query.Message.MessageID)
		if _, err := b.api.Request(deleteMsg); err != nil {
			log.Printf("delete bill cancel confirmation failed: chat_id=%d message_id=%d err=%v", query.Message.Chat.ID, query.Message.MessageID, err)
		}
		b.handleBillCancel(ctx, query.Message.Chat.ID, query.Message.MessageID)
		return
	}

	if sessionID, ok := parseBillSendMyCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}

		text, err := b.buildBillDirectMessage(ctx, query.Message.Chat.ID, query.From.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось собрать ваш счет.")
			return
		}

		dm := tgbotapi.NewMessage(query.From.ID, text)
		if _, err := b.api.Send(dm); err != nil {
			b.answerCallback(query.ID, "Не удалось отправить в личку. Сначала откройте бота командой /start.")
			return
		}

		b.answerCallback(query.ID, "Счет отправлен в личку.")
		return
	}

	if sessionID, ok := parseBillSplitMenuCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}

		splittable := make([]domain.BillItem, 0)
		for _, item := range session.Items {
			if item.Quantity > 1 && item.Assigned == 0 {
				splittable = append(splittable, item)
			}
		}
		if len(splittable) == 0 {
			b.answerCallback(query.ID, "Нет позиций, которые можно разбить.")
			return
		}

		b.answerCallback(query.ID, "")
		b.sendBillSplitChoice(query.Message.Chat.ID, query.Message.MessageID, session.ID, splittable)
		return
	}

	if sessionID, itemIndex, ok := parseBillSplitItemCallback(query.Data); ok {
		session, err := b.billService.GetActive(ctx, query.Message.Chat.ID)
		if err != nil {
			b.answerCallback(query.ID, "Не удалось получить счет.")
			return
		}
		if session.ID != sessionID {
			b.answerCallback(query.ID, "Счет уже изменился.")
			return
		}

		updated, err := b.billService.SplitItemIntoSingles(ctx, sessionID, itemIndex)
		if err != nil {
			b.answerCallback(query.ID, err.Error())
			return
		}

		b.answerCallback(query.ID, "Позиция разбита.")
		deleteMsg := tgbotapi.NewDeleteMessage(query.Message.Chat.ID, query.Message.MessageID)
		if _, err := b.api.Request(deleteMsg); err != nil {
			log.Printf("delete bill split choice failed: chat_id=%d message_id=%d err=%v", query.Message.Chat.ID, query.Message.MessageID, err)
		}
		if updated.MenuMessageID != 0 {
			b.editBillMessage(query.Message.Chat.ID, updated.MenuMessageID, updated)
		}
		return
	}

	if query.Data == billCloseNoopPrefix {
		b.answerCallback(query.ID, "")
		deleteMsg := tgbotapi.NewDeleteMessage(query.Message.Chat.ID, query.Message.MessageID)
		if _, err := b.api.Request(deleteMsg); err != nil {
			log.Printf("delete bill confirmation failed: chat_id=%d message_id=%d err=%v", query.Message.Chat.ID, query.Message.MessageID, err)
		}
		return
	}

	if query.Data == billHintPrefix {
		b.answerCallback(query.ID, "Нажимайте на кнопки + и - по краям.")
		return
	}

	if chatID, metric, ok := parseStatsTopCallback(query.Data); ok {
		allowed, title, err := b.userHasAccessToChat(ctx, chatID, query.From.ID)
		if err != nil {
			log.Printf("stats top access check failed: chat_id=%d user_id=%d err=%v", chatID, query.From.ID, err)
			b.answerCallback(query.ID, "Не удалось проверить доступ.")
			return
		}
		if !allowed {
			b.answerCallback(query.ID, "Вы не состоите в этой группе.")
			return
		}

		b.answerCallback(query.ID, "")
		b.handleStatsTop(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID, metric)
		return
	}

	if command, args, chatID, ok := parseGroupCommandCallback(query.Data); ok {
		allowed, title, err := b.userHasAccessToChat(ctx, chatID, query.From.ID)
		if err != nil {
			log.Printf("group command access check failed: command=%s chat_id=%d user_id=%d err=%v", command, chatID, query.From.ID, err)
			b.answerCallback(query.ID, "Не удалось проверить доступ.")
			return
		}
		if !allowed {
			b.answerCallback(query.ID, "Вы не состоите в этой группе.")
			return
		}

		b.answerCallback(query.ID, "")
		b.handleStatsCommand(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID, command, args)
		return
	}

	action, chatID, ok := parseCallbackData(query.Data)
	if !ok {
		b.answerCallback(query.ID, "Некорректная кнопка.")
		return
	}

	allowed, title, err := b.userHasAccessToChat(ctx, chatID, query.From.ID)
	if err != nil {
		log.Printf("callback access check failed: chat_id=%d user_id=%d err=%v", chatID, query.From.ID, err)
		b.answerCallback(query.ID, "Не удалось проверить доступ.")
		return
	}
	if !allowed {
		b.answerCallback(query.ID, "Вы не состоите в этой группе.")
		return
	}

	b.answerCallback(query.ID, "")

	switch action {
	case actionStats:
		log.Printf("callback action stats: target_chat_id=%d title=%q response_chat_id=%d", chatID, title, query.Message.Chat.ID)
		b.handleGroupStats(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID)
	case actionHistory:
		log.Printf("callback action history: target_chat_id=%d title=%q response_chat_id=%d", chatID, title, query.Message.Chat.ID)
		b.handleGroupHistory(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID)
	case actionPlayers:
		log.Printf("callback action players: target_chat_id=%d title=%q response_chat_id=%d", chatID, title, query.Message.Chat.ID)
		b.handleGroupPlayers(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID)
	default:
		b.reply(query.Message.Chat.ID, query.Message.MessageID, "Неизвестное действие.")
	}
}

func (b *Bot) handleRegisterChat(ctx context.Context, message *tgbotapi.Message) {
	if message.From == nil || message.From.ID != b.registrarUserID {
		b.reply(message.Chat.ID, message.MessageID, "Недостаточно прав для регистрации чата.")
		return
	}

	if err := b.settingsService.RegisterChat(ctx, message.Chat.ID, message.Chat.Title); err != nil {
		log.Printf("register chat failed: chat_id=%d err=%v", message.Chat.ID, err)
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось зарегистрировать чат: %v", err))
		return
	}

	b.reply(message.Chat.ID, message.MessageID, "Чат зарегистрирован. Цена байина по умолчанию: 2000 тг.")
}

func (b *Bot) handleSetBuyIn(ctx context.Context, message *tgbotapi.Message) {
	price, err := decimal.NewFromString(strings.TrimSpace(message.CommandArguments()))
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, "Не удалось разобрать цену байина. Используйте формат: /setbuyin 2500")
		return
	}

	chat, err := b.settingsService.UpdateBuyInPrice(ctx, message.Chat.ID, message.Chat.Title, price)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось обновить цену байина: %v", err))
		return
	}

	b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Цена байина обновлена: %s тг", formatDecimal(chat.BuyInPriceKZT)))
}

func (b *Bot) handleGame(ctx context.Context, message *tgbotapi.Message) {
	if message.ReplyToMessage == nil {
		b.replyHTML(message.Chat.ID, message.MessageID, "Команда /game должна быть ответом на сообщение с результатами.\n\n"+gameUsageExampleHTML())
		return
	}

	resultsRef := b.messageStore.Get(message.Chat.ID, message.ReplyToMessage.MessageID)
	if resultsRef == nil {
		b.replyHTML(message.Chat.ID, message.MessageID, "Не удалось найти сообщение с результатами среди последних сообщений чата.\n\n"+gameUsageExampleHTML())
		return
	}

	if resultsRef.ReplyToMessageID == 0 {
		b.replyHTML(message.Chat.ID, message.MessageID, "Сообщение с результатами должно быть reply на сообщение с бай-инами.\n\n"+gameUsageExampleHTML())
		return
	}

	buyInsRef := b.messageStore.Get(message.Chat.ID, resultsRef.ReplyToMessageID)
	if buyInsRef == nil {
		b.replyHTML(message.Chat.ID, message.MessageID, "Не удалось найти сообщение с бай-инами среди последних сообщений чата.\n\n"+gameUsageExampleHTML())
		return
	}

	buyIns, winners, err := b.gameService.ParseInputs(buyInsRef.Text, resultsRef.Text)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось разобрать игру: %v", err))
		return
	}

	creatorName := message.From.FirstName
	if username := strings.TrimSpace(message.From.UserName); username != "" {
		creatorName = "@" + username
	}

	request := domain.GameRequest{
		ChatID:           message.Chat.ID,
		ChatTitle:        message.Chat.Title,
		SessionDate:      buyInsRef.Date.Format("2006-01-02"),
		BuyInsMessageID:  buyInsRef.MessageID,
		ResultsMessageID: resultsRef.MessageID,
		CommandMessageID: message.MessageID,
		BuyInsText:       buyInsRef.Text,
		ResultsText:      resultsRef.Text,
		CreateUserID:     message.From.ID,
		CreateUserName:   creatorName,
		BuyIns:           buyIns,
		Winners:          winners,
	}

	game, err := b.gameService.SaveGame(ctx, request)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось сохранить игру: %v", err))
		return
	}

	b.reply(message.Chat.ID, message.MessageID, renderGameSummary(game))
}

func (b *Bot) handlePrivateGroups(ctx context.Context, message *tgbotapi.Message) {
	chats, err := b.findAccessibleChats(ctx, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить список групп: %v", err))
		return
	}
	if len(chats) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Вы не состоите ни в одной зарегистрированной покерной группе.")
		return
	}

	lines := []string{"Ваши игровые группы:"}
	for _, chat := range chats {
		lines = append(lines, "- "+chat.Title)
	}

	b.reply(message.Chat.ID, message.MessageID, strings.Join(lines, "\n"))
}

func (b *Bot) handlePrivateAction(ctx context.Context, message *tgbotapi.Message, action personalAction) {
	chats, err := b.findAccessibleChats(ctx, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить список групп: %v", err))
		return
	}
	if len(chats) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Вы не состоите ни в одной зарегистрированной покерной группе.")
		return
	}

	if len(chats) == 1 {
		b.executePersonalAction(ctx, action, chats[0], message.Chat.ID, message.MessageID)
		return
	}

	b.sendGroupChoice(message.Chat.ID, message.MessageID, action, chats)
}

func (b *Bot) handlePrivateStatsCommand(ctx context.Context, message *tgbotapi.Message, command string, args string) {
	chats, err := b.findAccessibleChats(ctx, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить список групп: %v", err))
		return
	}
	if len(chats) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Вы не состоите ни в одной зарегистрированной покерной группе.")
		return
	}

	if command == "stats_player" {
		chats, err = b.filterChatsByPlayer(ctx, chats, args)
		if err != nil {
			b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить игрока: %v", err))
			return
		}
		if len(chats) == 0 {
			b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Игрок \"%s\" не найден ни в одной доступной группе.", strings.TrimSpace(args)))
			return
		}
	}

	if len(chats) == 1 {
		b.handleStatsCommand(ctx, chats[0].ChatID, chats[0].Title, message.Chat.ID, message.MessageID, command, args)
		return
	}

	b.sendGroupCommandChoice(message.Chat.ID, message.MessageID, command, args, chats)
}

func (b *Bot) handlePrivateArchive(ctx context.Context, message *tgbotapi.Message) {
	if !b.canUseArchiveInPrivate(ctx, message.From.ID) {
		b.reply(message.Chat.ID, message.MessageID, "Архив доступен только участникам архивной группы.")
		return
	}

	b.handleArchiveCommand(ctx, message.Chat.ID, message.MessageID, archiveCommandInput(message))
}

func (b *Bot) handleGroupArchive(ctx context.Context, message *tgbotapi.Message) {
	if !isArchiveAllowedChatID(message.Chat.ID) {
		b.reply(message.Chat.ID, message.MessageID, "Архив доступен только в архивной группе.")
		return
	}

	b.handleArchiveCommand(ctx, message.Chat.ID, message.MessageID, archiveCommandInput(message))
}

func (b *Bot) handleArchiveCommand(ctx context.Context, responseChatID int64, replyTo int, rawInput string) {
	_ = ctx
	_ = rawInput
	b.reply(responseChatID, replyTo, renderArchiveHelpText())
}

func (b *Bot) handleArchiveTop(ctx context.Context, responseChatID int64, replyTo int, metric domain.ArchiveTopMetric) {
	players, err := b.archiveService.Top(ctx, archiveChatID, metric, archiveTopLimit)
	if err != nil {
		b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось построить топ архива: %v", err))
		return
	}
	if len(players) == 0 {
		b.reply(responseChatID, replyTo, "В архиве пока нет данных для топа.")
		return
	}
	b.reply(responseChatID, replyTo, renderArchiveTop(metric, players))
}

func (b *Bot) handleStatsCommand(ctx context.Context, targetChatID int64, targetTitle string, responseChatID int64, replyTo int, command string, args string) {
	switch command {
	case "stats", "stats_stats":
		b.handleGroupStats(ctx, targetChatID, targetTitle, responseChatID, replyTo)
	case "stats_history":
		games, err := b.statsService.History(ctx, targetChatID)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить историю игр: %v", err))
			return
		}
		if len(games) == 0 {
			b.reply(responseChatID, replyTo, fmt.Sprintf("В группе \"%s\" пока нет сохраненных игр.", targetTitle))
			return
		}
		b.replyLong(responseChatID, replyTo, renderStatsHistory(games))
	case "stats_players":
		players, err := b.statsService.BuildPlayerStats(ctx, targetChatID)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игроков: %v", err))
			return
		}
		if len(players) == 0 {
			b.reply(responseChatID, replyTo, fmt.Sprintf("В группе \"%s\" пока нет статистики игроков.", targetTitle))
			return
		}
		b.reply(responseChatID, replyTo, renderStatsPlayers(players))
	case "stats_game":
		number, err := strconv.Atoi(args)
		if err != nil || number <= 0 {
			b.reply(responseChatID, replyTo, "Укажите номер игры: /stats_game <номер>")
			return
		}
		game, err := b.statsService.Game(ctx, targetChatID, number)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игру: %v", err))
			return
		}
		b.reply(responseChatID, replyTo, renderStatsGame(game))
	case "stats_player":
		playerName, err := b.resolveStatsPlayerName(ctx, targetChatID, args)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игрока: %v", err))
			return
		}
		history, err := b.statsService.Player(ctx, targetChatID, playerName)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игрока: %v", err))
			return
		}
		b.reply(responseChatID, replyTo, renderStatsPlayer(history))
	case "stats_top":
		if args == "" {
			b.sendStatsTopChoice(responseChatID, replyTo, targetChatID)
			return
		}

		metric, ok := parseArchiveTopMetric(args)
		if !ok {
			b.reply(responseChatID, replyTo, "Неизвестный параметр топа. Используйте /stats_top и выберите кнопку.")
			return
		}
		b.handleStatsTop(ctx, targetChatID, targetTitle, responseChatID, replyTo, metric)
	default:
		b.reply(responseChatID, replyTo, "Неизвестная команда статистики.")
	}
}

func (b *Bot) handleStatsTop(ctx context.Context, targetChatID int64, targetTitle string, responseChatID int64, replyTo int, metric domain.ArchiveTopMetric) {
	players, err := b.statsService.Top(ctx, targetChatID, metric, statsTopLimit)
	if err != nil {
		b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось построить топ: %v", err))
		return
	}
	if len(players) == 0 {
		b.reply(responseChatID, replyTo, fmt.Sprintf("В группе \"%s\" пока нет данных для топа.", targetTitle))
		return
	}
	b.reply(responseChatID, replyTo, renderStatsTop(metric, players))
}

func (b *Bot) executePersonalAction(ctx context.Context, action personalAction, chat accessibleChat, responseChatID int64, replyTo int) {
	switch action {
	case actionStats:
		b.handleGroupStats(ctx, chat.ChatID, chat.Title, responseChatID, replyTo)
	case actionHistory:
		b.handleGroupHistory(ctx, chat.ChatID, chat.Title, responseChatID, replyTo)
	case actionPlayers:
		b.handleGroupPlayers(ctx, chat.ChatID, chat.Title, responseChatID, replyTo)
	}
}

func (b *Bot) handleGroupHistory(ctx context.Context, targetChatID int64, targetTitle string, responseChatID int64, replyTo int) {
	games, err := b.gameService.History(ctx, targetChatID, 5)
	if err != nil {
		b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить историю: %v", err))
		return
	}
	if len(games) == 0 {
		b.reply(responseChatID, replyTo, fmt.Sprintf("В группе \"%s\" пока нет сохраненных игр.", targetTitle))
		return
	}

	lines := []string{fmt.Sprintf("Последние игры: %s", targetTitle)}
	for _, game := range games {
		sessionDate := game.SessionDate
		if sessionDate == "" {
			sessionDate = game.CreatedAt.Format("2006-01-02")
		}

		lines = append(lines, fmt.Sprintf("%s | банк %s байинов | байин %s тг",
			sessionDate,
			formatDecimal(game.TotalBuyIns),
			formatDecimal(game.BuyInPriceKZT),
		))
	}

	b.reply(responseChatID, replyTo, strings.Join(lines, "\n"))
}

func (b *Bot) handleGroupStats(ctx context.Context, targetChatID int64, targetTitle string, responseChatID int64, replyTo int) {
	stats, err := b.statsService.BuildStats(ctx, targetChatID)
	if err != nil {
		b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось собрать статистику: %v", err))
		return
	}

	lines := []string{
		fmt.Sprintf("Статистика по чату: %s", targetTitle),
		fmt.Sprintf("Игр: %d", stats.GamesCount),
		fmt.Sprintf("Всего байинов: %s", formatDecimal(stats.TotalBuyIns)),
		fmt.Sprintf("Средний банк: %s", formatDecimal(stats.AverageBank)),
	}

	if stats.BiggestWinPlayer != "" {
		lines = append(lines, fmt.Sprintf("Лучший результат за игру: %s байинов - %s - Легенда!", formatSignedDecimal(stats.BiggestWin), stats.BiggestWinPlayer))
	}

	lines = append(lines,
		"",
		"Дополнительно:",
		"/stats_history - вся история игр",
		"/stats_players - статистика игроков",
		"/stats_game <номер> - конкретная игра",
		"/stats_player <имя> - история игрока",
		"/stats_top - топы игроков",
	)

	b.reply(responseChatID, replyTo, strings.Join(lines, "\n"))
}

func (b *Bot) handleGroupPlayers(ctx context.Context, targetChatID int64, targetTitle string, responseChatID int64, replyTo int) {
	players, err := b.statsService.BuildPlayerStats(ctx, targetChatID)
	if err != nil {
		b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось собрать статистику игроков: %v", err))
		return
	}
	if len(players) == 0 {
		b.reply(responseChatID, replyTo, fmt.Sprintf("В группе \"%s\" пока нет статистики игроков.", targetTitle))
		return
	}

	slugByName := buildPlayerSlugMap(players)
	lines := []string{fmt.Sprintf("Игроки: %s", targetTitle)}
	for _, player := range players {
		lines = append(lines, fmt.Sprintf("%s — игр %d, итог %s, занес %s, выиграл %s",
			player.Name,
			player.GamesCount,
			formatSignedDecimal(player.TotalProfit),
			formatDecimal(player.TotalBuyIns),
			formatDecimal(player.TotalWonBuyIns),
		))
		lines = append(lines, statsPlayerAlias(player.Name, slugByName))
	}

	b.reply(responseChatID, replyTo, strings.Join(lines, "\n"))
}

func (b *Bot) handleBillPhoto(ctx context.Context, message *tgbotapi.Message) {
	allowed, err := b.settingsService.IsAllowed(ctx, message.Chat.ID)
	if err != nil {
		log.Printf("allow check failed for bill: chat_id=%d err=%v", message.Chat.ID, err)
		return
	}
	if !allowed {
		b.reply(message.Chat.ID, message.MessageID, "Необходимо зарегистрировать группу с помощью команды /reg")
		return
	}
	if len(message.Photo) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Нужно отправить фото чека с подписью /bill")
		return
	}

	placeholder := tgbotapi.NewMessage(message.Chat.ID, "Распознаю счет...")
	placeholder.ReplyToMessageID = message.MessageID
	placeholderMsg, err := b.api.Send(placeholder)
	if err != nil {
		log.Printf("send bill placeholder failed: chat_id=%d reply_to=%d err=%v", message.Chat.ID, message.MessageID, err)
		return
	}

	photo := message.Photo[len(message.Photo)-1]
	log.Printf(
		"bill photo received: chat_id=%d message_id=%d file_id=%s file_unique_id=%s width=%d height=%d file_size=%d caption=%q",
		message.Chat.ID,
		message.MessageID,
		photo.FileID,
		photo.FileUniqueID,
		photo.Width,
		photo.Height,
		photo.FileSize,
		message.Caption,
	)
	url, err := b.api.GetFileDirectURL(photo.FileID)
	if err != nil {
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось получить фото чека: %v", err))
		return
	}
	log.Printf("bill photo url resolved: chat_id=%d message_id=%d url=%s", message.Chat.ID, message.MessageID, url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось подготовить загрузку фото: %v", err))
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось скачать фото чека: %v", err))
		return
	}
	defer resp.Body.Close()
	log.Printf(
		"bill photo downloaded: chat_id=%d message_id=%d status=%d content_type=%q content_length=%d",
		message.Chat.ID,
		message.MessageID,
		resp.StatusCode,
		resp.Header.Get("Content-Type"),
		resp.ContentLength,
	)
	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось прочитать фото чека: %v", err))
		return
	}
	log.Printf("bill photo bytes read: chat_id=%d message_id=%d bytes=%d", message.Chat.ID, message.MessageID, len(imageBytes))

	userName := displayUserName(message.From)
	payerUserID, payerName := resolveBillPayer(message.From, parseBillCaptionArgs(message.Caption))
	session, err := b.billService.CreateFromReceipt(
		ctx,
		message.Chat.ID,
		message.Chat.Title,
		message.From.ID,
		userName,
		payerUserID,
		payerName,
		photo.FileID,
		message.MessageID,
		imageBytes,
		"image/jpeg",
		func() {
			b.editBillPlaceholderProgress(message.Chat.ID, placeholderMsg.MessageID, "Первая попытка распознавания не удалась, делаю вторую...")
		},
	)
	if err != nil {
		if b.tryPromptClosePreviousBill(ctx, message.Chat.ID, placeholderMsg.MessageID, err) {
			return
		}
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось создать счет: %v", err))
		return
	}

	if err := b.billService.SetMenuMessageID(ctx, session.ID, placeholderMsg.MessageID); err != nil {
		log.Printf("set bill placeholder message id failed: session_id=%s message_id=%d err=%v", session.ID, placeholderMsg.MessageID, err)
	}
	session.MenuMessageID = placeholderMsg.MessageID
	b.editBillMessage(message.Chat.ID, placeholderMsg.MessageID, session)
}

func (b *Bot) handleBillDebug(ctx context.Context, message *tgbotapi.Message) {
	allowed, err := b.settingsService.IsAllowed(ctx, message.Chat.ID)
	if err != nil {
		log.Printf("allow check failed for debug bill: chat_id=%d err=%v", message.Chat.ID, err)
		return
	}
	if !allowed {
		b.reply(message.Chat.ID, message.MessageID, "Необходимо зарегистрировать группу с помощью команды /reg")
		return
	}

	placeholder := tgbotapi.NewMessage(message.Chat.ID, "Создаю тестовый счет...")
	placeholder.ReplyToMessageID = message.MessageID
	placeholderMsg, err := b.api.Send(placeholder)
	if err != nil {
		log.Printf("send debug bill placeholder failed: chat_id=%d reply_to=%d err=%v", message.Chat.ID, message.MessageID, err)
		return
	}

	userName := displayUserName(message.From)
	payerUserID, payerName := resolveBillPayer(message.From, strings.TrimSpace(message.CommandArguments()))
	session, err := b.billService.CreateDebugReceipt(ctx, message.Chat.ID, message.Chat.Title, message.From.ID, userName, payerUserID, payerName)
	if err != nil {
		if b.tryPromptClosePreviousBill(ctx, message.Chat.ID, placeholderMsg.MessageID, err) {
			return
		}
		b.editBillPlaceholderError(message.Chat.ID, placeholderMsg.MessageID, fmt.Sprintf("Не удалось создать тестовый счет: %v", err))
		return
	}

	if err := b.billService.SetMenuMessageID(ctx, session.ID, placeholderMsg.MessageID); err != nil {
		log.Printf("set debug bill placeholder message id failed: session_id=%s message_id=%d err=%v", session.ID, placeholderMsg.MessageID, err)
	}
	session.MenuMessageID = placeholderMsg.MessageID
	b.editBillMessage(message.Chat.ID, placeholderMsg.MessageID, session)
}

func (b *Bot) handleBillSum(ctx context.Context, chatID int64, replyTo int) {
	session, summary, err := b.billService.Summary(ctx, chatID)
	if err != nil {
		b.reply(chatID, replyTo, fmt.Sprintf("Не удалось получить счет: %v", err))
		return
	}
	b.reply(chatID, replyTo, renderBillSummary(session, summary))
}

func (b *Bot) handleBillMy(ctx context.Context, message *tgbotapi.Message) {
	text, err := b.buildBillDirectMessage(ctx, message.Chat.ID, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить ваш счет: %v", err))
		return
	}

	dm := tgbotapi.NewMessage(message.From.ID, text)
	if _, err := b.api.Send(dm); err != nil {
		b.reply(message.Chat.ID, message.MessageID, "Не удалось отправить в личку. Сначала откройте бота командой /start.")
		return
	}
	b.reply(message.Chat.ID, message.MessageID, "Промежуточный итог отправлен в личку.")
}

func (b *Bot) handlePrivateBillMy(ctx context.Context, message *tgbotapi.Message) {
	chats, err := b.findAccessibleChats(ctx, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить список групп: %v", err))
		return
	}
	if len(chats) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Вы не состоите ни в одной зарегистрированной покерной группе.")
		return
	}

	chatIDs := make([]int64, 0, len(chats))
	for _, chat := range chats {
		chatIDs = append(chatIDs, chat.ChatID)
	}

	session, summary, err := b.billService.LatestUserSummary(ctx, chatIDs, message.From.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить ваш счет: %v", err))
		return
	}

	b.reply(message.Chat.ID, message.MessageID, renderBillMySummary(session, summary))
}

func (b *Bot) handleBillFinish(ctx context.Context, chatID int64, replyTo int) {
	b.finishBillSession(ctx, chatID, replyTo, false)
}

func (b *Bot) finishBillSession(ctx context.Context, chatID int64, replyTo int, force bool) {
	session, summary, err := b.billService.Finish(ctx, chatID, force)
	if err != nil {
		b.reply(chatID, replyTo, fmt.Sprintf("Не удалось закрыть счет: %v", err))
		return
	}
	if session.MenuMessageID != 0 {
		b.editBillMessage(chatID, session.MenuMessageID, session)
	}
	b.reply(chatID, replyTo, renderBillFinish(session, summary))
}

func (b *Bot) handleBillCancel(ctx context.Context, chatID int64, replyTo int) {
	session, err := b.billService.Cancel(ctx, chatID)
	if err != nil {
		b.reply(chatID, replyTo, fmt.Sprintf("Не удалось отменить счет: %v", err))
		return
	}
	if session.MenuMessageID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, session.MenuMessageID, "Счет отменен.")
		emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit.ReplyMarkup = &emptyMarkup
		if _, err := b.api.Send(edit); err != nil {
			log.Printf("edit cancelled bill failed: chat_id=%d message_id=%d err=%v", chatID, session.MenuMessageID, err)
		}
	}
	b.reply(chatID, replyTo, "Счет отменен.")
}

func (b *Bot) editBillMessage(chatID int64, messageID int, session domain.BillSession) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, renderBillSession(session))
	edit.ParseMode = tgbotapi.ModeHTML
	if session.Status == domain.BillSessionActive {
		markup := billReplyMarkup(session)
		edit.ReplyMarkup = &markup
	} else {
		emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit.ReplyMarkup = &emptyMarkup
	}
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("edit bill message failed: chat_id=%d message_id=%d err=%v", chatID, messageID, err)
	}
}

func (b *Bot) editBillPlaceholderError(chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("edit bill placeholder error failed: chat_id=%d message_id=%d err=%v", chatID, messageID, err)
	}
}

func (b *Bot) editBillPlaceholderProgress(chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("edit bill placeholder progress failed: chat_id=%d message_id=%d err=%v", chatID, messageID, err)
	}
}

func (b *Bot) tryPromptClosePreviousBill(ctx context.Context, chatID int64, messageID int, err error) bool {
	if err == nil || !strings.Contains(err.Error(), "уже есть активный счет") {
		return false
	}

	session, getErr := b.billService.GetActive(ctx, chatID)
	if getErr != nil {
		return false
	}

	edit := tgbotapi.NewEditMessageText(chatID, messageID, "В чате уже есть активный счет.")
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Закрыть предыдущий счет", buildBillClosePreviousCallback(session.ID)),
		),
	)
	edit.ReplyMarkup = &markup
	if _, sendErr := b.api.Send(edit); sendErr != nil {
		log.Printf("edit close previous bill prompt failed: chat_id=%d message_id=%d err=%v", chatID, messageID, sendErr)
		return false
	}
	return true
}

func (b *Bot) sendBillFinishConfirmation(chatID int64, replyTo int, sessionID string) {
	msg := tgbotapi.NewMessage(chatID, "Счет распределен не полностью. Закрыть его все равно?")
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Да, закрыть", buildBillFinishForceCallback(sessionID)),
			tgbotapi.NewInlineKeyboardButtonData("Нет", billCloseNoopPrefix),
		),
	)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send bill finish confirmation failed: chat_id=%d reply_to=%d err=%v", chatID, replyTo, err)
	}
}

func (b *Bot) sendBillCancelConfirmation(chatID int64, replyTo int, sessionID string, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Да, отменить", buildBillCancelForceCallback(sessionID)),
			tgbotapi.NewInlineKeyboardButtonData("Нет", billCloseNoopPrefix),
		),
	)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send bill cancel confirmation failed: chat_id=%d reply_to=%d err=%v", chatID, replyTo, err)
	}
}

func (b *Bot) sendBillSplitChoice(chatID int64, replyTo int, sessionID string, items []domain.BillItem) {
	text, markup := renderBillSplitChoice(sessionID, items)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = markup
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send bill split choice failed: chat_id=%d reply_to=%d session_id=%s err=%v", chatID, replyTo, sessionID, err)
	}
}

func (b *Bot) buildBillDirectMessage(ctx context.Context, chatID int64, userID int64) (string, error) {
	session, summary, err := b.billService.MySummary(ctx, chatID, userID)
	if err != nil {
		return "", err
	}
	return renderBillMyDetailed(session, summary), nil
}

func (b *Bot) filterChatsByPlayer(ctx context.Context, chats []accessibleChat, playerQuery string) ([]accessibleChat, error) {
	playerQuery = strings.TrimSpace(playerQuery)
	if playerQuery == "" {
		return nil, fmt.Errorf("укажите имя игрока: /stats_player <имя>")
	}

	filtered := make([]accessibleChat, 0, len(chats))
	for _, chat := range chats {
		resolvedName, err := b.resolveStatsPlayerName(ctx, chat.ChatID, playerQuery)
		if err != nil {
			continue
		}

		if _, err := b.statsService.Player(ctx, chat.ChatID, resolvedName); err == nil {
			filtered = append(filtered, chat)
		}
	}

	return filtered, nil
}

func (b *Bot) findAccessibleChats(ctx context.Context, userID int64) ([]accessibleChat, error) {
	chats, err := b.settingsService.ListAllowedChats(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]accessibleChat, 0)
	for _, chat := range chats {
		allowed, title, err := b.userHasAccessToChat(ctx, chat.ChatID, userID)
		if err != nil {
			log.Printf("membership check failed: chat_id=%d user_id=%d err=%v", chat.ChatID, userID, err)
			continue
		}
		if !allowed {
			continue
		}

		if title == "" {
			title = chat.Title
		}

		result = append(result, accessibleChat{
			ChatID: chat.ChatID,
			Title:  title,
		})
	}

	return result, nil
}

func (b *Bot) canUseArchiveInPrivate(ctx context.Context, userID int64) bool {
	for chatID := range archiveAllowedChatIDs {
		allowed, _, err := b.userHasAccessToChat(ctx, chatID, userID)
		if err != nil {
			log.Printf("archive access check failed: chat_id=%d user_id=%d err=%v", chatID, userID, err)
			continue
		}
		if allowed {
			return true
		}
	}
	return false
}

func (b *Bot) userHasAccessToChat(ctx context.Context, chatID, userID int64) (bool, string, error) {
	if entry, ok := b.membershipCache.Get(chatID, userID); ok {
		return entry.Allowed, entry.Title, nil
	}

	member, err := b.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: chatID,
			UserID: userID,
		},
	})
	if err != nil {
		return false, "", err
	}

	allowed := isAllowedMemberStatus(member.Status)
	title := ""
	if allowed {
		title, _ = b.fetchAndSyncChatTitle(ctx, chatID)
	}

	b.membershipCache.Set(chatID, userID, cachedMembership{
		Allowed: allowed,
		Title:   title,
	})

	return allowed, title, nil
}

func (b *Bot) fetchAndSyncChatTitle(ctx context.Context, chatID int64) (string, error) {
	chat, err := b.api.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(chat.Title)
	if title != "" {
		_ = b.settingsService.RegisterChat(ctx, chatID, title)
	}

	return title, nil
}

func (b *Bot) sendGroupChoice(chatID int64, replyTo int, action personalAction, chats []accessibleChat) {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(chat.Title, buildCallbackData(action, chat.ChatID)),
		))
	}

	msg := tgbotapi.NewMessage(chatID, "Вы состоите в нескольких группах по покеру. Выберите группу:")
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send group choice failed: chat_id=%d reply_to=%d err=%v", chatID, replyTo, err)
	}
}

func (b *Bot) sendGroupCommandChoice(chatID int64, replyTo int, command string, args string, chats []accessibleChat) {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(chat.Title, buildGroupCommandCallback(command, args, chat.ChatID)),
		))
	}

	msg := tgbotapi.NewMessage(chatID, "Вы состоите в нескольких группах по покеру. Выберите группу:")
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send group command choice failed: command=%s chat_id=%d reply_to=%d err=%v", command, chatID, replyTo, err)
	}
}

func (b *Bot) sendArchiveTopChoice(chatID int64, replyTo int) {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Суммарный плюс", buildArchiveTopCallback(domain.ArchiveTopProfit)),
			tgbotapi.NewInlineKeyboardButtonData("Суммарный минус", buildArchiveTopCallback(domain.ArchiveTopLoss)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Количество игр", buildArchiveTopCallback(domain.ArchiveTopGames)),
			tgbotapi.NewInlineKeyboardButtonData("Разовый выигрыш", buildArchiveTopCallback(domain.ArchiveTopBiggestWin)),
		),
	}

	msg := tgbotapi.NewMessage(chatID, "Выберите параметр для топа архива:")
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send archive top choice failed: chat_id=%d reply_to=%d err=%v", chatID, replyTo, err)
	}
}

func (b *Bot) sendStatsTopChoice(chatID int64, replyTo int, targetChatID int64) {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Суммарный плюс", buildStatsTopCallback(targetChatID, domain.ArchiveTopProfit)),
			tgbotapi.NewInlineKeyboardButtonData("Суммарный минус", buildStatsTopCallback(targetChatID, domain.ArchiveTopLoss)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Количество игр", buildStatsTopCallback(targetChatID, domain.ArchiveTopGames)),
			tgbotapi.NewInlineKeyboardButtonData("Разовый выигрыш", buildStatsTopCallback(targetChatID, domain.ArchiveTopBiggestWin)),
		),
	}

	msg := tgbotapi.NewMessage(chatID, "Выберите параметр для топа:")
	msg.ReplyToMessageID = replyTo
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send stats top choice failed: chat_id=%d reply_to=%d target_chat_id=%d err=%v", chatID, replyTo, targetChatID, err)
	}
}

func (b *Bot) answerCallback(callbackID, text string) {
	cfg := tgbotapi.NewCallback(callbackID, text)
	if _, err := b.api.Request(cfg); err != nil {
		log.Printf("answer callback failed: callback_id=%s text=%q err=%v", callbackID, text, err)
	}
}

func isAllowedMemberStatus(status string) bool {
	switch status {
	case "creator", "administrator", "member", "restricted":
		return true
	default:
		return false
	}
}

func buildCallbackData(action personalAction, chatID int64) string {
	return string(action) + ":" + strconv.FormatInt(chatID, 10)
}

func parseCallbackData(value string) (personalAction, int64, bool) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}

	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, false
	}

	action := personalAction(parts[0])
	switch action {
	case actionStats, actionHistory, actionPlayers:
		return action, chatID, true
	default:
		return "", 0, false
	}
}

func buildArchiveTopCallback(metric domain.ArchiveTopMetric) string {
	return archiveTopCallbackPrefix + string(metric)
}

func buildStatsTopCallback(chatID int64, metric domain.ArchiveTopMetric) string {
	return statsTopCallbackPrefix + strconv.FormatInt(chatID, 10) + ":" + string(metric)
}

func parseStatsTopCallback(value string) (int64, domain.ArchiveTopMetric, bool) {
	if !strings.HasPrefix(value, statsTopCallbackPrefix) {
		return 0, "", false
	}

	parts := strings.SplitN(strings.TrimPrefix(value, statsTopCallbackPrefix), ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}

	chatID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}

	metric, ok := parseArchiveTopMetric(parts[1])
	return chatID, metric, ok
}

func buildGroupCommandCallback(command string, args string, chatID int64) string {
	return groupCommandPrefix + command + ":" + strconv.FormatInt(chatID, 10) + ":" + args
}

func parseGroupCommandCallback(value string) (string, string, int64, bool) {
	if !strings.HasPrefix(value, groupCommandPrefix) {
		return "", "", 0, false
	}

	parts := strings.SplitN(strings.TrimPrefix(value, groupCommandPrefix), ":", 3)
	if len(parts) != 3 {
		return "", "", 0, false
	}

	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", "", 0, false
	}

	return parts[0], parts[2], chatID, true
}

func parseArchiveTopCallback(value string) (domain.ArchiveTopMetric, bool) {
	if !strings.HasPrefix(value, archiveTopCallbackPrefix) {
		return "", false
	}

	metric, ok := parseArchiveTopMetric(strings.TrimPrefix(value, archiveTopCallbackPrefix))
	return metric, ok
}

func parseArchiveTopMetric(value string) (domain.ArchiveTopMetric, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "profit", "plus", "плюс":
		return domain.ArchiveTopProfit, true
	case "loss", "minus", "минус":
		return domain.ArchiveTopLoss, true
	case "games", "игры":
		return domain.ArchiveTopGames, true
	case "biggest_win", "win", "разовый", "выигрыш":
		return domain.ArchiveTopBiggestWin, true
	default:
		return "", false
	}
}

func buildBillAdjustCallback(sessionID string, itemIndex int, delta int) string {
	return billAdjustCallbackPrefix + sessionID + ":" + strconv.Itoa(itemIndex) + ":" + strconv.Itoa(delta)
}

func parseBillAdjustCallback(value string) (string, int, int, bool) {
	if !strings.HasPrefix(value, billAdjustCallbackPrefix) {
		return "", 0, 0, false
	}

	parts := strings.Split(strings.TrimPrefix(value, billAdjustCallbackPrefix), ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}

	itemIndex, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, 0, false
	}
	delta, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, 0, false
	}

	return parts[0], itemIndex, delta, true
}

func buildBillFinishCallback(sessionID string) string {
	return billFinishCallbackPrefix + sessionID
}

func parseBillFinishCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billFinishCallbackPrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billFinishCallbackPrefix), true
}

func buildBillFinishForceCallback(sessionID string) string {
	return billFinishForcePrefix + sessionID
}

func parseBillFinishForceCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billFinishForcePrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billFinishForcePrefix), true
}

func buildBillCancelCallback(sessionID string) string {
	return billCancelCallbackPrefix + sessionID
}

func parseBillCancelCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billCancelCallbackPrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billCancelCallbackPrefix), true
}

func buildBillClosePreviousCallback(sessionID string) string {
	return billClosePreviousPrefix + sessionID
}

func parseBillClosePreviousCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billClosePreviousPrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billClosePreviousPrefix), true
}

func buildBillCancelForceCallback(sessionID string) string {
	return billCancelForcePrefix + sessionID
}

func parseBillCancelForceCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billCancelForcePrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billCancelForcePrefix), true
}

func buildBillSendMyCallback(sessionID string) string {
	return billSendMyCallbackPrefix + sessionID
}

func parseBillSendMyCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billSendMyCallbackPrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billSendMyCallbackPrefix), true
}

func buildBillSplitMenuCallback(sessionID string) string {
	return billSplitMenuPrefix + sessionID
}

func parseBillSplitMenuCallback(value string) (string, bool) {
	if !strings.HasPrefix(value, billSplitMenuPrefix) {
		return "", false
	}
	return strings.TrimPrefix(value, billSplitMenuPrefix), true
}

func buildBillSplitItemCallback(sessionID string, itemIndex int) string {
	return billSplitItemPrefix + sessionID + ":" + strconv.Itoa(itemIndex)
}

func parseBillSplitItemCallback(value string) (string, int, bool) {
	if !strings.HasPrefix(value, billSplitItemPrefix) {
		return "", 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, billSplitItemPrefix), ":", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	itemIndex, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false
	}
	return parts[0], itemIndex, true
}

func billReplyMarkup(session domain.BillSession) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0)
	for _, specRow := range billKeyboard(session) {
		row := make([]tgbotapi.InlineKeyboardButton, 0, len(specRow))
		for _, spec := range specRow {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(spec.Text, spec.Data))
		}
		rows = append(rows, row)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func isBillPhotoCommand(message *tgbotapi.Message) bool {
	if message == nil || len(message.Photo) == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(message.Caption), "/bill")
}

func parseBillCaptionArgs(caption string) string {
	caption = strings.TrimSpace(caption)
	if !strings.HasPrefix(caption, "/bill") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(caption, "/bill"))
}

func resolveBillPayer(user *tgbotapi.User, raw string) (int64, string) {
	defaultName := displayUserName(user)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if user == nil {
			return 0, defaultName
		}
		return user.ID, defaultName
	}

	if user != nil {
		if strings.EqualFold(raw, defaultName) {
			return user.ID, defaultName
		}
		if user.UserName != "" && strings.EqualFold(strings.TrimPrefix(raw, "@"), user.UserName) {
			return user.ID, defaultName
		}
	}

	return 0, raw
}

func displayUserName(user *tgbotapi.User) string {
	if user == nil {
		return "Unknown"
	}
	if username := strings.TrimSpace(user.UserName); username != "" {
		return "@" + username
	}
	name := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	if name != "" {
		return name
	}
	return strconv.FormatInt(user.ID, 10)
}

func isArchiveAllowedChatID(chatID int64) bool {
	_, ok := archiveAllowedChatIDs[chatID]
	return ok
}

func personalHelpText() string {
	return strings.Join([]string{
		"Бот для учета покерных игр и расчета итогов",
		"",
		"Поддерживаемые команды",
		"/start /help - показать это сообщение",
		"/reg - зарегистрировать группу для игр (доступно только администратору бота)",
		"/game - подсчитать результаты игры (доступно из зарегистрированной группы)",
		"/setbuyin 2500 - изменить цену байина (2000 по умолчанию, доступно из зарегистрированной группы)",
		"/bill - отправьте фото чека с подписью /bill или /bill @payer",
		"/debug - создать тестовый счет без OCR",
		"/groups - показать доступные игровые группы",
		"/stats - показать статистику игр",
		"/history - показать историю игр",
		"/players - показать статистику игроков",
		"/archive - показать сообщение о переносе архива",
		"",
		"Для счета используются кнопки в сообщении чека.",
	}, "\n")
}

func (b *Bot) reply(chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send message failed: chat_id=%d reply_to=%d err=%v text=%q", chatID, replyTo, err, text)
	}
}

func (b *Bot) replyHTML(chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send html message failed: chat_id=%d reply_to=%d err=%v text=%q", chatID, replyTo, err, text)
	}
}

func (b *Bot) replyLong(chatID int64, replyTo int, text string) {
	const maxMessageLength = 4096
	if len(text) <= maxMessageLength {
		b.reply(chatID, replyTo, text)
		return
	}

	lines := strings.Split(text, "\n")
	var builder strings.Builder
	first := true

	flush := func() {
		if builder.Len() == 0 {
			return
		}
		b.reply(chatID, replyTo, strings.TrimRight(builder.String(), "\n"))
		replyTo = 0
		builder.Reset()
	}

	for _, line := range lines {
		chunk := line + "\n"
		if builder.Len()+len(chunk) > maxMessageLength && builder.Len() > 0 {
			flush()
			first = false
		}
		if !first && builder.Len() == 0 && len(chunk) > maxMessageLength {
			b.reply(chatID, replyTo, line)
			replyTo = 0
			continue
		}
		builder.WriteString(chunk)
	}

	flush()
}

func archiveCommandInput(message *tgbotapi.Message) string {
	command := message.Command()
	args := strings.TrimSpace(message.CommandArguments())
	if strings.HasPrefix(command, "archive_") {
		suffix := strings.TrimPrefix(command, "archive_")
		if args == "" {
			return suffix
		}
		return suffix + " " + args
	}
	return args
}

func parseArchiveCommandInput(value string) (string, string) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", ""
	}

	parts := strings.SplitN(raw, " ", 2)
	command := strings.ToLower(strings.TrimSpace(parts[0]))
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	return command, args
}

type storedMessage struct {
	MessageID        int
	Text             string
	ReplyToMessageID int
	Date             time.Time
}

type chatMessageBuffer struct {
	order []int
	items map[int]storedMessage
	limit int
}

type messageStore struct {
	mu    sync.RWMutex
	chats map[int64]*chatMessageBuffer
	limit int
}

func newMessageStore(limit int) *messageStore {
	return &messageStore{
		chats: make(map[int64]*chatMessageBuffer),
		limit: limit,
	}
}

func (s *messageStore) Save(message *tgbotapi.Message) {
	if message == nil || message.Chat == nil || strings.TrimSpace(message.Text) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	buffer, ok := s.chats[message.Chat.ID]
	if !ok {
		buffer = &chatMessageBuffer{
			order: make([]int, 0, s.limit),
			items: make(map[int]storedMessage, s.limit),
			limit: s.limit,
		}
		s.chats[message.Chat.ID] = buffer
	}

	replyToID := 0
	if message.ReplyToMessage != nil {
		replyToID = message.ReplyToMessage.MessageID
	}

	if _, exists := buffer.items[message.MessageID]; !exists {
		buffer.order = append(buffer.order, message.MessageID)
	}

	buffer.items[message.MessageID] = storedMessage{
		MessageID:        message.MessageID,
		Text:             message.Text,
		ReplyToMessageID: replyToID,
		Date:             time.Unix(int64(message.Date), 0).UTC(),
	}

	for len(buffer.order) > buffer.limit {
		oldestID := buffer.order[0]
		buffer.order = buffer.order[1:]
		delete(buffer.items, oldestID)
	}
}

func (s *messageStore) Get(chatID int64, messageID int) *storedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buffer, ok := s.chats[chatID]
	if !ok {
		return nil
	}

	message, ok := buffer.items[messageID]
	if !ok {
		return nil
	}

	copy := message
	return &copy
}

type cachedMembership struct {
	Allowed   bool
	Title     string
	ExpiresAt time.Time
}

type membershipCache struct {
	mu   sync.RWMutex
	ttl  time.Duration
	data map[string]cachedMembership
}

func newMembershipCache(ttl time.Duration) *membershipCache {
	return &membershipCache{
		ttl:  ttl,
		data: make(map[string]cachedMembership),
	}
}

func (c *membershipCache) Get(chatID, userID int64) (cachedMembership, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.data[membershipCacheKey(chatID, userID)]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return cachedMembership{}, false
	}

	return entry, true
}

func (c *membershipCache) Set(chatID, userID int64, entry cachedMembership) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry.ExpiresAt = time.Now().Add(c.ttl)
	c.data[membershipCacheKey(chatID, userID)] = entry
}

func membershipCacheKey(chatID, userID int64) string {
	return strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(userID, 10)
}
