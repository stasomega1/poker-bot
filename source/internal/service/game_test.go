package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type fakeGameRepository struct {
	latest   domain.Game
	replaced domain.Game
}

func (r *fakeGameRepository) Create(context.Context, domain.Game) error { return nil }
func (r *fakeGameRepository) Replace(_ context.Context, game domain.Game) error {
	r.replaced = game
	r.latest = game
	return nil
}
func (r *fakeGameRepository) ListRecentByChatID(context.Context, int64, int64) ([]domain.Game, error) {
	return []domain.Game{r.latest}, nil
}
func (r *fakeGameRepository) ListAllByChatID(context.Context, int64) ([]domain.Game, error) {
	return []domain.Game{r.latest}, nil
}
func (r *fakeGameRepository) FindLatestByChatID(context.Context, int64) (domain.Game, error) {
	return r.latest, nil
}
func (r *fakeGameRepository) FindByChatIDAndGameNumber(context.Context, int64, int) (domain.Game, error) {
	return r.latest, nil
}
func (r *fakeGameRepository) NextGameNumber(context.Context, int64) (int, error) { return 2, nil }
func (r *fakeGameRepository) BackfillGameNumbers(context.Context) error          { return nil }
func (r *fakeGameRepository) BuildStatsByChatID(context.Context, int64) (domain.Stats, error) {
	return domain.Stats{}, nil
}
func (r *fakeGameRepository) BuildPlayerStatsByChatID(context.Context, int64) ([]domain.PlayerStats, error) {
	return nil, nil
}

type fakeGameAllowedChatRepository struct {
	chat domain.AllowedChat
}

func (r *fakeGameAllowedChatRepository) CreateIfMissing(context.Context, domain.AllowedChat) error {
	return nil
}
func (r *fakeGameAllowedChatRepository) FindActiveByChatID(context.Context, int64) (domain.AllowedChat, error) {
	return r.chat, nil
}
func (r *fakeGameAllowedChatRepository) ListActive(context.Context) ([]domain.AllowedChat, error) {
	return []domain.AllowedChat{r.chat}, nil
}
func (r *fakeGameAllowedChatRepository) UpdateBuyInPrice(context.Context, int64, string, decimal.Decimal) (domain.AllowedChat, error) {
	return r.chat, nil
}

func TestRecalculateLatestGameReplacesGameAndPreservesIdentity(t *testing.T) {
	createdAt := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeGameRepository{latest: domain.Game{
		ChatID:                 10,
		ChatTitle:              "Poker",
		GameNumber:             7,
		SessionDate:            "2026-08-01",
		BuyInPriceKZT:          decimal.NewFromInt(2500),
		SourceBuyInsMessageID:  100,
		SourceResultsMessageID: 101,
		SourceCommandMessageID: 102,
		CreatedAt:              createdAt,
		CreatedByUserID:        42,
		CreatedByName:          "@creator",
	}}
	chats := &fakeGameAllowedChatRepository{chat: domain.AllowedChat{ChatID: 10, IsActive: true}}
	service := NewGameService(repo, chats, NewMessageParser(), NewSettlementCalculator())

	buyInsText := "Alice 2\nBob 1"
	resultsText := "Alice 3"
	buyIns, winners, err := service.ParseInputs(buyInsText, resultsText)
	if err != nil {
		t.Fatalf("ParseInputs() error = %v", err)
	}

	game, err := service.RecalculateLatestGame(context.Background(), 10, 102, buyInsText, resultsText, buyIns, winners)
	if err != nil {
		t.Fatalf("RecalculateLatestGame() error = %v", err)
	}
	if game.GameNumber != 7 || !game.CreatedAt.Equal(createdAt) {
		t.Fatalf("game identity changed: number=%d created_at=%v", game.GameNumber, game.CreatedAt)
	}
	if !game.BuyInPriceKZT.Equal(decimal.NewFromInt(2500)) {
		t.Fatalf("buy-in price changed: %s", game.BuyInPriceKZT)
	}
	if game.SourceBuyInsText != buyInsText || game.SourceResultsText != resultsText {
		t.Fatalf("source texts were not replaced: %#v", game)
	}
	if repo.replaced.GameNumber != 7 {
		t.Fatal("repository did not receive the recalculated game")
	}
}

func TestRecalculateLatestGameRejectsDifferentGameCommand(t *testing.T) {
	repo := &fakeGameRepository{latest: domain.Game{ChatID: 10, SourceCommandMessageID: 200}}
	chats := &fakeGameAllowedChatRepository{chat: domain.AllowedChat{ChatID: 10, IsActive: true}}
	service := NewGameService(repo, chats, NewMessageParser(), NewSettlementCalculator())

	_, err := service.RecalculateLatestGame(context.Background(), 10, 199, "Alice 1", "Alice 1", nil, nil)
	if !errors.Is(err, ErrRegameNotLatest) {
		t.Fatalf("error = %v, want ErrRegameNotLatest", err)
	}
	if repo.replaced.ChatID != 0 {
		t.Fatal("repository Replace() was called for a non-latest game")
	}
}
