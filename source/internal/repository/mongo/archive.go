package mongo

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"poker-bot/internal/domain"

	"go.mongodb.org/mongo-driver/bson"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	ErrArchiveGameNotFound   = errors.New("archive game not found")
	ErrArchivePlayerNotFound = errors.New("archive player not found")
)

type ArchiveRepository struct {
	collection *driver.Collection
}

func NewArchiveRepository(db *driver.Database) *ArchiveRepository {
	return &ArchiveRepository{collection: db.Collection("archive")}
}

func (r *ArchiveRepository) ListRecentByChatID(ctx context.Context, chatID int64, limit int64) ([]domain.ArchiveGame, error) {
	opts := options.Find().SetSort(bson.D{{Key: "game_number", Value: -1}}).SetLimit(limit)
	cursor, err := r.collection.Find(ctx, bson.M{"archive_chat_id": chatID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []archiveDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	games := make([]domain.ArchiveGame, 0, len(docs))
	for _, doc := range docs {
		games = append(games, doc.toDomain())
	}
	return games, nil
}

func (r *ArchiveRepository) BuildStatsByChatID(ctx context.Context, chatID int64) (domain.ArchiveStats, error) {
	games, err := r.listAllByChatID(ctx, chatID)
	if err != nil {
		return domain.ArchiveStats{}, err
	}

	stats := domain.ArchiveStats{}
	playerGames := make(map[string]int)

	for i, game := range games {
		stats.GamesCount++
		stats.TotalBuyIns += game.BuyInsTotal
		stats.AveragePlayerCount += float64(game.PlayerCount)

		if i == 0 || game.BuyInsTotal > stats.MaxBank {
			stats.MaxBank = game.BuyInsTotal
		}
		if i == 0 || game.BuyInsTotal < stats.MinBank {
			stats.MinBank = game.BuyInsTotal
		}

		for _, player := range game.Players {
			playerGames[player.Name]++
			if stats.BiggestWinPlayer == "" || player.ProfitBuyIns > stats.BiggestWin {
				stats.BiggestWin = player.ProfitBuyIns
				stats.BiggestWinPlayer = player.Name
			}
		}
	}

	if stats.GamesCount > 0 {
		stats.AverageBank = stats.TotalBuyIns / float64(stats.GamesCount)
		stats.AveragePlayerCount = stats.AveragePlayerCount / float64(stats.GamesCount)
	}

	for name, gamesCount := range playerGames {
		if gamesCount > stats.MostActiveGames || (gamesCount == stats.MostActiveGames && (stats.MostActivePlayer == "" || name < stats.MostActivePlayer)) {
			stats.MostActivePlayer = name
			stats.MostActiveGames = gamesCount
		}
	}

	return stats, nil
}

func (r *ArchiveRepository) BuildPlayerStatsByChatID(ctx context.Context, chatID int64) ([]domain.ArchivePlayerStats, error) {
	games, err := r.listAllByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return aggregateArchivePlayerStats(games), nil
}

func (r *ArchiveRepository) FindByChatIDAndGameNumber(ctx context.Context, chatID int64, gameNumber int) (domain.ArchiveGame, error) {
	var doc archiveDocument
	err := r.collection.FindOne(ctx, bson.M{
		"archive_chat_id": chatID,
		"game_number":     gameNumber,
	}).Decode(&doc)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.ArchiveGame{}, ErrArchiveGameNotFound
	}
	if err != nil {
		return domain.ArchiveGame{}, err
	}
	return doc.toDomain(), nil
}

func (r *ArchiveRepository) FindPlayerHistoryByChatIDAndName(ctx context.Context, chatID int64, name string) (domain.ArchivePlayerHistory, error) {
	games, err := r.listAllByChatID(ctx, chatID)
	if err != nil {
		return domain.ArchivePlayerHistory{}, err
	}

	statsList := aggregateArchivePlayerStats(games)
	targetName := ""
	for _, player := range statsList {
		if normalizeArchiveName(player.Name) == normalizeArchiveName(name) {
			targetName = player.Name
			break
		}
	}
	if targetName == "" {
		return domain.ArchivePlayerHistory{}, ErrArchivePlayerNotFound
	}

	var playerStats domain.ArchivePlayerStats
	for _, player := range statsList {
		if player.Name == targetName {
			playerStats = player
			break
		}
	}

	history := make([]domain.ArchivePlayerHistoryEntry, 0)
	for _, game := range games {
		for _, player := range game.Players {
			if player.Name != targetName {
				continue
			}
			history = append(history, domain.ArchivePlayerHistoryEntry{
				GameNumber:   game.GameNumber,
				SessionDate:  game.SessionDate,
				PlayedAt:     game.PlayedAt,
				BuyIns:       player.BuyIns,
				Result:       player.Result,
				ProfitBuyIns: player.ProfitBuyIns,
			})
			break
		}
	}

	sort.Slice(history, func(i, j int) bool {
		return history[i].GameNumber > history[j].GameNumber
	})

	return domain.ArchivePlayerHistory{
		Player: playerStats,
		Games:  history,
	}, nil
}

func (r *ArchiveRepository) BuildTopByChatID(ctx context.Context, chatID int64, metric domain.ArchiveTopMetric, limit int) ([]domain.ArchivePlayerStats, error) {
	players, err := r.BuildPlayerStatsByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}

	sort.Slice(players, func(i, j int) bool {
		switch metric {
		case domain.ArchiveTopLoss:
			if players[i].TotalProfit == players[j].TotalProfit {
				return players[i].Name < players[j].Name
			}
			return players[i].TotalProfit < players[j].TotalProfit
		case domain.ArchiveTopGames:
			if players[i].GamesCount == players[j].GamesCount {
				return players[i].Name < players[j].Name
			}
			return players[i].GamesCount > players[j].GamesCount
		case domain.ArchiveTopBiggestWin:
			if players[i].BiggestWin == players[j].BiggestWin {
				return players[i].Name < players[j].Name
			}
			return players[i].BiggestWin > players[j].BiggestWin
		default:
			if players[i].TotalProfit == players[j].TotalProfit {
				return players[i].Name < players[j].Name
			}
			return players[i].TotalProfit > players[j].TotalProfit
		}
	})

	if limit > 0 && len(players) > limit {
		players = players[:limit]
	}
	return players, nil
}

