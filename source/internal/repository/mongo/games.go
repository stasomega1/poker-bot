package mongo

import (
	"context"
	"errors"
	"sort"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/bson"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type GameRepository struct {
	collection *driver.Collection
}

func NewGameRepository(db *driver.Database) *GameRepository {
	return &GameRepository{collection: db.Collection("games")}
}

func (r *GameRepository) Create(ctx context.Context, game domain.Game) error {
	_, err := r.collection.InsertOne(ctx, gameDocumentFromDomain(game))
	return err
}

func (r *GameRepository) ListRecentByChatID(ctx context.Context, chatID int64, limit int64) ([]domain.Game, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit)
	cursor, err := r.collection.Find(ctx, bson.M{"chat_id": chatID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var documents []gameDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, err
	}

	games := make([]domain.Game, 0, len(documents))
	for _, document := range documents {
		game, err := document.toDomain()
		if err != nil {
			return nil, err
		}
		games = append(games, game)
	}

	return games, nil
}

func (r *GameRepository) ListAllByChatID(ctx context.Context, chatID int64) ([]domain.Game, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})
	cursor, err := r.collection.Find(ctx, bson.M{"chat_id": chatID}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var documents []gameDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, err
	}

	games := make([]domain.Game, 0, len(documents))
	for _, document := range documents {
		game, err := document.toDomain()
		if err != nil {
			return nil, err
		}
		games = append(games, game)
	}

	return games, nil
}

func (r *GameRepository) FindByChatIDAndGameNumber(ctx context.Context, chatID int64, gameNumber int) (domain.Game, error) {
	var document gameDocument
	err := r.collection.FindOne(ctx, bson.M{"chat_id": chatID, "game_number": gameNumber}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.Game{}, ErrGameNotFound
	}
	if err != nil {
		return domain.Game{}, err
	}
	return document.toDomain()
}

func (r *GameRepository) NextGameNumber(ctx context.Context, chatID int64) (int, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "game_number", Value: -1}})
	var document gameDocument
	err := r.collection.FindOne(ctx, bson.M{"chat_id": chatID}, opts).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return document.GameNumber + 1, nil
}

