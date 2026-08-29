package telegram

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"poker-bot/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shopspring/decimal"
)

var almatyLocation = time.FixedZone("GMT+5", 5*60*60)

func renderBillSession(session domain.BillSession) string {
	lines := make([]string, 0, len(session.Items)*3+8)

	if session.Status == domain.BillSessionFinished {
		lines = append(lines, "Счет закрыт")
		lines = append(lines, "")
	} else if countRemainingUnits(session) == 0 {
		lines = append(lines, "<b>Все позиции распределены</b>")
		lines = append(lines, "")
	}

	lines = append(lines,
		fmt.Sprintf("Счет: %s", html.EscapeString(fallbackBillMerchant(session.MerchantName))),
		fmt.Sprintf("Дата: %s", formatBillDate(session.CreatedAt)),
		fmt.Sprintf("Плательщик: %s", html.EscapeString(session.PayerName)),
		fmt.Sprintf("Итого: %s тг", formatDecimal(session.TotalAmount)),
		fmt.Sprintf("Сервис: %s тг", formatDecimal(session.ServiceAmount)),
		fmt.Sprintf("Распределено: %d / %d долей", countAssignedUnits(session), countTotalUnits(session)),
		fmt.Sprintf("Если нажать на кпоку позиции между + и - можно увидеть полное название, что бы не ходить вверх"),
		"",
		"Позиции:",
	)

	if session.RecognitionAttempts > 0 {
		lines = append(lines, fmt.Sprintf("Распознано с %d попытки", session.RecognitionAttempts), "")
	}

	for _, item := range session.Items {
		lines = append(lines, fmt.Sprintf("%d. %s - %s", item.Index, html.EscapeString(item.Name), billItemQuantityLabel(item)))
		lines = append(lines, fmt.Sprintf("Цена: %s | Разобрано: %d/%d", formatDecimal(item.UnitPrice), item.Assigned, item.EffectiveQuantity()))
		for _, assignmentLine := range billAssignmentLines(session, item) {
			lines = append(lines, assignmentLine)
		}
		lines = append(lines, "")
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderBillSummary(session domain.BillSession, summary []domain.BillParticipantSummary) string {
	lines := []string{
		fmt.Sprintf("Счет: %s", fallbackBillMerchant(session.MerchantName)),
		fmt.Sprintf("Плательщик: %s", session.PayerName),
		"",
		"Промежуточный итог:",
	}

	for _, row := range summary {
		lines = append(lines, fmt.Sprintf("%s", row.UserName))
		lines = append(lines, fmt.Sprintf("Позиции: %s тг | Сервис: %s тг | Итого: %s тг",
			formatDecimal(row.ItemsTotal),
			formatDecimal(row.ServiceShare),
			formatDecimal(row.GrandTotal),
		))
		lines = append(lines, "")
	}

	lines = append(lines, fmt.Sprintf("Осталось распределить долей: %d", countRemainingUnits(session)))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderBillMySummary(session domain.BillSession, row domain.BillParticipantSummary) string {
	return strings.Join([]string{
		fmt.Sprintf("Ваш счет: %s", fallbackBillMerchant(session.MerchantName)),
		fmt.Sprintf("Плательщик: %s", session.PayerName),
		"",
		fmt.Sprintf("Позиции: %s тг", formatDecimal(row.ItemsTotal)),
		fmt.Sprintf("Сервис: %s тг", formatDecimal(row.ServiceShare)),
		fmt.Sprintf("Итого: %s тг", formatDecimal(row.GrandTotal)),
	}, "\n")
}

func renderBillMyDetailed(session domain.BillSession, row domain.BillParticipantSummary) string {
	lines := []string{
		fmt.Sprintf("Счет: %s", fallbackBillMerchant(session.MerchantName)),
		fmt.Sprintf("Дата: %s", formatBillDate(session.CreatedAt)),
		fmt.Sprintf("Плательщик: %s", session.PayerName),
		"",
		"Ваши позиции:",
	}

	selected := false
	for _, item := range session.Items {
		userQty := 0
		for _, assignment := range session.Assignments {
			if assignment.ItemIndex == item.Index && assignment.UserID == row.UserID {
				userQty += assignment.Quantity
			}
		}
		if userQty == 0 {
			continue
		}
		selected = true
		lines = append(lines, fmt.Sprintf("%s - %d/%d", item.Name, userQty, item.EffectiveQuantity()))
		lines = append(lines, fmt.Sprintf("%s тг", billAssignmentAmount(item, userQty)))
	}
	if !selected {
		lines = append(lines, "Пока ничего не выбрано.", "")
	}

	lines = append(lines, "")
	withCommission := row.ItemsTotal.Mul(decimal.NewFromFloat(1.1))
	lines = append(lines,
		fmt.Sprintf("Итого по всем позициям: %s тг", formatDecimal(row.ItemsTotal)),
		fmt.Sprintf("Итого с комиссией 10%%: %s тг", formatDecimal(withCommission)),
	)

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderBillFinish(session domain.BillSession, summary []domain.BillParticipantSummary, actorName string) string {
	lines := []string{
		fmt.Sprintf("Счет закрыт: %s", fallbackBillMerchant(session.MerchantName)),
		fmt.Sprintf("Плательщик: %s", session.PayerName),
	}
	if strings.TrimSpace(actorName) != "" {
		lines = append(lines, fmt.Sprintf("Закрыл: %s", actorName))
	}
	lines = append(lines, "", "К переводу:")

	for _, row := range summary {
		lines = append(lines, fmt.Sprintf("%s -> %s: %s тг", row.UserName, session.PayerName, formatDecimal(row.GrandTotal)))
	}

	return strings.Join(lines, "\n")
}

func renderBillCancelled(actorName string) string {
	if strings.TrimSpace(actorName) == "" {
		return "Счет отменен."
	}
	return fmt.Sprintf("Счет отменен: %s", actorName)
}

func renderBillReminder(session domain.BillSession) string {
	lines := []string{
		"Kind reminder, остались нераспределенные позиции",
		"",
	}

	for _, item := range session.Items {
		if item.Remaining <= 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s - осталось %d/%d",
			item.Index,
			item.Name,
			item.Remaining,
			item.EffectiveQuantity(),
		))
	}

	return strings.Join(lines, "\n")
}

func countAssignedUnits(session domain.BillSession) int {
	total := 0
	for _, item := range session.Items {
		total += item.Assigned
	}
	return total
}

func countTotalUnits(session domain.BillSession) int {
	total := 0
	for _, item := range session.Items {
		total += item.ProgressCapacity()
	}
	return total
}

func countRemainingUnits(session domain.BillSession) int {
	total := 0
	for _, item := range session.Items {
		total += item.Remaining
	}
	return total
}

func fallbackBillMerchant(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "без названия"
	}
	return value
}

func formatBillDate(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.In(almatyLocation).Format("02.01.2006 15:04")
}

func billAssignmentLines(session domain.BillSession, item domain.BillItem) []string {
	type itemAssignment struct {
		UserName string
		Quantity int
		Amount   string
	}

	assignments := make([]itemAssignment, 0)
	for _, assignment := range session.Assignments {
		if assignment.ItemIndex != item.Index {
			continue
		}
		amount := billAssignmentAmount(item, assignment.Quantity)
		assignments = append(assignments, itemAssignment{
			UserName: assignment.UserName,
			Quantity: assignment.Quantity,
			Amount:   amount,
		})
	}

	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].Quantity == assignments[j].Quantity {
			return assignments[i].UserName < assignments[j].UserName
		}
		return assignments[i].Quantity > assignments[j].Quantity
	})

	lines := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		lines = append(lines, fmt.Sprintf("%s %d - %s",
			html.EscapeString(assignment.UserName),
			assignment.Quantity,
			assignment.Amount,
		))
	}
	return lines
}

