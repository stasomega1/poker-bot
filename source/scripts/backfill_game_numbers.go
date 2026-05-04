package main

import (
	"context"
	"log"
	"os"
	"strings"

	mongorepo "poker-bot/internal/repository/mongo"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load(".env", "../.env", "../../.env")

	mongoURI := strings.TrimSpace(os.Getenv("MONGODB_URI"))
	mongoDB := strings.TrimSpace(os.Getenv("MONGODB_DB"))
	if mongoURI == "" {
		log.Fatal("MONGODB_URI is required")
	}
	if mongoDB == "" {
		log.Fatal("MONGODB_DB is required")
	}

	client, err := mongorepo.NewClient(context.Background(), mongoURI, mongoDB)
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer client.Close(context.Background())

	repo := mongorepo.NewGameRepository(client.Database)
	if err := repo.BackfillGameNumbers(context.Background()); err != nil {
		log.Fatalf("backfill game numbers: %v", err)
	}

	log.Println("game number backfill completed")
}
