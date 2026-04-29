package service

import (
	"fmt"
	"strings"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type MessageParser struct{}

func NewMessageParser() *MessageParser {
	return &MessageParser{}
}

func (p *MessageParser) ParsePlayers(text string) ([]domain.PlayerInput, error) {
	lines := strings.Split(text, "\n")
	players := make([]domain.PlayerInput, 0, len(lines))
	seen := make(map[string]struct{})

	for idx, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		player, err := p.parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", idx+1, err)
		}

		key := normalizeName(player.Name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("line %d: duplicated player %q", idx+1, player.Name)
		}
		seen[key] = struct{}{}

		players = append(players, player)
	}

	if len(players) == 0 {
		return nil, fmt.Errorf("message is empty")
	}

	return players, nil
}

func (p *MessageParser) parseLine(line string) (domain.PlayerInput, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return domain.PlayerInput{}, fmt.Errorf("expected format <name> <amount>")
	}

	amount, err := decimal.NewFromString(fields[len(fields)-1])
	if err != nil {
		return domain.PlayerInput{}, fmt.Errorf("invalid amount %q", fields[len(fields)-1])
	}
	if amount.LessThan(decimal.Zero) {
		return domain.PlayerInput{}, fmt.Errorf("amount must be zero or greater")
	}

	name := normalizeDisplayName(strings.Join(fields[:len(fields)-1], " "))
	if name == "" {
		return domain.PlayerInput{}, fmt.Errorf("player name is empty")
	}

	return domain.PlayerInput{
		Name:   name,
		Amount: amount,
	}, nil
}

func normalizeName(value string) string {
	return strings.ToLower(normalizeDisplayName(value))
}

func normalizeDisplayName(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