func (r *ArchiveRepository) listAllByChatID(ctx context.Context, chatID int64) ([]domain.ArchiveGame, error) {
	opts := options.Find().SetSort(bson.D{{Key: "game_number", Value: 1}})
	cursor, err := r.collection.Find(ctx, bson.M{"archive_chat_id": chatID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []archiveDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	games := make([]domain.ArchiveGame, 0, len(docs))
	for _, doc := range docs {
		games = append(games, doc.toDomain())
	}
	return games, nil
}

type archiveDocument struct {
	ArchiveChatID      int64                   `bson:"archive_chat_id"`
	GameNumber         int                     `bson:"game_number"`
	SessionDate        string                  `bson:"session_date"`
	PlayedAt           time.Time               `bson:"played_at"`
	BuyInsMessageDates []time.Time             `bson:"buyins_message_dates"`
	ResultsMessageDate time.Time               `bson:"results_message_date"`
	PlayerCount        int                     `bson:"player_count"`
	Winners            []string                `bson:"winners"`
	WinnersCount       int                     `bson:"winners_count"`
	TopWinner          string                  `bson:"top_winner"`
	TopWinnerProfit    float64                 `bson:"top_winner_profit"`
	BuyInsTotal        float64                 `bson:"buyins_total"`
	ResultsTotal       float64                 `bson:"results_total"`
	Difference         float64                 `bson:"difference"`
	MathMatches        bool                    `bson:"math_matches"`
	Players            []archivePlayerDocument `bson:"players"`
}

type archivePlayerDocument struct {
	Name         string  `bson:"name"`
	BuyIns       float64 `bson:"buyins"`
	Result       float64 `bson:"result"`
	ProfitBuyIns float64 `bson:"profit_buyins"`
}

func (d archiveDocument) toDomain() domain.ArchiveGame {
	players := make([]domain.ArchivePlayerResult, 0, len(d.Players))
	for _, player := range d.Players {
		players = append(players, domain.ArchivePlayerResult{
			Name:         player.Name,
			BuyIns:       player.BuyIns,
			Result:       player.Result,
			ProfitBuyIns: player.ProfitBuyIns,
		})
	}

	return domain.ArchiveGame{
		ArchiveChatID:   d.ArchiveChatID,
		GameNumber:      d.GameNumber,
		SessionDate:     d.SessionDate,
		PlayedAt:        d.PlayedAt,
		PlayerCount:     d.PlayerCount,
		Winners:         append([]string(nil), d.Winners...),
		WinnersCount:    d.WinnersCount,
		TopWinner:       d.TopWinner,
		TopWinnerProfit: d.TopWinnerProfit,
		BuyInsTotal:     d.BuyInsTotal,
		ResultsTotal:    d.ResultsTotal,
		Difference:      d.Difference,
		MathMatches:     d.MathMatches,
		Players:         players,
	}
}

func aggregateArchivePlayerStats(games []domain.ArchiveGame) []domain.ArchivePlayerStats {
	aggregates := make(map[string]*domain.ArchivePlayerStats)

	for _, game := range games {
		for _, player := range game.Players {
			aggregate, ok := aggregates[player.Name]
			if !ok {
				aggregate = &domain.ArchivePlayerStats{
					Name:        player.Name,
					BiggestLoss: player.ProfitBuyIns,
				}
				aggregates[player.Name] = aggregate
			}

			aggregate.GamesCount++
			aggregate.TotalBuyIns += player.BuyIns
			aggregate.TotalWon += player.Result
			aggregate.TotalProfit += player.ProfitBuyIns

			if player.ProfitBuyIns > aggregate.BiggestWin {
				aggregate.BiggestWin = player.ProfitBuyIns
			}
			if player.ProfitBuyIns < aggregate.BiggestLoss {
				aggregate.BiggestLoss = player.ProfitBuyIns
			}

			switch {
			case player.ProfitBuyIns > 0:
				aggregate.WinningGames++
			case player.ProfitBuyIns < 0:
				aggregate.LosingGames++
			default:
				aggregate.NeutralGames++
			}
		}
	}

	result := make([]domain.ArchivePlayerStats, 0, len(aggregates))
	for _, aggregate := range aggregates {
		if aggregate.GamesCount > 0 {
			aggregate.AverageProfit = aggregate.TotalProfit / float64(aggregate.GamesCount)
		}
		result = append(result, *aggregate)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].GamesCount == result[j].GamesCount {
			if result[i].TotalProfit == result[j].TotalProfit {
				return result[i].Name < result[j].Name
			}
			return result[i].TotalProfit > result[j].TotalProfit
		}
		return result[i].GamesCount > result[j].GamesCount
	})

	return result
}

func normalizeArchiveName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
