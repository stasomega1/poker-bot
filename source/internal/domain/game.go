package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type AllowedChat struct {
	ChatID        int64           `bson:"chat_id"`
	Title         string          `bson:"title"`
	IsActive      bool            `bson:"is_active"`
	BuyInPriceKZT decimal.Decimal `bson:"buyin_price_kzt"`
	CreatedAt     time.Time       `bson:"created_at"`
	UpdatedAt     time.Time       `bson:"updated_at"`
}

type Game struct {
	ID                     string          `bson:"_id,omitempty"`
	ChatID                 int64           `bson:"chat_id"`
	ChatTitle              string          `bson:"chat_title"`
	GameNumber             int             `bson:"game_number"`
	SessionDate            string          `bson:"session_date"`
	BuyInPriceKZT          decimal.Decimal `bson:"buyin_price_kzt"`
	SourceBuyInsMessageID  int             `bson:"source_buyins_message_id"`
	SourceResultsMessageID int             `bson:"source_results_message_id"`
	SourceCommandMessageID int             `bson:"source_command_message_id"`
	SourceBuyInsText       string          `bson:"source_buyins_text"`
	SourceResultsText      string          `bson:"source_results_text"`
	PlayerCount            int             `bson:"player_count"`
	Winners                []string        `bson:"winners"`
	WinnersCount           int             `bson:"winners_count"`
	TopWinner              string          `bson:"top_winner"`
	TopWinnerProfit        decimal.Decimal `bson:"top_winner_profit"`
	ResultsTotal           decimal.Decimal `bson:"results_total"`
	Players                []PlayerResult  `bson:"players"`
	Settlements            []Settlement    `bson:"settlements"`
	TotalBuyIns            decimal.Decimal `bson:"total_buyins"`
	CreatedAt              time.Time       `bson:"created_at"`
	CreatedByUserID        int64           `bson:"created_by_user_id"`
	CreatedByName          string          `bson:"created_by_name"`
}

type PlayerInput struct {
	Name   string
	Amount decimal.Decimal
}

type PlayerResult struct {
	Name         string          `bson:"name"`
	BuyIns       decimal.Decimal `bson:"buyins"`
	WonBuyIns    decimal.Decimal `bson:"won_buyins"`
	ProfitBuyIns decimal.Decimal `bson:"profit_buyins"`
	ProfitKZT    decimal.Decimal `bson:"profit_kzt"`
}

type Settlement struct {
	FromName     string          `bson:"from_name"`
	ToName       string          `bson:"to_name"`
	AmountBuyIns decimal.Decimal `bson:"amount_buyins"`
	AmountKZT    decimal.Decimal `bson:"amount_kzt"`
}

type GameRequest struct {
	ChatID           int64
	ChatTitle        string
	SessionDate      string
	BuyInPriceKZT    decimal.Decimal
	BuyInsMessageID  int
	ResultsMessageID int
	CommandMessageID int
	BuyInsText       string
	ResultsText      string
	CreateUserID     int64
	CreateUserName   string
	BuyIns           []PlayerInput
	Winners          []PlayerInput
}

type Stats struct {
	GamesCount       int64
	TotalBuyIns      decimal.Decimal
	AverageBank      decimal.Decimal
	BiggestWin       decimal.Decimal
	BiggestWinPlayer string
	BiggestLoss      decimal.Decimal
}

type PlayerStats struct {
	Name           string
	GamesCount     int64
	TotalBuyIns    decimal.Decimal
	TotalWonBuyIns decimal.Decimal
	TotalProfit    decimal.Decimal
	AverageProfit  decimal.Decimal
	BiggestWin     decimal.Decimal
	BiggestLoss    decimal.Decimal
	WinningGames   int64
	LosingGames    int64
	NeutralGames   int64
}

type NumberedGame struct {
	Game Game
}

type GamePlayerHistoryEntry struct {
	GameNumber   int
	SessionDate  string
	CreatedAt    time.Time
	BuyIns       decimal.Decimal
	WonBuyIns    decimal.Decimal
	ProfitBuyIns decimal.Decimal
}

type GamePlayerHistory struct {
	Player PlayerStats
	Games  []GamePlayerHistoryEntry
}
