package telegram

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"poker-bot/internal/domain"
)

func renderStatsHistory(games []domain.NumberedGame) string {
	lines := []string{"Игры:"}
	for _, numbered := range games {
		game := numbered.Game
		winners := "нет"
		if len(game.Winners) > 0 {
			sortedWinners := append([]string(nil), game.Winners...)
			sort.Strings(sortedWinners)
			winners = strings.Join(sortedWinners, ", ")
		}

		lines = append(lines, fmt.Sprintf("#%d | %s | банк %s | игроков %d | победители: %s",
			game.GameNumber,
			formatStatsDate(game.CreatedAt, game.SessionDate),
			formatDecimal(game.TotalBuyIns),
			game.PlayerCount,
			winners,
		))
		lines = append(lines, statsGameAlias(game.GameNumber))
	}
	return strings.Join(lines, "\n")
}

func renderStatsPlayers(players []domain.PlayerStats) string {
	slugByName := buildPlayerSlugMap(players)
	lines := []string{"Игроки:", ""}
	for i, player := range players {
		lines = append(lines, player.Name)
		lines = append(lines, fmt.Sprintf("Игр: %d | Итог: %s | Бай-ины: %s | Выиграл: %s",
			player.GamesCount,
			formatSignedDecimal(player.TotalProfit),
			formatDecimal(player.TotalBuyIns),
			formatDecimal(player.TotalWonBuyIns),
		))
		lines = append(lines, statsPlayerAlias(player.Name, slugByName))
		if i != len(players)-1 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func renderStatsGame(numbered domain.NumberedGame) string {
	game := numbered.Game
	players := append([]domain.PlayerResult(nil), game.Players...)
	sort.Slice(players, func(i, j int) bool {
		if players[i].ProfitBuyIns.Equal(players[j].ProfitBuyIns) {
			return players[i].Name < players[j].Name
		}
		return players[i].ProfitBuyIns.GreaterThan(players[j].ProfitBuyIns)
	})

	lines := []string{
		fmt.Sprintf("Игра #%d", game.GameNumber),
		fmt.Sprintf("Дата: %s", formatStatsDate(game.CreatedAt, game.SessionDate)),
		fmt.Sprintf("Банк: %s бай-инов", formatDecimal(game.TotalBuyIns)),
		fmt.Sprintf("Игроков: %d", game.PlayerCount),
		"",
		"Итоги игры:",
		"",
	}

	for i, player := range players {
		lines = append(lines, player.Name)
		lines = append(lines, fmt.Sprintf("Бай-ины: %s | Результат: %s | Профит: %s",
			formatDecimal(player.BuyIns),
			formatDecimal(player.WonBuyIns),
			formatSignedDecimal(player.ProfitBuyIns),
		))
		if i != len(players)-1 {
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func renderStatsPlayer(history domain.GamePlayerHistory) string {
	player := history.Player
	lines := []string{
		fmt.Sprintf("Игрок: %s", player.Name),
		"",
		"Общая статистика:",
		fmt.Sprintf("Игр: %d", player.GamesCount),
		fmt.Sprintf("Итог: %s", formatSignedDecimal(player.TotalProfit)),
		fmt.Sprintf("Бай-ины: %s", formatDecimal(player.TotalBuyIns)),
		fmt.Sprintf("Выиграл: %s", formatDecimal(player.TotalWonBuyIns)),
		fmt.Sprintf("Средний результат: %s", formatAverageDecimal(player.AverageProfit)),
		fmt.Sprintf("Лучший результат: %s", formatSignedDecimal(player.BiggestWin)),
		fmt.Sprintf("Худший результат: %s", formatSignedDecimal(player.BiggestLoss)),
		"",
		"История игр:",
		"",
	}

	for i, game := range history.Games {
		lines = append(lines, fmt.Sprintf("#%d | %s", game.GameNumber, formatStatsDate(game.CreatedAt, game.SessionDate)))
		lines = append(lines, fmt.Sprintf("Бай-ины: %s | Результат: %s | Профит: %s",
			formatDecimal(game.BuyIns),
			formatDecimal(game.WonBuyIns),
			formatSignedDecimal(game.ProfitBuyIns),
		))
		if i != len(history.Games)-1 {
			lines = append(lines, "")
		}
	}

	return strings.Join(lines, "\n")
}

func renderStatsTop(metric domain.ArchiveTopMetric, players []domain.PlayerStats) string {
	lines := []string{fmt.Sprintf("Топ 10: %s", archiveTopMetricTitle(metric))}
	for i, player := range players {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, player.Name, statsTopMetricValue(metric, player)))
	}
	return strings.Join(lines, "\n")
}

func statsTopMetricValue(metric domain.ArchiveTopMetric, player domain.PlayerStats) string {
	switch metric {
	case domain.ArchiveTopLoss:
		return formatSignedDecimal(player.TotalProfit)
	case domain.ArchiveTopGames:
		return strconv.FormatInt(player.GamesCount, 10)
	case domain.ArchiveTopBiggestWin:
		return formatSignedDecimal(player.BiggestWin)
	default:
		return formatSignedDecimal(player.TotalProfit)
	}
}

func formatStatsDate(createdAt time.Time, fallback string) string {
	if parsed, err := time.Parse("2006-01-02", fallback); err == nil {
		return parsed.Format("02.01.2006")
	}
	if !createdAt.IsZero() {
		return createdAt.Format("02.01.2006")
	}
	return fallback
}
