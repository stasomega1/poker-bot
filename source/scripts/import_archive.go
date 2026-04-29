package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	archiveChatID     int64 = 1817456467
	archiveCollection       = "archive"
)

type archiveInput struct {
	Games []archiveGameInput `json:"games"`
}

type archiveGameInput struct {
	SessionDate        string             `json:"session_date"`
	BuyInsMessageDates []string           `json:"buyins_message_dates"`
	ResultsMessageDate string             `json:"results_message_date"`
	BuyIns             map[string]float64 `json:"buyins"`
	Results            map[string]float64 `json:"results"`
	ProfitBuyIns       map[string]float64 `json:"profit_buyins"`
	MathMatches        bool               `json:"math_matches"`
	MathDetails        archiveMathDetails `json:"math_details"`
}

type archiveMathDetails struct {
	BuyInsTotal  float64 `json:"buyins_total"`
	ResultsTotal float64 `json:"results_total"`
	Difference   float64 `json:"difference"`
}

type archivePlayer struct {
	Name         string  `bson:"name"`
	BuyIns       float64 `bson:"buyins"`
	Result       float64 `bson:"result"`
	ProfitBuyIns float64 `bson:"profit_buyins"`
}

type archiveDocument struct {
	ArchiveChatID      int64           `bson:"archive_chat_id"`
	GameNumber         int             `bson:"game_number"`
	SessionDate        string          `bson:"session_date"`
	PlayedAt           time.Time       `bson:"played_at"`
	BuyInsMessageDates []time.Time     `bson:"buyins_message_dates"`
	ResultsMessageDate time.Time       `bson:"results_message_date"`
	PlayerCount        int             `bson:"player_count"`
	Winners            []string        `bson:"winners"`
	WinnersCount       int             `bson:"winners_count"`
	TopWinner          string          `bson:"top_winner"`
	TopWinnerProfit    float64         `bson:"top_winner_profit"`
	BuyInsTotal        float64         `bson:"buyins_total"`
	ResultsTotal       float64         `bson:"results_total"`
	Difference         float64         `bson:"difference"`
	MathMatches        bool            `bson:"math_matches"`
	Players            []archivePlayer `bson:"players"`
	ImportedAt         time.Time       `bson:"imported_at"`
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

	jsonPath, err := resolveJSONPath(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		log.Fatalf("read %s: %v", jsonPath, err)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	var input archiveInput
	if err := json.Unmarshal(data, &input); err != nil {
		log.Fatalf("unmarshal json: %v", err)
	}

	documents, err := buildArchiveDocuments(input.Games)
	if err != nil {
		log.Fatalf("build archive documents: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer func() {
		_ = client.Disconnect(context.Background())
	}()

	collection := client.Database(mongoDB).Collection(archiveCollection)

	if _, err := collection.DeleteMany(ctx, bson.M{"archive_chat_id": archiveChatID}); err != nil {
		log.Fatalf("clear existing archive: %v", err)
	}

	if len(documents) > 0 {
		payload := make([]interface{}, 0, len(documents))
		for _, document := range documents {
			payload = append(payload, document)
		}

		if _, err := collection.InsertMany(ctx, payload); err != nil {
			log.Fatalf("insert archive documents: %v", err)
		}
	}

	if err := ensureArchiveIndexes(ctx, collection); err != nil {
		log.Fatalf("ensure indexes: %v", err)
	}

	fmt.Printf("archive import completed: %d games\n", len(documents))
}

func buildArchiveDocuments(games []archiveGameInput) ([]archiveDocument, error) {
	type sortableGame struct {
		input      archiveGameInput
		playedAt   time.Time
		buyInDates []time.Time
	}

	items := make([]sortableGame, 0, len(games))
	for _, game := range games {
		buyInDates := make([]time.Time, 0, len(game.BuyInsMessageDates))
		for _, raw := range game.BuyInsMessageDates {
			parsed, err := parseArchiveTime(raw)
			if err != nil {
				return nil, fmt.Errorf("parse buyins_message_date %q: %w", raw, err)
			}
			buyInDates = append(buyInDates, parsed)
		}

		playedAt, err := resolvePlayedAt(game.ResultsMessageDate, buyInDates)
		if err != nil {
			return nil, err
		}

		items = append(items, sortableGame{
			input:      game,
			playedAt:   playedAt,
			buyInDates: buyInDates,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].playedAt.Equal(items[j].playedAt) {
			return items[i].input.SessionDate < items[j].input.SessionDate
		}
		return items[i].playedAt.Before(items[j].playedAt)
	})

	now := time.Now().UTC()
	result := make([]archiveDocument, 0, len(items))

	for idx, item := range items {
		players := buildPlayers(item.input)
		winners, topWinner, topWinnerProfit := buildWinners(players)

		result = append(result, archiveDocument{
			ArchiveChatID:      archiveChatID,
			GameNumber:         idx + 1,
			SessionDate:        item.input.SessionDate,
			PlayedAt:           item.playedAt,
			BuyInsMessageDates: item.buyInDates,
			ResultsMessageDate: item.playedAt,
			PlayerCount:        len(players),
			Winners:            winners,
			WinnersCount:       len(winners),
			TopWinner:          topWinner,
			TopWinnerProfit:    topWinnerProfit,
			BuyInsTotal:        item.input.MathDetails.BuyInsTotal,
			ResultsTotal:       item.input.MathDetails.ResultsTotal,
			Difference:         item.input.MathDetails.Difference,
			MathMatches:        item.input.MathMatches,
			Players:            players,
			ImportedAt:         now,
		})
	}

	return result, nil
}

func buildPlayers(game archiveGameInput) []archivePlayer {
	names := make([]string, 0, len(game.BuyIns))
	for name := range game.BuyIns {
		names = append(names, name)
	}
	sort.Strings(names)

	players := make([]archivePlayer, 0, len(names))
	for _, name := range names {
		players = append(players, archivePlayer{
			Name:         name,
			BuyIns:       game.BuyIns[name],
			Result:       game.Results[name],
			ProfitBuyIns: game.ProfitBuyIns[name],
		})
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].ProfitBuyIns == players[j].ProfitBuyIns {
			return players[i].Name < players[j].Name
		}
		return players[i].ProfitBuyIns > players[j].ProfitBuyIns
	})

	return players
}

