package telegram

import (
	"fmt"
	"strings"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

func renderGameSummary(game domain.Game) string {
	lines := []string{
		"Игра сохранена.",
		fmt.Sprintf("Цена байина: %s тг", formatDecimal(game.BuyInPriceKZT)),
		fmt.Sprintf("Общий банк: %s байинов", formatDecimal(game.TotalBuyIns)),
		"",
		"Итоги:",
	}

	for _, player := range game.Players {
		lines = append(lines, fmt.Sprintf("%s: внес %s, забрал %s, итог %s байинов (%s тг)",
			player.Name,
			formatDecimal(player.BuyIns),
			formatDecimal(player.WonBuyIns),
			formatSignedDecimal(player.ProfitBuyIns),
			formatSignedDecimal(player.ProfitKZT),
		))
	}

	lines = append(lines, "", "Переводы:")
	if len(game.Settlements) == 0 {
		lines = append(lines, "Переводы не требуются.")
	} else {
		for _, settlement := range game.Settlements {
			lines = append(lines, fmt.Sprintf("%s -> %s: %s байинов (%s тг)",
				settlement.FromName,
				settlement.ToName,
				formatDecimal(settlement.AmountBuyIns),
				formatDecimal(settlement.AmountKZT),
			))
		}
	}

	return strings.Join(lines, "\n")
}

func helpText() string {
	return strings.Join([]string{
		"Команды:",
		"/reg — зарегистрировать чат",
		"/game — сохранить игру по цепочке reply",
		"/setbuyin 2500 — изменить цену байина",
		"/history — последние игры",
		"/stats — статистика по текущей группе или выбор группы в личке",
		"/players — статистика игроков по текущей группе или выбор группы в личке",
		"/groups — список доступных игровых групп в личке",
		"/archive — архив исторических игр",
		"/help — справка",
		"",
		"Формат игры:",
		"1. Отправьте сообщение с байинами всех игроков.",
		"2. Reply на него отправьте сообщение с результатами победителей.",
		"3. Reply на второе сообщение отправьте /game.",
		"",
		"Формат строк:",
		"Имя 4",
		"Имя Фамилия 2.5",
	}, "\n")
}

func formatDecimal(value decimal.Decimal) string {
	return value.String()
}

func formatSignedDecimal(value decimal.Decimal) string {
	if value.GreaterThan(decimal.Zero) {
		return "+" + value.String()
	}
	return value.String()
}
