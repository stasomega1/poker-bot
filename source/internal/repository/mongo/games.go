package mongo

import (
	"context"
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
				aggregate = &domain.PlayerStats{Name: player.Name}
				aggregates[player.Name] = aggregate
			}

			aggregate.GamesCount++
			aggregate.TotalBuyIns = aggregate.TotalBuyIns.Add(player.BuyIns)
			aggregate.TotalWonBuyIns = aggregate.TotalWonBuyIns.Add(player.WonBuyIns)
			aggregate.TotalProfit = aggregate.TotalProfit.Add(player.ProfitBuyIns)
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
	BuyInPriceKZT          string               `bson:"buyin_price_kzt"`
	SourceBuyInsMessageID  int                  `bson:"source_buyins_message_id"`
	SourceResultsMessageID int                  `bson:"source_results_message_id"`
	SourceCommandMessageID int                  `bson:"source_command_message_id"`
	SourceBuyInsText       string               `bson:"source_buyins_text"`
	SourceResultsText      string               `bson:"source_results_text"`
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
		BuyInPriceKZT:          game.BuyInPriceKZT.String(),
		SourceBuyInsMessageID:  game.SourceBuyInsMessageID,
		SourceResultsMessageID: game.SourceResultsMessageID,
		SourceCommandMessageID: game.SourceCommandMessageID,
		SourceBuyInsText:       game.SourceBuyInsText,
		SourceResultsText:      game.SourceResultsText,
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
		BuyInPriceKZT:          price,
		SourceBuyInsMessageID:  d.SourceBuyInsMessageID,
		SourceResultsMessageID: d.SourceResultsMessageID,
		SourceCommandMessageID: d.SourceCommandMessageID,
		SourceBuyInsText:       d.SourceBuyInsText,
		SourceResultsText:      d.SourceResultsText,
		Players:                players,
		Settlements:            settlements,
		TotalBuyIns:            totalBuyIns,
		CreatedAt:              d.CreatedAt,
		CreatedByUserID:        d.CreatedByUserID,
		CreatedByName:          d.CreatedByName,
	}, nil
}
