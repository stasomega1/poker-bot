package app

import (
	"context"

	"poker-bot/internal/config"
	mongostorage "poker-bot/internal/repository/mongo"
	"poker-bot/internal/service"
	"poker-bot/internal/telegram"
)

type App struct {
	bot    *telegram.Bot
	client *mongostorage.Client
}

func New(cfg config.Config) (*App, error) {
	client, err := mongostorage.NewClient(context.Background(), cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return nil, err
	}

	chatRepo := mongostorage.NewAllowedChatRepository(client.Database, cfg.DefaultBuyInPrice)
	gameRepo := mongostorage.NewGameRepository(client.Database)
	archiveRepo := mongostorage.NewArchiveRepository(client.Database)

	calculator := service.NewSettlementCalculator()
	parser := service.NewMessageParser()
	gameService := service.NewGameService(gameRepo, chatRepo, parser, calculator)
	settingsService := service.NewChatSettingsService(chatRepo)
	statsService := service.NewStatsService(gameRepo, chatRepo)
	archiveService := service.NewArchiveService(archiveRepo)

	bot, err := telegram.NewBot(cfg, gameService, settingsService, statsService, archiveService)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, err
	}

	return &App{
		bot:    bot,
		client: client,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.bot.Run(ctx)
}

func (a *App) Close(ctx context.Context) error {
	if err := a.bot.Close(); err != nil {
		return err
	}

	return a.client.Close(ctx)
}
