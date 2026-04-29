package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type SettlementCalculator struct{}

type ledgerEntry struct {
	Name   string
	Amount decimal.Decimal
}

func NewSettlementCalculator() *SettlementCalculator {
	return &SettlementCalculator{}
}

func (c *SettlementCalculator) BuildGame(request domain.GameRequest) (domain.Game, error) {
	buyInsTotal := decimal.Zero
	winsTotal := decimal.Zero

	buyInsByName := make(map[string]domain.PlayerInput, len(request.BuyIns))
	for _, entry := range request.BuyIns {
		buyInsByName[normalizeName(entry.Name)] = entry
		buyInsTotal = buyInsTotal.Add(entry.Amount)
	}

	winnersByName := make(map[string]domain.PlayerInput, len(request.Winners))
	missingWinners := make([]string, 0)

	for _, entry := range request.Winners {
		key := normalizeName(entry.Name)
		buyInEntry, ok := buyInsByName[key]
		if !ok {
			missingWinners = append(missingWinners, entry.Name)
			continue
		}

		entry.Name = buyInEntry.Name
		winnersByName[key] = entry
		winsTotal = winsTotal.Add(entry.Amount)
	}

	if len(missingWinners) > 0 {
		sort.Strings(missingWinners)
		return domain.Game{}, fmt.Errorf(
			"победители отсутствуют в сообщении с байинами: %s (проверьте опечатки, имена победителей должны совпадать с именами в байинах)",
			strings.Join(missingWinners, ", "),
		)
	}

	if !winsTotal.Equal(buyInsTotal) {
		return domain.Game{}, fmt.Errorf("касса не сходится: в байинах %s, в результатах %s", buyInsTotal.String(), winsTotal.String())
	}

	players := make([]domain.PlayerResult, 0, len(request.BuyIns))
	debtors := make([]ledgerEntry, 0)
	creditors := make([]ledgerEntry, 0)

	for _, entry := range request.BuyIns {
		won := decimal.Zero
		if winner, ok := winnersByName[normalizeName(entry.Name)]; ok {
			won = winner.Amount
		}

		profitBuyIns := won.Sub(entry.Amount)
		profitKZT := profitBuyIns.Mul(request.BuyInPriceKZT)

		players = append(players, domain.PlayerResult{
			Name:         entry.Name,
			BuyIns:       entry.Amount,
			WonBuyIns:    won,
			ProfitBuyIns: profitBuyIns,
			ProfitKZT:    profitKZT,
		})

		switch {
		case profitBuyIns.GreaterThan(decimal.Zero):
			creditors = append(creditors, ledgerEntry{Name: entry.Name, Amount: profitBuyIns})
		case profitBuyIns.LessThan(decimal.Zero):
			debtors = append(debtors, ledgerEntry{Name: entry.Name, Amount: profitBuyIns.Abs()})
		}
	}

	sort.Slice(players, func(i, j int) bool {
		return players[i].ProfitBuyIns.GreaterThan(players[j].ProfitBuyIns)
	})

	return domain.Game{
		ChatID:                 request.ChatID,
		ChatTitle:              request.ChatTitle,
		BuyInPriceKZT:          request.BuyInPriceKZT,
		SourceBuyInsMessageID:  request.BuyInsMessageID,
		SourceResultsMessageID: request.ResultsMessageID,
		SourceCommandMessageID: request.CommandMessageID,
		SourceBuyInsText:       request.BuyInsText,
		SourceResultsText:      request.ResultsText,
		Players:                players,
		Settlements:            buildSettlements(debtors, creditors, request.BuyInPriceKZT),
		TotalBuyIns:            buyInsTotal,
		CreatedAt:              time.Now().UTC(),
		CreatedByUserID:        request.CreateUserID,
		CreatedByName:          request.CreateUserName,
	}, nil
}

func buildSettlements(debtors, creditors []ledgerEntry, price decimal.Decimal) []domain.Settlement {
	result := make([]domain.Settlement, 0)
	debtorIndex := 0
	creditorIndex := 0

	for debtorIndex < len(debtors) && creditorIndex < len(creditors) {
		debtor := &debtors[debtorIndex]
		creditor := &creditors[creditorIndex]

		amount := minDecimal(debtor.Amount, creditor.Amount)
		result = append(result, domain.Settlement{
			FromName:     debtor.Name,
			ToName:       creditor.Name,
			AmountBuyIns: amount,
			AmountKZT:    amount.Mul(price),
		})

		debtor.Amount = debtor.Amount.Sub(amount)
		creditor.Amount = creditor.Amount.Sub(amount)

		if debtor.Amount.Equal(decimal.Zero) {
			debtorIndex++
		}
		if creditor.Amount.Equal(decimal.Zero) {
			creditorIndex++
		}
	}

	return result
}

func minDecimal(left, right decimal.Decimal) decimal.Decimal {
	if left.LessThan(right) {
		return left
	}
	return right
}
