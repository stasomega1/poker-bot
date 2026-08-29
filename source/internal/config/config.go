package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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
	HTTPAddr           string
	WebAppBaseURL      string
	InitDataMaxAge     time.Duration
	BillAutoCloseAfter time.Duration
	BillSweepInterval  time.Duration
	WebAppDevMode      bool
	WebAppDevUserID    int64
	WebAppDevUsername  string
	WebAppDevFirstName string
	WebAppDevLastName  string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		BotToken:           strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		MongoURI:           strings.TrimSpace(os.Getenv("MONGODB_URI")),
		MongoDatabase:      strings.TrimSpace(getEnv("MONGODB_DB", "poker_bot")),
		OpenAIAPIKey:       strings.TrimSpace(getEnv("OPENAI_API_KEY", getEnv("OPEN_API_KEY", ""))),
		OpenAIReceiptModel: strings.TrimSpace(getEnv("OPENAI_RECEIPT_MODEL", "gpt-5.4")),
		HTTPAddr:           strings.TrimSpace(getEnv("HTTP_ADDR", ":8080")),
		WebAppBaseURL:      strings.TrimRight(strings.TrimSpace(getEnv("WEBAPP_BASE_URL", "")), "/"),
		WebAppDevMode:      parseBoolEnv("WEBAPP_DEV_MODE"),
		WebAppDevUsername:  strings.TrimSpace(getEnv("WEBAPP_DEV_USERNAME", "")),
		WebAppDevFirstName: strings.TrimSpace(getEnv("WEBAPP_DEV_FIRST_NAME", "Local")),
		WebAppDevLastName:  strings.TrimSpace(getEnv("WEBAPP_DEV_LAST_NAME", "Dev")),
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

	webAppDevUserID, err := strconv.ParseInt(strings.TrimSpace(getEnv("WEBAPP_DEV_USER_ID", "0")), 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("parse WEBAPP_DEV_USER_ID: %w", err)
	}
	cfg.WebAppDevUserID = webAppDevUserID

	initDataMaxAge, err := time.ParseDuration(strings.TrimSpace(getEnv("TELEGRAM_INIT_DATA_MAX_AGE", "24h")))
	if err != nil {
		return Config{}, fmt.Errorf("parse TELEGRAM_INIT_DATA_MAX_AGE: %w", err)
	}
	cfg.InitDataMaxAge = initDataMaxAge

	billAutoCloseAfter, err := time.ParseDuration(strings.TrimSpace(getEnv("BILL_AUTO_CLOSE_AFTER", "0")))
	if err != nil {
		return Config{}, fmt.Errorf("parse BILL_AUTO_CLOSE_AFTER: %w", err)
	}
	cfg.BillAutoCloseAfter = billAutoCloseAfter

	billSweepInterval, err := time.ParseDuration(strings.TrimSpace(getEnv("BILL_SWEEP_INTERVAL", "5m")))
	if err != nil {
		return Config{}, fmt.Errorf("parse BILL_SWEEP_INTERVAL: %w", err)
	}
	cfg.BillSweepInterval = billSweepInterval

	switch {
	case cfg.BotToken == "":
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	case cfg.MongoURI == "":
		return Config{}, fmt.Errorf("MONGODB_URI is required")
	case cfg.DefaultBuyInPrice.LessThanOrEqual(decimal.Zero):
		return Config{}, fmt.Errorf("DEFAULT_BUYIN_PRICE must be greater than zero")
	case cfg.RegistrarUserID == 0:
		return Config{}, fmt.Errorf("REGISTRAR_USER_ID is required")
	case cfg.HTTPAddr == "":
		return Config{}, fmt.Errorf("HTTP_ADDR is required")
	case cfg.InitDataMaxAge <= 0:
		return Config{}, fmt.Errorf("TELEGRAM_INIT_DATA_MAX_AGE must be greater than zero")
	case cfg.BillAutoCloseAfter < 0:
		return Config{}, fmt.Errorf("BILL_AUTO_CLOSE_AFTER must not be negative")
	case cfg.BillSweepInterval <= 0:
		return Config{}, fmt.Errorf("BILL_SWEEP_INTERVAL must be greater than zero")
	case cfg.WebAppDevMode && cfg.WebAppDevUserID == 0:
		cfg.WebAppDevUserID = cfg.RegistrarUserID
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

func parseBoolEnv(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
