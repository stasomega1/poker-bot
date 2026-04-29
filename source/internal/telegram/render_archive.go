package telegram

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"poker-bot/internal/domain"
)

func renderArchiveHelpText() string {
	return strings.Join([]string{
		"Команды архива:",
		"/archive",
		"/archive_history",
		"/archive_stats",
		"/archive_players",
		"/archive_game <номер>",
		"/archive_player <имя>",
		"/archive_top",
		"",
		"Архив доступен только в архивной группе и в личке ее участникам.",
	}, "\n")
}

func renderArchiveHistory(games []domain.ArchiveGame) string {
	lines := []string{"Архивные игры:"}
	for _, game := range games {
		winners := "нет"
		if len(game.Winners) > 0 {
			sortedWinners := append([]string(nil), game.Winners...)
			sort.Strings(sortedWinners)
			winners = strings.Join(sortedWinners, ", ")
		}

		lines = append(lines, fmt.Sprintf("#%d | %s | банк %s | игроков %d | победители: %s",
			game.GameNumber,
			formatArchiveDate(game.PlayedAt, game.SessionDate),
			formatArchiveNumber(game.BuyInsTotal),
			game.PlayerCount,
			winners,
		))
	}
	return strings.Join(lines, "\n")
}

func renderArchiveStats(stats domain.ArchiveStats) string {
	lines := []string{
		"Статистика архива:",
		fmt.Sprintf("Игр: %d", stats.GamesCount),
		fmt.Sprintf("Всего бай-инов: %s", formatArchiveNumber(stats.TotalBuyIns)),
		fmt.Sprintf("Средний банк: %s", formatArchiveNumber(stats.AverageBank)),
		fmt.Sprintf("Максимальный банк: %s", formatArchiveNumber(stats.MaxBank)),
		fmt.Sprintf("Минимальный банк: %s", formatArchiveNumber(stats.MinBank)),
		fmt.Sprintf("Среднее число игроков: %s", formatArchiveNumber(stats.AveragePlayerCount)),
	}

	if stats.BiggestWinPlayer != "" {
		lines = append(lines, fmt.Sprintf("Лучший результат за игру: %s — %s", formatArchiveSigned(stats.BiggestWin), stats.BiggestWinPlayer))
	}
	if stats.MostActivePlayer != "" {
		lines = append(lines, fmt.Sprintf("Самый активный игрок: %s (%d игр)", stats.MostActivePlayer, stats.MostActiveGames))
	}

	return strings.Join(lines, "\n")
}