func renderBillItemDetails(session domain.BillSession, item domain.BillItem) string {
	lines := []string{
		fmt.Sprintf("%s - %s", item.Name, billItemQuantityLabel(item)),
		fmt.Sprintf("Цена: %s | Разобрано: %d/%d", formatDecimal(item.UnitPrice), item.Assigned, item.EffectiveQuantity()),
	}
	for _, assignmentLine := range billAssignmentLines(session, item) {
		lines = append(lines, assignmentLine)
	}
	return strings.Join(lines, "\n")
}

func billAssignmentAmount(item domain.BillItem, quantity int) string {
	if quantity <= 0 {
		return formatDecimal(decimal.Zero)
	}

	if item.IsSharedSingleton() {
		divisor := max(item.EffectiveQuantity(), item.Assigned)
		share := item.LineTotal.Div(decimal.NewFromInt(int64(divisor)))
		return formatDecimal(share.Mul(decimal.NewFromInt(int64(quantity))))
	}

	if item.Assigned > item.Quantity {
		share := item.LineTotal.Div(decimal.NewFromInt(int64(item.Assigned)))
		return formatDecimal(share.Mul(decimal.NewFromInt(int64(quantity))))
	}

	return formatDecimal(item.UnitPrice.Mul(decimal.NewFromInt(int64(quantity))))
}

