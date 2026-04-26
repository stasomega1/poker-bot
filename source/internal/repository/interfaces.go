package repository

import (
	"context"

	"pocker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type AllowedChatRepository interface {
	CreateIfMissing(ctx context.Context, chat domain.AllowedChat) error
	FindActiveByChatID(ctx context.Context, chatID int64) (domain.AllowedChat, error)
	UpdateBuyInPrice(ctx context.Context, chatID int64, title string, price decimal.Decimal) (domain.AllowedChat, error)
}

type GameRepository interface {
	Create(ctx context.Context, game domain.Game) error
	ListRecentByChatID(ctx context.Context, chatID int64, limit int64) ([]domain.Game, error)
	BuildStatsByChatID(ctx context.Context, chatID int64) (domain.Stats, error)
	BuildPlayerStatsByChatID(ctx context.Context, chatID int64) ([]domain.PlayerStats, error)
}
