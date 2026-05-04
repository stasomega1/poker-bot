package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"poker-bot/internal/domain"
)

func normalizeStatsCommand(command string, args string) (string, string) {
	switch {
	case strings.HasPrefix(command, "stats_game_"):
		return "stats_game", strings.TrimSpace(strings.TrimPrefix(command, "stats_game_"))
	case strings.HasPrefix(command, "stats_player_"):
		return "stats_player", strings.TrimSpace(strings.TrimPrefix(command, "stats_player_"))
	default:
		return command, strings.TrimSpace(args)
	}
}

func statsGameAlias(gameNumber int) string {
	return "/stats_game_" + strconv.Itoa(gameNumber)
}

func statsPlayerAlias(name string, slugByName map[string]string) string {
	slug := slugByName[name]
	if slug == "" {
		slug = buildPlayerSlug(name)
	}
	return "/stats_player_" + slug
}

func (b *Bot) buildPlayerSlugMap(ctx context.Context, chatID int64) (map[string]string, error) {
	players, err := b.statsService.BuildPlayerStats(ctx, chatID)
	if err != nil {
		return nil, err
	}

	return buildPlayerSlugMap(players), nil
}

func (b *Bot) resolveStatsPlayerName(ctx context.Context, chatID int64, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("укажите имя игрока: /stats_player <имя>")
	}

	players, err := b.statsService.BuildPlayerStats(ctx, chatID)
	if err != nil {
		return "", err
	}

	normalizedInput := normalizeStatsPlayerValue(trimmed)
	for _, player := range players {
		if normalizeStatsPlayerValue(player.Name) == normalizedInput {
			return player.Name, nil
		}
	}

	nameBySlug := buildSlugToPlayerMap(players)
	if name, ok := nameBySlug[normalizedInput]; ok {
		return name, nil
	}

	return trimmed, nil
}

func buildPlayerSlugMap(players []domain.PlayerStats) map[string]string {
	sorted := append([]domain.PlayerStats(nil), players...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	counts := make(map[string]int)
	slugByName := make(map[string]string, len(sorted))
	for _, player := range sorted {
		base := buildPlayerSlug(player.Name)
		counts[base]++

		slug := base
		if counts[base] > 1 {
			slug = fmt.Sprintf("%s-%d", base, counts[base])
		}

		slugByName[player.Name] = slug
	}

	return slugByName
}

func buildSlugToPlayerMap(players []domain.PlayerStats) map[string]string {
	slugByName := buildPlayerSlugMap(players)
	nameBySlug := make(map[string]string, len(slugByName))
	for name, slug := range slugByName {
		nameBySlug[slug] = name
	}
	return nameBySlug
}

func buildPlayerSlug(value string) string {
	var builder strings.Builder
	lastUnderscore := false

	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		replacement, ok := transliterateCyrillic(r)
		switch {
		case ok:
			builder.WriteString(replacement)
			lastUnderscore = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			lastUnderscore = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if builder.Len() > 0 && !lastUnderscore {
				builder.WriteRune('_')
				lastUnderscore = true
			}
		}
	}

	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		return "player"
	}
	return slug
}

func normalizeStatsPlayerValue(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func transliterateCyrillic(r rune) (string, bool) {
	switch r {
	case 'а':
		return "a", true
	case 'б':
		return "b", true
	case 'в':
		return "v", true
	case 'г':
		return "g", true
	case 'д':
		return "d", true
	case 'е', 'ё':
		return "e", true
	case 'ж':
		return "zh", true
	case 'з':
		return "z", true
	case 'и':
		return "i", true
	case 'й':
		return "y", true
	case 'к':
		return "k", true
	case 'л':
		return "l", true
	case 'м':
		return "m", true
	case 'н':
		return "n", true
	case 'о':
		return "o", true
	case 'п':
		return "p", true
	case 'р':
		return "r", true
	case 'с':
		return "s", true
	case 'т':
		return "t", true
	case 'у':
		return "u", true
	case 'ф':
		return "f", true
	case 'х':
		return "h", true
	case 'ц':
		return "ts", true
	case 'ч':
		return "ch", true
	case 'ш':
		return "sh", true
	case 'щ':
		return "sch", true
	case 'ъ', 'ь':
		return "", true
	case 'ы':
		return "y", true
	case 'э':
		return "e", true
	case 'ю':
		return "yu", true
	case 'я':
		return "ya", true
	case 'ә':
		return "a", true
	case 'ғ':
		return "g", true
	case 'қ':
		return "k", true
	case 'ң':
		return "n", true
	case 'ө':
		return "o", true
	case 'ұ':
		return "u", true
	case 'ү':
		return "u", true
	case 'һ':
		return "h", true
	case 'і':
		return "i", true
	default:
		return "", false
	}
}