func renderArchivePlayers(players []domain.ArchivePlayerStats) string {
	lines := []string{"Игроки архива:", ""}
	for i, player := range players {
		lines = append(lines, player.Name)
		lines = append(lines, fmt.Sprintf("Игр: %d | Итог: %s | Бай-ины: %s | Выиграл: %s",
			player.GamesCount,
			formatArchiveSigned(player.TotalProfit),
			formatArchiveNumber(player.TotalBuyIns),
			formatArchiveNumber(player.TotalWon),
		))
		if i != len(players)-1 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func renderArchiveGame(game domain.ArchiveGame) string {
	players := append([]domain.ArchivePlayerResult(nil), game.Players...)
	sort.Slice(players, func(i, j int) bool {
		if players[i].ProfitBuyIns == players[j].ProfitBuyIns {
			return players[i].Name < players[j].Name
		}
		return players[i].ProfitBuyIns > players[j].ProfitBuyIns
	})

	lines := []string{
		fmt.Sprintf("Архивная игра #%d", game.GameNumber),
		fmt.Sprintf("Дата: %s", formatArchiveDate(game.PlayedAt, game.SessionDate)),
		fmt.Sprintf("Банк: %s бай-инов", formatArchiveNumber(game.BuyInsTotal)),
		fmt.Sprintf("Игроков: %d", game.PlayerCount),
		"",
		"Итоги игры:",
		"",
	}

	for i, player := range players {
		lines = append(lines, player.Name)
		lines = append(lines, fmt.Sprintf("Бай-ины: %s | Результат: %s | Профит: %s",
			formatArchiveNumber(player.BuyIns),
			formatArchiveNumber(player.Result),
			formatArchiveSigned(player.ProfitBuyIns),
		))
		if i != len(players)-1 {
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func renderArchivePlayer(history domain.ArchivePlayerHistory) string {
	player := history.Player
	lines := []string{
		fmt.Sprintf("Игрок архива: %s", player.Name),
		"",
		"Общая статистика:",
		fmt.Sprintf("Игр: %d", player.GamesCount),
		fmt.Sprintf("Итог: %s", formatArchiveSigned(player.TotalProfit)),
		fmt.Sprintf("Бай-ины: %s", formatArchiveNumber(player.TotalBuyIns)),
		fmt.Sprintf("Выиграл: %s", formatArchiveNumber(player.TotalWon)),
		fmt.Sprintf("Средний результат: %s", formatArchiveAverage(player.AverageProfit)),
		fmt.Sprintf("Лучший результат: %s", formatArchiveSigned(player.BiggestWin)),
		fmt.Sprintf("Худший результат: %s", formatArchiveSigned(player.BiggestLoss)),
		"",
		"История игр:",
		"",
	}

	for i, game := range history.Games {
		lines = append(lines, fmt.Sprintf("#%d | %s", game.GameNumber, formatArchiveDate(game.PlayedAt, game.SessionDate)))
		lines = append(lines, fmt.Sprintf("Бай-ины: %s | Результат: %s | Профит: %s",
			formatArchiveNumber(game.BuyIns),
			formatArchiveNumber(game.Result),
			formatArchiveSigned(game.ProfitBuyIns),
		))
		if i != len(history.Games)-1 {
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func renderArchiveTop(metric domain.ArchiveTopMetric, players []domain.ArchivePlayerStats) string {
	lines := []string{fmt.Sprintf("Топ 10: %s", archiveTopMetricTitle(metric))}

	for i, player := range players {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, player.Name, archiveTopMetricValue(metric, player)))
	}

	return strings.Join(lines, "\n")
}

func archiveTopMetricTitle(metric domain.ArchiveTopMetric) string {
	switch metric {
	case domain.ArchiveTopLoss:
		return "самый большой минус"
	case domain.ArchiveTopGames:
		return "количество игр"
	case domain.ArchiveTopBiggestWin:
		return "разовый выигрыш"
	default:
		return "суммарный плюс"
	}
}

func archiveTopMetricValue(metric domain.ArchiveTopMetric, player domain.ArchivePlayerStats) string {
	switch metric {
	case domain.ArchiveTopLoss:
		return formatArchiveSigned(player.TotalProfit)
	case domain.ArchiveTopGames:
		return strconv.Itoa(player.GamesCount)
	case domain.ArchiveTopBiggestWin:
		return formatArchiveSigned(player.BiggestWin)
	default:
		return formatArchiveSigned(player.TotalProfit)
	}
}

func formatArchiveDate(playedAt time.Time, fallback string) string {
	if parsed, err := time.Parse("2006-01-02", fallback); err == nil {
		return parsed.Format("02.01.2006")
	}

	if !playedAt.IsZero() {
		return playedAt.Format("02.01.2006")
	}

	return fallback
}

func formatArchiveNumber(value float64) string {
	truncated := math.Trunc(value*10) / 10
	if truncated == math.Trunc(truncated) {
		return strconv.FormatFloat(truncated, 'f', 0, 64)
	}
	return strconv.FormatFloat(truncated, 'f', 1, 64)
}

func formatArchiveAverage(value float64) string {
	rounded := math.Round(value*1000) / 1000
	if rounded > 0 {
		return "+" + strconv.FormatFloat(rounded, 'f', 3, 64)
	}
	return strconv.FormatFloat(rounded, 'f', 3, 64)
}

func formatArchiveSigned(value float64) string {
	if value > 0 {
		return "+" + formatArchiveNumber(value)
	}
	return formatArchiveNumber(value)
}
