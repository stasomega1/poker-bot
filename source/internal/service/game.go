package service

import (
	"context"
	"errors"
	"fmt"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"
)

var ErrRegameNotLatest = errors.New("regame target is not the latest game")

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

func (s *GameService) LatestGame(ctx context.Context, chatID int64) (domain.Game, error) {
	if _, err := s.findActiveChat(ctx, chatID); err != nil {
		return domain.Game{}, err
	}
	return s.gameRepo.FindLatestByChatID(ctx, chatID)
}

func (s *GameService) RecalculateLatestGame(
	ctx context.Context,
	chatID int64,
	sourceCommandMessageID int,
	buyInsText string,
	resultsText string,
	buyIns []domain.PlayerInput,
	winners []domain.PlayerInput,
) (domain.Game, error) {
	if _, err := s.findActiveChat(ctx, chatID); err != nil {
		return domain.Game{}, err
	}

	previous, err := s.gameRepo.FindLatestByChatID(ctx, chatID)
	if err != nil {
		return domain.Game{}, err
	}
	if previous.SourceCommandMessageID != sourceCommandMessageID {
		return domain.Game{}, ErrRegameNotLatest
	}

	request := domain.GameRequest{
		ChatID:           previous.ChatID,
		ChatTitle:        previous.ChatTitle,
		SessionDate:      previous.SessionDate,
		BuyInPriceKZT:    previous.BuyInPriceKZT,
		BuyInsMessageID:  previous.SourceBuyInsMessageID,
		ResultsMessageID: previous.SourceResultsMessageID,
		CommandMessageID: previous.SourceCommandMessageID,
		BuyInsText:       buyInsText,
		ResultsText:      resultsText,
		CreateUserID:     previous.CreatedByUserID,
		CreateUserName:   previous.CreatedByName,
		BuyIns:           buyIns,
		Winners:          winners,
	}
	recalculated, err := s.calculator.BuildGame(request)
	if err != nil {
		return domain.Game{}, err
	}
	recalculated.GameNumber = previous.GameNumber
	recalculated.CreatedAt = previous.CreatedAt

	if err := s.gameRepo.Replace(ctx, recalculated); err != nil {
		return domain.Game{}, err
	}
	return recalculated, nil
}

func (s *GameService) findActiveChat(ctx context.Context, chatID int64) (domain.AllowedChat, error) {
	chat, err := s.chatRepo.FindActiveByChatID(ctx, chatID)
	if errors.Is(err, mongorepo.ErrChatNotFound) {
		return domain.AllowedChat{}, fmt.Errorf("чат не зарегистрирован для игры")
	}
	return chat, err
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
