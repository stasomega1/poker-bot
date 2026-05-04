package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

func (s *StatsService) History(ctx context.Context, chatID int64) ([]domain.NumberedGame, error) {
	if err := s.ensureChatAllowed(ctx, chatID); err != nil {
		return nil, err
	}

	games, err := s.gameRepo.ListAllByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	result := make([]domain.NumberedGame, 0, len(games))
	for _, game := range games {
		result = append(result, domain.NumberedGame{
			Game: game,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Game.GameNumber > result[j].Game.GameNumber
	})

	return result, nil
}

func (s *StatsService) Game(ctx context.Context, chatID int64, gameNumber int) (domain.NumberedGame, error) {
	if gameNumber <= 0 {
		return domain.NumberedGame{}, fmt.Errorf("укажите номер игры: /stats_game <номер>")
	}

	game, err := s.gameRepo.FindByChatIDAndGameNumber(ctx, chatID, gameNumber)
	if err != nil {
		if errors.Is(err, mongorepo.ErrGameNotFound) {
			return domain.NumberedGame{}, fmt.Errorf("игра #%d не найдена", gameNumber)
		}
		return domain.NumberedGame{}, err
	}

	return domain.NumberedGame{Game: game}, nil
}

func (s *StatsService) Player(ctx context.Context, chatID int64, name string) (domain.GamePlayerHistory, error) {
	if err := s.ensureChatAllowed(ctx, chatID); err != nil {
		return domain.GamePlayerHistory{}, err
	}
	if strings.TrimSpace(name) == "" {
		return domain.GamePlayerHistory{}, fmt.Errorf("укажите имя игрока: /stats_player <имя>")
	}

	games, err := s.gameRepo.ListAllByChatID(ctx, chatID)
	if err != nil {
		return domain.GamePlayerHistory{}, err
	}
	players, err := s.gameRepo.BuildPlayerStatsByChatID(ctx, chatID)
	if err != nil {
		return domain.GamePlayerHistory{}, err
	}

	targetName := ""
	for _, player := range players {
		if normalizeStatsName(player.Name) == normalizeStatsName(name) {
			targetName = player.Name
			break
		}
	}
	if targetName == "" {
		return domain.GamePlayerHistory{}, fmt.Errorf("игрок \"%s\" не найден", strings.TrimSpace(name))
	}

	var playerStats domain.PlayerStats
	for _, player := range players {
		if player.Name == targetName {
			playerStats = player
			break
		}
	}

	history := make([]domain.GamePlayerHistoryEntry, 0)
	for _, game := range games {
		for _, player := range game.Players {
			if player.Name != targetName {
				continue
			}
			history = append(history, domain.GamePlayerHistoryEntry{
				GameNumber:   game.GameNumber,
				SessionDate:  game.SessionDate,
				CreatedAt:    game.CreatedAt,
				BuyIns:       player.BuyIns,
				WonBuyIns:    player.WonBuyIns,
				ProfitBuyIns: player.ProfitBuyIns,
			})
			break
		}
	}

	sort.Slice(history, func(i, j int) bool {
		return history[i].GameNumber > history[j].GameNumber
	})

	return domain.GamePlayerHistory{
		Player: playerStats,
		Games:  history,
	}, nil
}

func (s *StatsService) Top(ctx context.Context, chatID int64, metric domain.ArchiveTopMetric, limit int) ([]domain.PlayerStats, error) {
	if err := s.ensureChatAllowed(ctx, chatID); err != nil {
		return nil, err
	}

	players, err := s.gameRepo.BuildPlayerStatsByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	sort.Slice(players, func(i, j int) bool {
		switch metric {
		case domain.ArchiveTopLoss:
			if players[i].TotalProfit.Equal(players[j].TotalProfit) {
				return players[i].Name < players[j].Name
			}
			return players[i].TotalProfit.LessThan(players[j].TotalProfit)
		case domain.ArchiveTopGames:
			if players[i].GamesCount == players[j].GamesCount {
				return players[i].Name < players[j].Name
			}
			return players[i].GamesCount > players[j].GamesCount
		case domain.ArchiveTopBiggestWin:
			if players[i].BiggestWin.Equal(players[j].BiggestWin) {
				return players[i].Name < players[j].Name
			}
			return players[i].BiggestWin.GreaterThan(players[j].BiggestWin)
		default:
			if players[i].TotalProfit.Equal(players[j].TotalProfit) {
				return players[i].Name < players[j].Name
			}
			return players[i].TotalProfit.GreaterThan(players[j].TotalProfit)
		}
	})

	if limit > 0 && len(players) > limit {
		players = players[:limit]
	}

	return players, nil
}

func (s *StatsService) ensureChatAllowed(ctx context.Context, chatID int64) error {
	if _, err := s.chatRepo.FindActiveByChatID(ctx, chatID); err != nil {
		if errors.Is(err, mongorepo.ErrChatNotFound) {
			return fmt.Errorf("чат не зарегистрирован для игры")
		}
		return err
	}
	return nil
}

func normalizeStatsName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