func (r *GameRepository) BackfillGameNumbers(ctx context.Context) error {
	chatIDs, err := r.collection.Distinct(ctx, "chat_id", bson.M{})
	if err != nil {
		return err
	}

	for _, rawChatID := range chatIDs {
		chatID, ok := rawChatID.(int64)
		if !ok {
			if numberLong, ok := rawChatID.(interface{ Int64() int64 }); ok {
				chatID = numberLong.Int64()
			} else {
				continue
			}
		}

		opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}})
		cursor, err := r.collection.Find(ctx, bson.M{"chat_id": chatID}, opts)
		if err != nil {
			return err
		}

		var documents []gameDocument
		if err := cursor.All(ctx, &documents); err != nil {
			cursor.Close(ctx)
			return err
		}
		cursor.Close(ctx)

		for i, document := range documents {
			if _, err := r.collection.UpdateOne(ctx, bson.M{
				"chat_id":                   chatID,
				"source_buyins_message_id":  document.SourceBuyInsMessageID,
				"source_results_message_id": document.SourceResultsMessageID,
				"source_command_message_id": document.SourceCommandMessageID,
				"created_at":                document.CreatedAt,
			}, bson.M{
				"$set": bson.M{"game_number": i + 1},
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *GameRepository) BuildStatsByChatID(ctx context.Context, chatID int64) (domain.Stats, error) {
	cursor, err := r.collection.Find(ctx, bson.M{"chat_id": chatID})
	if err != nil {
		return domain.Stats{}, err
	}
	defer cursor.Close(ctx)

	stats := domain.Stats{}
	var hasPlayers bool

	for cursor.Next(ctx) {
		var document gameDocument
		if err := cursor.Decode(&document); err != nil {
			return domain.Stats{}, err
		}

		game, err := document.toDomain()
		if err != nil {
			return domain.Stats{}, err
		}

		stats.GamesCount++
		stats.TotalBuyIns = stats.TotalBuyIns.Add(game.TotalBuyIns)

		for _, player := range game.Players {
			if !hasPlayers || player.ProfitBuyIns.GreaterThan(stats.BiggestWin) {
				stats.BiggestWin = player.ProfitBuyIns
				stats.BiggestWinPlayer = player.Name
			}
			if !hasPlayers || player.ProfitBuyIns.LessThan(stats.BiggestLoss) {
				stats.BiggestLoss = player.ProfitBuyIns
			}
			hasPlayers = true
		}
	}

	if stats.GamesCount > 0 {
		stats.AverageBank = stats.TotalBuyIns.Div(decimal.NewFromInt(stats.GamesCount))
	}

	return stats, cursor.Err()
}

func (r *GameRepository) BuildPlayerStatsByChatID(ctx context.Context, chatID int64) ([]domain.PlayerStats, error) {
	cursor, err := r.collection.Find(ctx, bson.M{"chat_id": chatID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	aggregates := make(map[string]*domain.PlayerStats)

	for cursor.Next(ctx) {
		var document gameDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}

		game, err := document.toDomain()
		if err != nil {
			return nil, err
		}

		for _, player := range game.Players {
			aggregate, ok := aggregates[player.Name]
			if !ok {
				aggregate = &domain.PlayerStats{
					Name:        player.Name,
					BiggestLoss: player.ProfitBuyIns,
				}
				aggregates[player.Name] = aggregate
			}

			aggregate.GamesCount++
			aggregate.TotalBuyIns = aggregate.TotalBuyIns.Add(player.BuyIns)
			aggregate.TotalWonBuyIns = aggregate.TotalWonBuyIns.Add(player.WonBuyIns)
			aggregate.TotalProfit = aggregate.TotalProfit.Add(player.ProfitBuyIns)

			if player.ProfitBuyIns.GreaterThan(aggregate.BiggestWin) {
				aggregate.BiggestWin = player.ProfitBuyIns
			}
			if player.ProfitBuyIns.LessThan(aggregate.BiggestLoss) {
				aggregate.BiggestLoss = player.ProfitBuyIns
			}

			switch {
			case player.ProfitBuyIns.GreaterThan(decimal.Zero):
				aggregate.WinningGames++
			case player.ProfitBuyIns.LessThan(decimal.Zero):
				aggregate.LosingGames++
			default:
				aggregate.NeutralGames++
			}
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, err
	}

	result := make([]domain.PlayerStats, 0, len(aggregates))
	for _, aggregate := range aggregates {
		if aggregate.GamesCount > 0 {
			aggregate.AverageProfit = aggregate.TotalProfit.Div(decimal.NewFromInt(aggregate.GamesCount))
		}
		result = append(result, *aggregate)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalProfit.Equal(result[j].TotalProfit) {
			return result[i].Name < result[j].Name
		}
		return result[i].TotalProfit.GreaterThan(result[j].TotalProfit)
	})

	return result, nil
}

type gameDocument struct {
	ChatID                 int64                `bson:"chat_id"`
	ChatTitle              string               `bson:"chat_title"`
	GameNumber             int                  `bson:"game_number"`
	SessionDate            string               `bson:"session_date"`
	BuyInPriceKZT          string               `bson:"buyin_price_kzt"`
	SourceBuyInsMessageID  int                  `bson:"source_buyins_message_id"`
	SourceResultsMessageID int                  `bson:"source_results_message_id"`
	SourceCommandMessageID int                  `bson:"source_command_message_id"`
	SourceBuyInsText       string               `bson:"source_buyins_text"`
	SourceResultsText      string               `bson:"source_results_text"`
	PlayerCount            int                  `bson:"player_count"`
	Winners                []string             `bson:"winners"`
	WinnersCount           int                  `bson:"winners_count"`
	TopWinner              string               `bson:"top_winner"`
	TopWinnerProfit        string               `bson:"top_winner_profit"`
	ResultsTotal           string               `bson:"results_total"`
	Players                []playerDocument     `bson:"players"`
	Settlements            []settlementDocument `bson:"settlements"`
	TotalBuyIns            string               `bson:"total_buyins"`
	CreatedAt              time.Time            `bson:"created_at"`
	CreatedByUserID        int64                `bson:"created_by_user_id"`
	CreatedByName          string               `bson:"created_by_name"`
}

type playerDocument struct {
	Name         string `bson:"name"`
	BuyIns       string `bson:"buyins"`
	WonBuyIns    string `bson:"won_buyins"`
	ProfitBuyIns string `bson:"profit_buyins"`
	ProfitKZT    string `bson:"profit_kzt"`
}

type settlementDocument struct {
	FromName     string `bson:"from_name"`
	ToName       string `bson:"to_name"`
	AmountBuyIns string `bson:"amount_buyins"`
	AmountKZT    string `bson:"amount_kzt"`
}

func gameDocumentFromDomain(game domain.Game) gameDocument {
	players := make([]playerDocument, 0, len(game.Players))
	for _, player := range game.Players {
		players = append(players, playerDocument{
			Name:         player.Name,
			BuyIns:       player.BuyIns.String(),
			WonBuyIns:    player.WonBuyIns.String(),
			ProfitBuyIns: player.ProfitBuyIns.String(),
			ProfitKZT:    player.ProfitKZT.String(),
		})
	}

	settlements := make([]settlementDocument, 0, len(game.Settlements))
	for _, settlement := range game.Settlements {
		settlements = append(settlements, settlementDocument{
			FromName:     settlement.FromName,
			ToName:       settlement.ToName,
			AmountBuyIns: settlement.AmountBuyIns.String(),
			AmountKZT:    settlement.AmountKZT.String(),
		})
	}

	return gameDocument{
		ChatID:                 game.ChatID,
		ChatTitle:              game.ChatTitle,
		GameNumber:             game.GameNumber,
		SessionDate:            game.SessionDate,
		BuyInPriceKZT:          game.BuyInPriceKZT.String(),
		SourceBuyInsMessageID:  game.SourceBuyInsMessageID,
		SourceResultsMessageID: game.SourceResultsMessageID,
		SourceCommandMessageID: game.SourceCommandMessageID,
		SourceBuyInsText:       game.SourceBuyInsText,
		SourceResultsText:      game.SourceResultsText,
		PlayerCount:            game.PlayerCount,
		Winners:                game.Winners,
		WinnersCount:           game.WinnersCount,
		TopWinner:              game.TopWinner,
		TopWinnerProfit:        game.TopWinnerProfit.String(),
		ResultsTotal:           game.ResultsTotal.String(),
		Players:                players,
		Settlements:            settlements,
		TotalBuyIns:            game.TotalBuyIns.String(),
		CreatedAt:              game.CreatedAt,
		CreatedByUserID:        game.CreatedByUserID,
		CreatedByName:          game.CreatedByName,
	}
}

func (d gameDocument) toDomain() (domain.Game, error) {
	price, err := decimal.NewFromString(d.BuyInPriceKZT)
	if err != nil {
		return domain.Game{}, err
	}
	totalBuyIns, err := decimal.NewFromString(d.TotalBuyIns)
	if err != nil {
		return domain.Game{}, err
	}
	topWinnerProfit := decimal.Zero
	if d.TopWinnerProfit != "" {
		topWinnerProfit, err = decimal.NewFromString(d.TopWinnerProfit)
		if err != nil {
			return domain.Game{}, err
		}
	}
	resultsTotal := decimal.Zero
	if d.ResultsTotal != "" {
		resultsTotal, err = decimal.NewFromString(d.ResultsTotal)
		if err != nil {
			return domain.Game{}, err
		}
	}

	players := make([]domain.PlayerResult, 0, len(d.Players))
	for _, player := range d.Players {
		buyIns, err := decimal.NewFromString(player.BuyIns)
		if err != nil {
			return domain.Game{}, err
		}
		wonBuyIns, err := decimal.NewFromString(player.WonBuyIns)
		if err != nil {
			return domain.Game{}, err
		}
		profitBuyIns, err := decimal.NewFromString(player.ProfitBuyIns)
		if err != nil {
			return domain.Game{}, err
		}
		profitKZT, err := decimal.NewFromString(player.ProfitKZT)
		if err != nil {
			return domain.Game{}, err
		}

		players = append(players, domain.PlayerResult{
			Name:         player.Name,
			BuyIns:       buyIns,
			WonBuyIns:    wonBuyIns,
			ProfitBuyIns: profitBuyIns,
			ProfitKZT:    profitKZT,
		})
	}

	settlements := make([]domain.Settlement, 0, len(d.Settlements))
	for _, settlement := range d.Settlements {
		amountBuyIns, err := decimal.NewFromString(settlement.AmountBuyIns)
		if err != nil {
			return domain.Game{}, err
		}
		amountKZT, err := decimal.NewFromString(settlement.AmountKZT)
		if err != nil {
			return domain.Game{}, err
		}

		settlements = append(settlements, domain.Settlement{
			FromName:     settlement.FromName,
			ToName:       settlement.ToName,
			AmountBuyIns: amountBuyIns,
			AmountKZT:    amountKZT,
		})
	}

	return domain.Game{
		ChatID:                 d.ChatID,
		ChatTitle:              d.ChatTitle,
		GameNumber:             d.GameNumber,
		SessionDate:            d.SessionDate,
		BuyInPriceKZT:          price,
		SourceBuyInsMessageID:  d.SourceBuyInsMessageID,
		SourceResultsMessageID: d.SourceResultsMessageID,
		SourceCommandMessageID: d.SourceCommandMessageID,
		SourceBuyInsText:       d.SourceBuyInsText,
		SourceResultsText:      d.SourceResultsText,
		PlayerCount:            d.PlayerCount,
		Winners:                d.Winners,
		WinnersCount:           d.WinnersCount,
		TopWinner:              d.TopWinner,
		TopWinnerProfit:        topWinnerProfit,
		ResultsTotal:           resultsTotal,
		Players:                players,
		Settlements:            settlements,
		TotalBuyIns:            totalBuyIns,
		CreatedAt:              d.CreatedAt,
		CreatedByUserID:        d.CreatedByUserID,
		CreatedByName:          d.CreatedByName,
	}, nil
}
