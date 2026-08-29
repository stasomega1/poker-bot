package app

import (
	"context"
	"log"
	"time"

	"poker-bot/internal/config"
	"poker-bot/internal/domain"
	mongostorage "poker-bot/internal/repository/mongo"
	"poker-bot/internal/service"
	"poker-bot/internal/telegram"
	"poker-bot/internal/webapp"
)

type App struct {
	bot               *telegram.Bot
	webServer         *webapp.Server
	client            *mongostorage.Client
	bill              *service.BillService
	billSweepInterval time.Duration
}

func New(cfg config.Config) (*App, error) {
	client, err := mongostorage.NewClient(context.Background(), cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return nil, err
	}

	chatRepo := mongostorage.NewAllowedChatRepository(client.Database, cfg.DefaultBuyInPrice)
	gameRepo := mongostorage.NewGameRepository(client.Database)
	archiveRepo := mongostorage.NewArchiveRepository(client.Database)
	billRepo := mongostorage.NewBillSessionRepository(client.Database)

	calculator := service.NewSettlementCalculator()
	parser := service.NewMessageParser()
	gameService := service.NewGameService(gameRepo, chatRepo, parser, calculator)
	settingsService := service.NewChatSettingsService(chatRepo)
	statsService := service.NewStatsService(gameRepo, chatRepo)
	archiveService := service.NewArchiveService(archiveRepo)
	var receiptOCR service.ReceiptOCR
	if cfg.OpenAIAPIKey != "" {
		receiptOCR = service.NewOpenAIReceiptOCR(cfg.OpenAIAPIKey, cfg.OpenAIReceiptModel)
	}
	billService := service.NewBillService(billRepo, chatRepo, receiptOCR)
	billService.SetAutoCloseAfter(cfg.BillAutoCloseAfter)

	bot, err := telegram.NewBot(cfg, gameService, settingsService, statsService, archiveService, billService)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, err
	}

	webServer, err := webapp.NewServer(
		cfg.HTTPAddr,
		billService,
		webapp.NewInitDataValidator(cfg.BotToken, bot.BotID(), cfg.InitDataMaxAge),
		bot,
		buildWebAppDevAuth(cfg),
	)
	if err != nil {
		_ = client.Close(context.Background())
		return nil, err
	}
	if cfg.WebAppBaseURL == "" {
		log.Printf("mini app button disabled: WEBAPP_BASE_URL is empty")
	} else if cfg.WebAppBaseURL[:min(len(cfg.WebAppBaseURL), len("https://"))] != "https://" {
		log.Printf("mini app local mode: WEBAPP_BASE_URL=%s uses URL buttons instead of Telegram web_app buttons", cfg.WebAppBaseURL)
	} else {
		log.Printf("mini app enabled: WEBAPP_BASE_URL=%s", cfg.WebAppBaseURL)
	}
	if cfg.WebAppDevMode {
		log.Printf("mini app dev auth enabled: user_id=%d username=%s", cfg.WebAppDevUserID, cfg.WebAppDevUsername)
	}

	return &App{
		bot:               bot,
		webServer:         webServer,
		client:            client,
		bill:              billService,
		billSweepInterval: cfg.BillSweepInterval,
	}, nil
}

func buildWebAppDevAuth(cfg config.Config) *webapp.AuthData {
	if !cfg.WebAppDevMode {
		return nil
	}
	return &webapp.AuthData{
		User: webapp.TelegramUser{
			ID:        cfg.WebAppDevUserID,
			Username:  cfg.WebAppDevUsername,
			FirstName: cfg.WebAppDevFirstName,
			LastName:  cfg.WebAppDevLastName,
		},
	}
}

func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go a.runBillSweep(runCtx)
	go func() {
		errCh <- a.webServer.Run(runCtx)
	}()
	go func() {
		errCh <- a.bot.Run(runCtx)
	}()

	err := <-errCh
	cancel()
	return err
}

func (a *App) runBillSweep(ctx context.Context) {
	if a.bill == nil || a.billSweepInterval <= 0 {
		return
	}

	ticker := time.NewTicker(a.billSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			closed, err := a.bill.CloseExpiredSessions(ctx, time.Now().UTC(), 100)
			if err != nil {
				log.Printf("bill sweep failed: %v", err)
				continue
			}
			for _, item := range closed {
				a.bot.RefreshBillMessage(ctx, item.Session)
				if item.Session.Status == domain.BillSessionFinished {
					a.bot.PublishBillFinished(ctx, item.Session, item.Summary)
					continue
				}
				a.bot.PublishBillCancelled(ctx, item.Session)
			}
		}
	}
}

func (a *App) Close(ctx context.Context) error {
	if err := a.bot.Close(); err != nil {
		return err
	}
	if err := a.webServer.Shutdown(ctx); err != nil {
		return err
	}

	return a.client.Close(ctx)
}