func billKeyboard(session domain.BillSession) [][]ButtonSpec {
	sortedItems := append([]domain.BillItem(nil), session.Items...)
	sort.Slice(sortedItems, func(i, j int) bool {
		return sortedItems[i].Index < sortedItems[j].Index
	})

	rows := make([][]ButtonSpec, 0, len(sortedItems))
	for _, item := range sortedItems {
		rows = append(rows, []ButtonSpec{
			{
				Text: "−",
				Data: buildBillAdjustCallback(session.ID, item.Index, -1),
			},
			{
				Text: billItemButtonLabel(item),
				Data: buildBillItemHintCallback(session.ID, item.Index),
			},
			{
				Text: "+",
				Data: buildBillAdjustCallback(session.ID, item.Index, 1),
			},
		})
	}
	rows = append(rows, []ButtonSpec{
		{
			Text: "Отправить мне счет",
			Data: buildBillSendMyCallback(session.ID),
		},
	})
	rows = append(rows, []ButtonSpec{
		{
			Text: "Разбить позицию по одной",
			Data: buildBillSplitMenuCallback(session.ID),
		},
	})
	rows = append(rows, []ButtonSpec{
		{
			Text: "Закрыть счет",
			Data: buildBillFinishCallback(session.ID),
		},
		{
			Text: "Отменить счет",
			Data: buildBillCancelCallback(session.ID),
		},
	})
	return rows
}

type ButtonSpec struct {
	Text string
	Data string
}

func billItemButtonLabel(item domain.BillItem) string {
	return fmt.Sprintf("%s %d/%d", shortenBillItemName(item.Name, 10), item.Assigned, item.EffectiveQuantity())
}

func billItemQuantityLabel(item domain.BillItem) string {
	label := fmt.Sprintf("%d шт", item.Quantity)
	if item.IsSharedSingleton() {
		return fmt.Sprintf("%s (разбито на %d человек)", label, item.EffectiveQuantity())
	}
	return label
}

func shortenBillItemName(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}

	runes := []rune(value)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "."
}

func renderBillSplitChoice(sessionID string, items []domain.BillItem) (string, tgbotapi.InlineKeyboardMarkup) {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(items)+1)
	for _, item := range items {
		label := fmt.Sprintf("#%d %s - %d шт", item.Index, shortenBillItemName(item.Name, 20), item.Quantity)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, buildBillSplitItemCallback(sessionID, item.Index)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Нет", billCloseNoopPrefix),
	))
	return "Выберите позицию, которую нужно разбить по одной:", tgbotapi.NewInlineKeyboardMarkup(rows...)
}
