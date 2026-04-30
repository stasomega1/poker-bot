package telegram

import (
	"context"
	"fmt"
	"log"
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

const archiveTopCallbackPrefix = "archive_top:"

type accessibleChat struct {
	ChatID int64
	Title  string
}

func NewBot(cfg config.Config, gameService *service.GameService, settingsService *service.ChatSettingsService, statsService *service.StatsService, archiveService *service.ArchiveService) (*Bot, error) {
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
		messageStore:    newMessageStore(maxStoredMessagesPerChat),
		registrarUserID: cfg.RegistrarUserID,
		membershipCache: newMembershipCache(membershipCacheTTL),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 30

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

	switch message.Command() {
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
	case "players":
		b.handleGroupPlayers(ctx, message.Chat.ID, message.Chat.Title, message.Chat.ID, message.MessageID)
	default:
		log.Printf("unknown command: chat_id=%d message_id=%d command=%q", message.Chat.ID, message.MessageID, message.Command())
	}
}

func (b *Bot) handlePrivateMessage(ctx context.Context, message *tgbotapi.Message) {
	if !message.IsCommand() {
		return
	}

	switch message.Command() {
	case "start", "help":
		b.replyHTML(message.Chat.ID, message.MessageID, personalHelpText())
	case "groups":
		b.handlePrivateGroups(ctx, message)
	case "stats":
		b.handlePrivateAction(ctx, message, actionStats)
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

	if metric, ok := parseArchiveTopCallback(query.Data); ok {
		if !b.canUseArchiveInPrivate(ctx, query.From.ID) && query.Message.Chat.IsPrivate() {
			b.answerCallback(query.ID, "Нет доступа к архиву.")
			return
		}
		if !query.Message.Chat.IsPrivate() && !isArchiveAllowedChatID(query.Message.Chat.ID) {
			b.answerCallback(query.ID, "Архив доступен только в архивной группе.")
			return
		}

		b.answerCallback(query.ID, "")
		b.handleArchiveTop(ctx, query.Message.Chat.ID, query.Message.MessageID, metric)
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
		b.handleGroupStats(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID)
	case actionHistory:
		b.handleGroupHistory(ctx, chatID, title, query.Message.Chat.ID, query.Message.MessageID)
	case actionPlayers:
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
	command, args := parseArchiveCommandInput(rawInput)
	if command == "" {
		b.reply(responseChatID, replyTo, renderArchiveHelpText())
		return
	}

	switch command {
	case "history":
		games, err := b.archiveService.History(ctx, archiveChatID, 1000)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить архив: %v", err))
			return
		}
		if len(games) == 0 {
			b.reply(responseChatID, replyTo, "Архив пока пуст.")
			return
		}
		b.replyLong(responseChatID, replyTo, renderArchiveHistory(games))
	case "stats":
		stats, err := b.archiveService.Stats(ctx, archiveChatID)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось собрать статистику архива: %v", err))
			return
		}
		b.reply(responseChatID, replyTo, renderArchiveStats(stats))
	case "players":
		players, err := b.archiveService.Players(ctx, archiveChatID)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игроков архива: %v", err))
			return
		}
		if len(players) == 0 {
			b.reply(responseChatID, replyTo, "В архиве пока нет игроков.")
			return
		}
		b.reply(responseChatID, replyTo, renderArchivePlayers(players))
	case "game":
		number, err := strconv.Atoi(args)
		if err != nil || number <= 0 {
			b.reply(responseChatID, replyTo, "Укажите номер игры: /archive_game <номер>")
			return
		}
		game, err := b.archiveService.Game(ctx, archiveChatID, number)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игру архива: %v", err))
			return
		}
		b.reply(responseChatID, replyTo, renderArchiveGame(game))
	case "player":
		history, err := b.archiveService.Player(ctx, archiveChatID, args)
		if err != nil {
			b.reply(responseChatID, replyTo, fmt.Sprintf("Не удалось получить игрока архива: %v", err))
			return
		}
		b.reply(responseChatID, replyTo, renderArchivePlayer(history))
	case "top":
		if args == "" {
			b.sendArchiveTopChoice(responseChatID, replyTo)
			return
		}

		metric, ok := parseArchiveTopMetric(args)
		if !ok {
			b.reply(responseChatID, replyTo, "Неизвестный параметр топа. Используйте /archive_top и выберите кнопку.")
			return
		}
		b.handleArchiveTop(ctx, responseChatID, replyTo, metric)
	default:
		b.reply(responseChatID, replyTo, renderArchiveHelpText())
	}
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
		lines = append(lines, fmt.Sprintf("%s | банк %s байинов | цена %s тг",
			game.CreatedAt.Format("2006-01-02 15:04"),
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

	lines := []string{fmt.Sprintf("Игроки: %s", targetTitle)}
	for _, player := range players {
		lines = append(lines, fmt.Sprintf("%s — игр %d, итог %s, занес %s, выиграл %s",
			player.Name,
			player.GamesCount,
			formatSignedDecimal(player.TotalProfit),
			formatDecimal(player.TotalBuyIns),
			formatDecimal(player.TotalWonBuyIns),
		))
	}

	b.reply(responseChatID, replyTo, strings.Join(lines, "\n"))
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
	_, _ = b.api.Send(msg)
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
	_, _ = b.api.Send(msg)
}

func (b *Bot) answerCallback(callbackID, text string) {
	cfg := tgbotapi.NewCallback(callbackID, text)
	_, _ = b.api.Request(cfg)
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
		"/groups - показать доступные игровые группы",
		"/stats - показать статистику игр",
		"/history - показать историю игр",
		"/players - показать статистику игроков",
		"/archive - открыть архив игр",
	}, "\n")
}

func (b *Bot) reply(chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	_, _ = b.api.Send(msg)
}

func (b *Bot) replyHTML(chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	if replyTo != 0 {
		msg.ReplyToMessageID = replyTo
	}
	_, _ = b.api.Send(msg)
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
