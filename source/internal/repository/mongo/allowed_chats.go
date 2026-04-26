package mongo

import (
	"context"
	"errors"
	"time"

	"pocker-bot/internal/domain"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/bson"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AllowedChatRepository struct {
	collection        *driver.Collection
	defaultBuyInPrice decimal.Decimal
}

func NewAllowedChatRepository(db *driver.Database, defaultBuyInPrice decimal.Decimal) *AllowedChatRepository {
	return &AllowedChatRepository{
		collection:        db.Collection("allowed_chats"),
		defaultBuyInPrice: defaultBuyInPrice,
	}
}

func (r *AllowedChatRepository) CreateIfMissing(ctx context.Context, chat domain.AllowedChat) error {
	now := time.Now().UTC()

	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"chat_id": chat.ChatID},
		bson.M{
			"$setOnInsert": bson.M{
				"chat_id":         chat.ChatID,
				"is_active":       chat.IsActive,
				"buyin_price_kzt": r.defaultBuyInPrice.String(),
				"created_at":      now,
			},
			"$set": bson.M{
				"title":      chat.Title,
				"updated_at": now,
			},
		},
		options.Update().SetUpsert(true),
	)

	return err
}

func (r *AllowedChatRepository) FindActiveByChatID(ctx context.Context, chatID int64) (domain.AllowedChat, error) {
	var document allowedChatDocument
	err := r.collection.FindOne(ctx, bson.M{"chat_id": chatID, "is_active": true}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.AllowedChat{}, ErrChatNotFound
	}
	if err != nil {
		return domain.AllowedChat{}, err
	}

	return document.toDomain()
}

func (r *AllowedChatRepository) UpdateBuyInPrice(ctx context.Context, chatID int64, title string, price decimal.Decimal) (domain.AllowedChat, error) {
	now := time.Now().UTC()
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var document allowedChatDocument
	err := r.collection.FindOneAndUpdate(
		ctx,
		bson.M{"chat_id": chatID, "is_active": true},
		bson.M{
			"$set": bson.M{
				"title":           title,
				"buyin_price_kzt": price.String(),
				"updated_at":      now,
			},
		},
		opts,
	).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.AllowedChat{}, ErrChatNotFound
	}
	if err != nil {
		return domain.AllowedChat{}, err
	}

	return document.toDomain()
}

type allowedChatDocument struct {
	ChatID        int64     `bson:"chat_id"`
	Title         string    `bson:"title"`
	IsActive      bool      `bson:"is_active"`
	BuyInPriceKZT string    `bson:"buyin_price_kzt"`
	CreatedAt     time.Time `bson:"created_at"`
	UpdatedAt     time.Time `bson:"updated_at"`
}

func (d allowedChatDocument) toDomain() (domain.AllowedChat, error) {
	price, err := decimal.NewFromString(d.BuyInPriceKZT)
	if err != nil {
		return domain.AllowedChat{}, err
	}

	return domain.AllowedChat{
		ChatID:        d.ChatID,
		Title:         d.Title,
		IsActive:      d.IsActive,
		BuyInPriceKZT: price,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}, nil
}
