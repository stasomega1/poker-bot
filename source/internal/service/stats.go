package service

import (
	"context"
	"errors"
	"fmt"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"
)

type StatsService struct {
	gameRepo repository.GameRepository
	chatRepo repository.AllowedChatRepository
}

func NewStatsService(gameRepo repository.GameRepository, chatRepo repository.AllowedChatRepository) *StatsService {
	return &StatsService{
		gameRepo: gameRepo,
		chatRepo: chatRepo,
	}
}

func (s *StatsService) BuildStats(ctx context.Context, chatID int64) (domain.Stats, error) {
	if _, err := s.chatRepo.FindActiveByChatID(ctx, chatID); err != nil {
		if errors.Is(err, mongorepo.ErrChatNotFound) {
			return domain.Stats{}, fmt.Errorf("чат не зарегистрирован для игры")
		}
		return domain.Stats{}, err
	}

	return s.gameRepo.BuildStatsByChatID(ctx, chatID)
}

func (s *StatsService) BuildPlayerStats(ctx context.Context, chatID int64) ([]domain.PlayerStats, error) {
	if _, err := s.chatRepo.FindActiveByChatID(ctx, chatID); err != nil {
		if errors.Is(err, mongorepo.ErrChatNotFound) {
			return nil, fmt.Errorf("чат не зарегистрирован для игры")
		}
		return nil, err
	}

	return s.gameRepo.BuildPlayerStatsByChatID(ctx, chatID)
}
