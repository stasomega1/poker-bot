package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"

	"github.com/shopspring/decimal"
)

type ChatSettingsService struct {
	chatRepo repository.AllowedChatRepository
}

func NewChatSettingsService(chatRepo repository.AllowedChatRepository) *ChatSettingsService {
	return &ChatSettingsService{chatRepo: chatRepo}
}

func (s *ChatSettingsService) IsAllowed(ctx context.Context, chatID int64) (bool, error) {
	_, err := s.chatRepo.FindActiveByChatID(ctx, chatID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, mongorepo.ErrChatNotFound) {
		return false, nil
	}
	return false, err
}

func (s *ChatSettingsService) RegisterChat(ctx context.Context, chatID int64, title string) error {
	return s.chatRepo.CreateIfMissing(ctx, domain.AllowedChat{
		ChatID:    chatID,
		Title:     title,
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
}

func (s *ChatSettingsService) ListAllowedChats(ctx context.Context) ([]domain.AllowedChat, error) {
	return s.chatRepo.ListActive(ctx)
}

func (s *ChatSettingsService) UpdateBuyInPrice(ctx context.Context, chatID int64, title string, price decimal.Decimal) (domain.AllowedChat, error) {
	if price.LessThanOrEqual(decimal.Zero) {
		return domain.AllowedChat{}, fmt.Errorf("buy-in price must be greater than zero")
	}

	return s.chatRepo.UpdateBuyInPrice(ctx, chatID, title, price)
}
