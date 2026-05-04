package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

type Config struct {
	BotToken           string
	MongoURI           string
	MongoDatabase      string
	DefaultBuyInPrice  decimal.Decimal
	RegistrarUserID    int64
	OpenAIAPIKey       string
	OpenAIReceiptModel string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		BotToken:           strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		MongoURI:           strings.TrimSpace(os.Getenv("MONGODB_URI")),
		MongoDatabase:      strings.TrimSpace(getEnv("MONGODB_DB", "poker_bot")),
		OpenAIAPIKey:       strings.TrimSpace(getEnv("OPENAI_API_KEY", getEnv("OPEN_API_KEY", ""))),
		OpenAIReceiptModel: strings.TrimSpace(getEnv("OPENAI_RECEIPT_MODEL", "gpt-4.1-mini")),
	}

	price, err := decimal.NewFromString(strings.TrimSpace(getEnv("DEFAULT_BUYIN_PRICE", "2000")))
	if err != nil {
		return Config{}, fmt.Errorf("parse DEFAULT_BUYIN_PRICE: %w", err)
	}
	cfg.DefaultBuyInPrice = price

	registrarUserID, err := strconv.ParseInt(strings.TrimSpace(getEnv("REGISTRAR_USER_ID", "0")), 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("parse REGISTRAR_USER_ID: %w", err)
	}
	cfg.RegistrarUserID = registrarUserID

	switch {
	case cfg.BotToken == "":
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	case cfg.MongoURI == "":
		return Config{}, fmt.Errorf("MONGODB_URI is required")
	case cfg.DefaultBuyInPrice.LessThanOrEqual(decimal.Zero):
		return Config{}, fmt.Errorf("DEFAULT_BUYIN_PRICE must be greater than zero")
	case cfg.RegistrarUserID == 0:
		return Config{}, fmt.Errorf("REGISTRAR_USER_ID is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
