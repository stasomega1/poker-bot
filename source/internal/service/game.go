package service

import (
	"context"
	"errors"
	"fmt"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"
)

type GameService struct {
	gameRepo   repository.GameRepository
	chatRepo   repository.AllowedChatRepository
	parser     *MessageParser
	calculator *SettlementCalculator
}

func NewGameService(gameRepo repository.GameRepository, chatRepo repository.AllowedChatRepository, parser *MessageParser, calculator *SettlementCalculator) *GameService {
	return &GameService{
		gameRepo:   gameRepo,
		chatRepo:   chatRepo,
		parser:     parser,
		calculator: calculator,
	}
}

func (s *GameService) ParseInputs(buyInsText, resultsText string) ([]domain.PlayerInput, []domain.PlayerInput, error) {
	buyIns, err := s.parser.ParsePlayers(buyInsText)
	if err != nil {
		return nil, nil, fmt.Errorf("buy-ins parse error: %w", err)
	}

	results, err := s.parser.ParsePlayers(resultsText)
	if err != nil {
		return nil, nil, fmt.Errorf("results parse error: %w", err)
	}

	return buyIns, results, nil
}

func (s *GameService) SaveGame(ctx context.Context, request domain.GameRequest) (domain.Game, error) {
	chat, err := s.chatRepo.FindActiveByChatID(ctx, request.ChatID)
	if err != nil {
		if errors.Is(err, mongorepo.ErrChatNotFound) {
			return domain.Game{}, fmt.Errorf("чат не зарегистрирован для игры")
		}
		return domain.Game{}, err
	}

	request.BuyInPriceKZT = chat.BuyInPriceKZT

	game, err := s.calculator.BuildGame(request)
	if err != nil {
		return domain.Game{}, err
	}

	gameNumber, err := s.gameRepo.NextGameNumber(ctx, request.ChatID)
	if err != nil {
		return domain.Game{}, err
	}
	game.GameNumber = gameNumber

	if err := s.gameRepo.Create(ctx, game); err != nil {
		return domain.Game{}, err
	}

	return game, nil
}

func (s *GameService) History(ctx context.Context, chatID int64, limit int64) ([]domain.Game, error) {
	if _, err := s.chatRepo.FindActiveByChatID(ctx, chatID); err != nil {
		if errors.Is(err, mongorepo.ErrChatNotFound) {
			return nil, fmt.Errorf("чат не зарегистрирован для игры")
		}
		return nil, err
	}

	return s.gameRepo.ListRecentByChatID(ctx, chatID, limit)
}
