package domain

import "time"

type ArchiveGame struct {
	ArchiveChatID   int64
	GameNumber      int
	SessionDate     string
	PlayedAt        time.Time
	PlayerCount     int
	Winners         []string
	WinnersCount    int
	TopWinner       string
	TopWinnerProfit float64
	BuyInsTotal     float64
	ResultsTotal    float64
	Difference      float64
	MathMatches     bool
	Players         []ArchivePlayerResult
}

type ArchivePlayerResult struct {
	Name         string
	BuyIns       float64
	Result       float64
	ProfitBuyIns float64
}

type ArchiveStats struct {
	GamesCount         int
	TotalBuyIns        float64
	AverageBank        float64
	MaxBank            float64
	MaxBankGameNumber  int
	MaxBankSessionDate string
	MinBank            float64
	MinBankGameNumber  int
	MinBankSessionDate string
	AveragePlayerCount float64
	BiggestWin         float64
	BiggestWinPlayer   string
	BiggestWinGame     int
	BiggestWinDate     string
	MostActivePlayer   string
	MostActiveGames    int
}

type ArchivePlayerStats struct {
	Name          string
	GamesCount    int
	TotalBuyIns   float64
	TotalWon      float64
	TotalProfit   float64
	AverageProfit float64
	BiggestWin    float64
	BiggestLoss   float64
	WinningGames  int
	LosingGames   int
	NeutralGames  int
}

type ArchivePlayerHistoryEntry struct {
	GameNumber   int
	SessionDate  string
	PlayedAt     time.Time
	BuyIns       float64
	Result       float64
	ProfitBuyIns float64
}

type ArchivePlayerHistory struct {
	Player ArchivePlayerStats
	Games  []ArchivePlayerHistoryEntry
}

type ArchiveTopMetric string

const (
	ArchiveTopProfit     ArchiveTopMetric = "profit"
	ArchiveTopLoss       ArchiveTopMetric = "loss"
	ArchiveTopGames      ArchiveTopMetric = "games"
	ArchiveTopBiggestWin ArchiveTopMetric = "biggest_win"
)
