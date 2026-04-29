package service

import (
	"context"
	"fmt"
	"strings"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"
)

type ArchiveService struct {
	repo repository.ArchiveRepository
}

func NewArchiveService(repo repository.ArchiveRepository) *ArchiveService {
	return &ArchiveService{repo: repo}
}

func (s *ArchiveService) History(ctx context.Context, chatID int64, limit int64) ([]domain.ArchiveGame, error) {
	return s.repo.ListRecentByChatID(ctx, chatID, limit)
}

func (s *ArchiveService) Stats(ctx context.Context, chatID int64) (domain.ArchiveStats, error) {
	return s.repo.BuildStatsByChatID(ctx, chatID)
}

func (s *ArchiveService) Players(ctx context.Context, chatID int64) ([]domain.ArchivePlayerStats, error) {
	return s.repo.BuildPlayerStatsByChatID(ctx, chatID)
}

func (s *ArchiveService) Game(ctx context.Context, chatID int64, gameNumber int) (domain.ArchiveGame, error) {
	game, err := s.repo.FindByChatIDAndGameNumber(ctx, chatID, gameNumber)
	if err != nil {
		if err == mongorepo.ErrArchiveGameNotFound {
			return domain.ArchiveGame{}, fmt.Errorf("игра архива #%d не найдена", gameNumber)
		}
		return domain.ArchiveGame{}, err
	}
	return game, nil
}

func (s *ArchiveService) Player(ctx context.Context, chatID int64, name string) (domain.ArchivePlayerHistory, error) {
	if strings.TrimSpace(name) == "" {
		return domain.ArchivePlayerHistory{}, fmt.Errorf("укажите имя игрока: /archive_player <имя>")
	}

	history, err := s.repo.FindPlayerHistoryByChatIDAndName(ctx, chatID, name)
	if err != nil {
		if err == mongorepo.ErrArchivePlayerNotFound {
			return domain.ArchivePlayerHistory{}, fmt.Errorf("игрок \"%s\" в архиве не найден", strings.TrimSpace(name))
		}
		return domain.ArchivePlayerHistory{}, err
	}
	return history, nil
}

func (s *ArchiveService) Top(ctx context.Context, chatID int64, metric domain.ArchiveTopMetric, limit int) ([]domain.ArchivePlayerStats, error) {
	switch metric {
	case domain.ArchiveTopProfit, domain.ArchiveTopLoss, domain.ArchiveTopGames, domain.ArchiveTopBiggestWin:
	default:
		return nil, fmt.Errorf("неизвестный параметр топа")
	}

	return s.repo.BuildTopByChatID(ctx, chatID, metric, limit)
}
