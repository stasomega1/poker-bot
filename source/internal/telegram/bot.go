package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"pocker-bot/internal/config"
	"pocker-bot/internal/domain"
	"pocker-bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shopspring/decimal"
)

const (
	maxStoredMessagesPerChat = 500
)

type Bot struct {
	api             *tgbotapi.BotAPI
	gameService     *service.GameService
	settingsService *service.ChatSettingsService
	statsService    *service.StatsService
	messageStore    *messageStore
	registrarUserID int64
}

func NewBot(cfg config.Config, gameService *service.GameService, settingsService *service.ChatSettingsService, statsService *service.StatsService) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, err
	}

	return &Bot{
		api:             api,
		gameService:     gameService,
		settingsService: settingsService,
		statsService:    statsService,
		messageStore:    newMessageStore(maxStoredMessagesPerChat),
		registrarUserID: cfg.RegistrarUserID,
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
			if update.Message == nil {
				continue
			}
			b.handleMessage(ctx, update.Message)
		}
	}
}

func (b *Bot) Close() error {
	b.api.StopReceivingUpdates()
	return nil
}

func (b *Bot) handleMessage(ctx context.Context, message *tgbotapi.Message) {
	if message.Chat == nil || message.Chat.IsPrivate() {
		return
	}

	replyToID := 0
	if message.ReplyToMessage != nil {
		replyToID = message.ReplyToMessage.MessageID
	}

	log.Printf("incoming: chat_id=%d message_id=%d reply_to=%d command=%t user_id=%d text=%q",
		message.Chat.ID, message.MessageID, replyToID, message.IsCommand(), message.From.ID, message.Text)

	if message.IsCommand() && message.Command() == "reg" {
		b.handleRegisterChat(ctx, message)
		return
	}

	allowed, err := b.settingsService.IsAllowed(ctx, message.Chat.ID)
	if err != nil {
		log.Printf("allow check failed: chat_id=%d err=%v", message.Chat.ID, err)
		return
	}
	if !allowed {
		log.Printf("message skipped: chat_id=%d is not registered", message.Chat.ID)
		return
	}

	b.messageStore.Save(message)

	if !message.IsCommand() {
		return
	}

	switch message.Command() {
	case "start", "help":
		b.reply(message.Chat.ID, message.MessageID, helpText())
	case "setbuyin":
		b.handleSetBuyIn(ctx, message)
	case "game":
		b.handleGame(ctx, message)
	case "history":
		b.handleHistory(ctx, message)
	case "stats":
		b.handleStats(ctx, message)
	case "players":
		b.handlePlayers(ctx, message)
	default:
		log.Printf("unknown command: chat_id=%d message_id=%d command=%q", message.Chat.ID, message.MessageID, message.Command())
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
		b.reply(message.Chat.ID, message.MessageID, "Команда /game должна быть ответом на сообщение с результатами.")
		return
	}

	resultsRef := b.messageStore.Get(message.Chat.ID, message.ReplyToMessage.MessageID)
	if resultsRef == nil {
		b.reply(message.Chat.ID, message.MessageID, "Не удалось найти сообщение с результатами среди последних сообщений чата.")
		return
	}

	if resultsRef.ReplyToMessageID == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Сообщение с результатами должно быть reply на сообщение с байинами.")
		return
	}

	buyInsRef := b.messageStore.Get(message.Chat.ID, resultsRef.ReplyToMessageID)
	if buyInsRef == nil {
		b.reply(message.Chat.ID, message.MessageID, "Не удалось найти сообщение с байинами среди последних сообщений чата.")
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

func (b *Bot) handleHistory(ctx context.Context, message *tgbotapi.Message) {
	games, err := b.gameService.History(ctx, message.Chat.ID, 5)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось получить историю: %v", err))
		return
	}
	if len(games) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "История пока пустая.")
		return
	}

	lines := []string{"Последние игры:"}
	for _, game := range games {
		lines = append(lines, fmt.Sprintf("%s | банк %s байинов | цена %s тг",
			game.CreatedAt.Format("2006-01-02 15:04"),
			formatDecimal(game.TotalBuyIns),
			formatDecimal(game.BuyInPriceKZT),
		))
	}

	b.reply(message.Chat.ID, message.MessageID, strings.Join(lines, "\n"))
}

func (b *Bot) handleStats(ctx context.Context, message *tgbotapi.Message) {
	stats, err := b.statsService.BuildStats(ctx, message.Chat.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось собрать статистику: %v", err))
		return
	}

	lines := []string{
		"Статистика по чату:",
		fmt.Sprintf("Игр: %d", stats.GamesCount),
		fmt.Sprintf("Всего байинов: %s", formatDecimal(stats.TotalBuyIns)),
		fmt.Sprintf("Средний банк: %s", formatDecimal(stats.AverageBank)),
	}

	if stats.BiggestWinPlayer != "" {
		lines = append(lines, fmt.Sprintf("Лучший результат за игру: %s байинов - %s - Легенда!", formatSignedDecimal(stats.BiggestWin), stats.BiggestWinPlayer))
	}

	b.reply(message.Chat.ID, message.MessageID, strings.Join(lines, "\n"))
}

func (b *Bot) handlePlayers(ctx context.Context, message *tgbotapi.Message) {
	players, err := b.statsService.BuildPlayerStats(ctx, message.Chat.ID)
	if err != nil {
		b.reply(message.Chat.ID, message.MessageID, fmt.Sprintf("Не удалось собрать статистику игроков: %v", err))
		return
	}
	if len(players) == 0 {
		b.reply(message.Chat.ID, message.MessageID, "Статистика игроков пока пустая.")
		return
	}

	lines := []string{"Игроки:"}
	for _, player := range players {
		lines = append(lines, fmt.Sprintf("%s — игр %d, итог %s, занес %s, выиграл %s",
			player.Name,
			player.GamesCount,
			formatSignedDecimal(player.TotalProfit),
			formatDecimal(player.TotalBuyIns),
			formatDecimal(player.TotalWonBuyIns),
		))
	}

	b.reply(message.Chat.ID, message.MessageID, strings.Join(lines, "\n"))
}

func (b *Bot) reply(chatID int64, replyTo int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyTo
	_, _ = b.api.Send(msg)
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
