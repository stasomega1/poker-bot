package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"poker-bot/internal/domain"
	"poker-bot/internal/service"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	sourceArchiveChatID int64 = 1817456467
	targetChatID        int64 = -1001817456467
	targetChatTitle           = "Покер на эстрогенах"
	defaultBuyInPrice         = "2000"
)

type archiveMigrationDocument struct {
	ArchiveChatID int64                    `bson:"archive_chat_id"`
	GameNumber    int                      `bson:"game_number"`
	SessionDate   string                   `bson:"session_date"`
	PlayedAt      time.Time                `bson:"played_at"`
	Players       []archiveMigrationPlayer `bson:"players"`
}

type archiveMigrationPlayer struct {
	Name         string  `bson:"name"`
	BuyIns       float64 `bson:"buyins"`
	Result       float64 `bson:"result"`
	ProfitBuyIns float64 `bson:"profit_buyins"`
}

type gameMigrationDocument struct {
	ID                interface{} `bson:"_id,omitempty"`
	ChatID            int64       `bson:"chat_id"`
	SessionDate       string      `bson:"session_date"`
	SourceBuyInsText  string      `bson:"source_buyins_text"`
	SourceResultsText string      `bson:"source_results_text"`
}

func main() {
	_ = godotenv.Load(".env", "../.env", "../../.env")

	mongoURI := strings.TrimSpace(os.Getenv("MONGODB_URI"))
	mongoDB := strings.TrimSpace(os.Getenv("MONGODB_DB"))
	if mongoURI == "" {
		log.Fatal("MONGODB_URI is required")
	}
	if mongoDB == "" {
		log.Fatal("MONGODB_DB is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer func() {
		_ = client.Disconnect(context.Background())
	}()

	db := client.Database(mongoDB)
	archiveCollection := db.Collection("archive")
	gamesCollection := db.Collection("games")

	cursor, err := archiveCollection.Find(ctx, bson.M{"archive_chat_id": sourceArchiveChatID}, options.Find().SetSort(bson.D{{Key: "game_number", Value: 1}}))
	if err != nil {
		log.Fatalf("find archive games: %v", err)
	}
	defer cursor.Close(ctx)

	var archiveGames []archiveMigrationDocument
	if err := cursor.All(ctx, &archiveGames); err != nil {
		log.Fatalf("decode archive games: %v", err)
	}

	calculator := service.NewSettlementCalculator()
	price := decimal.RequireFromString(defaultBuyInPrice)

	var inserted int
	var skipped int
	var invalid []int

	for _, archiveGame := range archiveGames {
		request, err := buildGameRequest(archiveGame, price)
		if err != nil {
			log.Fatalf("build request for archive game #%d: %v", archiveGame.GameNumber, err)
		}

		exists, err := gameExists(ctx, gamesCollection, request)
		if err != nil {
			log.Fatalf("check duplicate for archive game #%d: %v", archiveGame.GameNumber, err)
		}
		if exists {
			skipped++
			continue
		}

		game, err := calculator.BuildGame(request)
		if err != nil {
			invalid = append(invalid, archiveGame.GameNumber)
			log.Printf("skip archive game #%d: %v", archiveGame.GameNumber, err)
			continue
		}

		game.ChatID = targetChatID
		game.ChatTitle = targetChatTitle
		game.CreatedAt = archiveGame.PlayedAt
		game.CreatedByUserID = 0
		game.CreatedByName = "archive import"
		game.SourceBuyInsMessageID = 0
		game.SourceResultsMessageID = 0
		game.SourceCommandMessageID = 0

		document := gameDocumentFromDomain(game)
		if _, err := gamesCollection.InsertOne(ctx, document); err != nil {
			log.Fatalf("insert archive game #%d: %v", archiveGame.GameNumber, err)
		}
		inserted++
	}

	fmt.Printf("archive migration completed: inserted=%d skipped=%d total=%d\n", inserted, skipped, len(archiveGames))
	if len(invalid) > 0 {
		fmt.Printf("invalid archive games skipped: %v\n", invalid)
	}
}

func buildGameRequest(archiveGame archiveMigrationDocument, price decimal.Decimal) (domain.GameRequest, error) {
	buyInsEntries := append([]archiveMigrationPlayer(nil), archiveGame.Players...)
	sort.Slice(buyInsEntries, func(i, j int) bool {
		return buyInsEntries[i].Name < buyInsEntries[j].Name
	})

	buyIns := make([]domain.PlayerInput, 0, len(buyInsEntries))
	for _, player := range buyInsEntries {
		buyIns = append(buyIns, domain.PlayerInput{
			Name:   player.Name,
			Amount: decimal.NewFromFloat(player.BuyIns),
		})
	}

	winnerEntries := make([]archiveMigrationPlayer, 0)
	for _, player := range archiveGame.Players {
		if player.Result > 0 {
			winnerEntries = append(winnerEntries, player)
		}
	}
	sort.Slice(winnerEntries, func(i, j int) bool {
		return winnerEntries[i].Name < winnerEntries[j].Name
	})

	winners := make([]domain.PlayerInput, 0, len(winnerEntries))
	for _, player := range winnerEntries {
		winners = append(winners, domain.PlayerInput{
			Name:   player.Name,
			Amount: decimal.NewFromFloat(player.Result),
		})
	}

	return domain.GameRequest{
		ChatID:           targetChatID,
		ChatTitle:        targetChatTitle,
		SessionDate:      archiveGame.SessionDate,
		BuyInPriceKZT:    price,
		BuyInsMessageID:  0,
		ResultsMessageID: 0,
		CommandMessageID: 0,
		BuyInsText:       buildBuyInsText(buyIns),
		ResultsText:      buildResultsText(winners),
		CreateUserID:     0,
		CreateUserName:   "archive import",
		BuyIns:           buyIns,
		Winners:          winners,
	}, nil
}

func buildBuyInsText(players []domain.PlayerInput) string {
	lines := make([]string, 0, len(players))
	for _, player := range players {
		lines = append(lines, fmt.Sprintf("%s %s", player.Name, player.Amount.String()))
	}
	return strings.Join(lines, "\n")
}

func buildResultsText(players []domain.PlayerInput) string {
	lines := make([]string, 0, len(players))
	for _, player := range players {
		lines = append(lines, fmt.Sprintf("%s %s", player.Name, player.Amount.String()))
	}
	return strings.Join(lines, "\n")
}

func gameExists(ctx context.Context, collection *mongo.Collection, request domain.GameRequest) (bool, error) {
	filter := bson.M{
		"chat_id":             targetChatID,
		"session_date":        request.SessionDate,
		"source_buyins_text":  request.BuyInsText,
		"source_results_text": request.ResultsText,
	}

	var existing gameMigrationDocument
	err := collection.FindOne(ctx, filter).Decode(&existing)
	if err == nil {
		return true, nil
	}
	if err == mongo.ErrNoDocuments {
		return false, nil
	}
	return false, err
}

type gameDocument struct {
	ChatID                 int64                `bson:"chat_id"`
	ChatTitle              string               `bson:"chat_title"`
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
