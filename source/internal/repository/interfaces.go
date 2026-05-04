package repository

import (
	"context"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type AllowedChatRepository interface {
	CreateIfMissing(ctx context.Context, chat domain.AllowedChat) error
	FindActiveByChatID(ctx context.Context, chatID int64) (domain.AllowedChat, error)
	ListActive(ctx context.Context) ([]domain.AllowedChat, error)
	UpdateBuyInPrice(ctx context.Context, chatID int64, title string, price decimal.Decimal) (domain.AllowedChat, error)
}

type GameRepository interface {
	Create(ctx context.Context, game domain.Game) error
	ListRecentByChatID(ctx context.Context, chatID int64, limit int64) ([]domain.Game, error)
	ListAllByChatID(ctx context.Context, chatID int64) ([]domain.Game, error)
	FindByChatIDAndGameNumber(ctx context.Context, chatID int64, gameNumber int) (domain.Game, error)
	NextGameNumber(ctx context.Context, chatID int64) (int, error)
	BackfillGameNumbers(ctx context.Context) error
	BuildStatsByChatID(ctx context.Context, chatID int64) (domain.Stats, error)
	BuildPlayerStatsByChatID(ctx context.Context, chatID int64) ([]domain.PlayerStats, error)
}

type ArchiveRepository interface {
	ListRecentByChatID(ctx context.Context, chatID int64, limit int64) ([]domain.ArchiveGame, error)
	BuildStatsByChatID(ctx context.Context, chatID int64) (domain.ArchiveStats, error)
	BuildPlayerStatsByChatID(ctx context.Context, chatID int64) ([]domain.ArchivePlayerStats, error)
	FindByChatIDAndGameNumber(ctx context.Context, chatID int64, gameNumber int) (domain.ArchiveGame, error)
	FindPlayerHistoryByChatIDAndName(ctx context.Context, chatID int64, name string) (domain.ArchivePlayerHistory, error)
	BuildTopByChatID(ctx context.Context, chatID int64, metric domain.ArchiveTopMetric, limit int) ([]domain.ArchivePlayerStats, error)
}

type BillSessionRepository interface {
	Create(ctx context.Context, session domain.BillSession) (domain.BillSession, error)
	FindActiveByChatID(ctx context.Context, chatID int64) (domain.BillSession, error)
	FindLatestByChatIDsAndUserID(ctx context.Context, chatIDs []int64, userID int64) (domain.BillSession, error)
	Update(ctx context.Context, session domain.BillSession) error
	FindByID(ctx context.Context, id string) (domain.BillSession, error)
}