func buildWinners(players []archivePlayer) ([]string, string, float64) {
	winners := make([]string, 0)
	topWinner := ""
	topWinnerProfit := 0.0

	for _, player := range players {
		if player.ProfitBuyIns <= 0 {
			continue
		}
		winners = append(winners, player.Name)
		if topWinner == "" || player.ProfitBuyIns > topWinnerProfit {
			topWinner = player.Name
			topWinnerProfit = player.ProfitBuyIns
		}
	}

	sort.Strings(winners)
	return winners, topWinner, topWinnerProfit
}

func parseArchiveTime(value string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05", value)
}

func resolvePlayedAt(resultsMessageDate string, buyInDates []time.Time) (time.Time, error) {
	if strings.TrimSpace(resultsMessageDate) != "" {
		playedAt, err := parseArchiveTime(resultsMessageDate)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse results_message_date %q: %w", resultsMessageDate, err)
		}
		return playedAt, nil
	}

	if len(buyInDates) == 0 {
		return time.Time{}, fmt.Errorf("results_message_date is empty and buyins_message_dates is empty")
	}

	latest := buyInDates[0]
	for _, buyInDate := range buyInDates[1:] {
		if buyInDate.After(latest) {
			latest = buyInDate
		}
	}

	return latest, nil
}

func ensureArchiveIndexes(ctx context.Context, collection *mongo.Collection) error {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "archive_chat_id", Value: 1}, {Key: "game_number", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("archive_chat_game_number"),
		},
		{
			Keys:    bson.D{{Key: "archive_chat_id", Value: 1}, {Key: "session_date", Value: 1}},
			Options: options.Index().SetName("archive_chat_session_date"),
		},
		{
			Keys:    bson.D{{Key: "archive_chat_id", Value: 1}, {Key: "players.name", Value: 1}},
			Options: options.Index().SetName("archive_chat_player_name"),
		},
	}

	_, err := collection.Indexes().CreateMany(ctx, models)
	return err
}

func resolveJSONPath(args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		path := strings.TrimSpace(args[0])
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("json file not found: %s", path)
		}
		return path, nil
	}

	candidates := []string{
		"results_2025_plus_final.json",
		"../results_2025_plus_final.json",
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("results_2025_plus_final.json not found; pass path explicitly")
}
