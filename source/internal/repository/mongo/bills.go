package mongo

import (
	"context"
	"errors"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type BillSessionRepository struct {
	collection *driver.Collection
}

func NewBillSessionRepository(db *driver.Database) *BillSessionRepository {
	return &BillSessionRepository{
		collection: db.Collection("bill_sessions"),
	}
}

func (r *BillSessionRepository) Create(ctx context.Context, session domain.BillSession) (domain.BillSession, error) {
	now := time.Now().UTC()
	document, err := billSessionDocumentFromDomain(session)
	if err != nil {
		return domain.BillSession{}, err
	}
	document.CreatedAt = now
	document.UpdatedAt = now

	result, err := r.collection.InsertOne(ctx, document)
	if err != nil {
		return domain.BillSession{}, err
	}

	objectID, ok := result.InsertedID.(primitive.ObjectID)
	if !ok {
		return domain.BillSession{}, errors.New("failed to convert inserted id")
	}

	document.ID = objectID
	return document.toDomain()
}

func (r *BillSessionRepository) FindActiveByChatID(ctx context.Context, chatID int64) (domain.BillSession, error) {
	var document billSessionDocument
	err := r.collection.FindOne(
		ctx,
		bson.M{"chat_id": chatID, "status": domain.BillSessionActive},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}}),
	).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.BillSession{}, ErrBillSessionNotFound
	}
	if err != nil {
		return domain.BillSession{}, err
	}
	return document.toDomain()
}

func (r *BillSessionRepository) FindDueReminders(ctx context.Context, before time.Time, limit int) ([]domain.BillSession, error) {
	if limit <= 0 {
		limit = 100
	}

	cursor, err := r.collection.Find(
		ctx,
		bson.M{
			"status":           domain.BillSessionActive,
			"reminder_at":      bson.M{"$lte": before.UTC()},
			"reminder_sent_at": bson.M{"$exists": false},
			"$or": []bson.M{
				{"auto_close_at": bson.M{"$exists": false}},
				{"auto_close_at": bson.M{"$gt": before.UTC()}},
			},
		},
		options.Find().
			SetSort(bson.D{{Key: "reminder_at", Value: 1}, {Key: "created_at", Value: 1}}).
			SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	sessions := make([]domain.BillSession, 0)
	for cursor.Next(ctx) {
		var document billSessionDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		session, err := document.toDomain()
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *BillSessionRepository) FindExpiredActive(ctx context.Context, before time.Time, limit int) ([]domain.BillSession, error) {
	if limit <= 0 {
		limit = 100
	}

	cursor, err := r.collection.Find(
		ctx,
		bson.M{
			"status":        domain.BillSessionActive,
			"auto_close_at": bson.M{"$lte": before.UTC()},
		},
		options.Find().
			SetSort(bson.D{{Key: "auto_close_at", Value: 1}, {Key: "created_at", Value: 1}}).
			SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	sessions := make([]domain.BillSession, 0)
	for cursor.Next(ctx) {
		var document billSessionDocument
		if err := cursor.Decode(&document); err != nil {
			return nil, err
		}
		session, err := document.toDomain()
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (r *BillSessionRepository) FindByID(ctx context.Context, id string) (domain.BillSession, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return domain.BillSession{}, err
	}

	var document billSessionDocument
	err = r.collection.FindOne(ctx, bson.M{"_id": objectID}).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.BillSession{}, ErrBillSessionNotFound
	}
	if err != nil {
		return domain.BillSession{}, err
	}
	return document.toDomain()
}

func (r *BillSessionRepository) FindLatestByChatIDsAndUserID(ctx context.Context, chatIDs []int64, userID int64) (domain.BillSession, error) {
	if len(chatIDs) == 0 {
		return domain.BillSession{}, ErrBillSessionNotFound
	}

	var document billSessionDocument
	err := r.collection.FindOne(
		ctx,
		bson.M{
			"chat_id": bson.M{"$in": chatIDs},
			"status":  bson.M{"$ne": domain.BillSessionCancelled},
			"assignments": bson.M{
				"$elemMatch": bson.M{
					"user_id": userID,
				},
			},
		},
		options.FindOne().SetSort(bson.D{{Key: "updated_at", Value: -1}, {Key: "created_at", Value: -1}}),
	).Decode(&document)
	if errors.Is(err, driver.ErrNoDocuments) {
		return domain.BillSession{}, ErrBillSessionNotFound
	}
	if err != nil {
		return domain.BillSession{}, err
	}
	return document.toDomain()
}

func (r *BillSessionRepository) Update(ctx context.Context, session domain.BillSession) error {
	document, err := billSessionDocumentFromDomain(session)
	if err != nil {
		return err
	}
	document.UpdatedAt = time.Now().UTC()

	_, err = r.collection.ReplaceOne(ctx, bson.M{"_id": document.ID}, document)
	return err
}

type billSessionDocument struct {
	ID                   primitive.ObjectID       `bson:"_id,omitempty"`
	ChatID               int64                    `bson:"chat_id"`
	ChatTitle            string                   `bson:"chat_title"`
	Status               domain.BillSessionStatus `bson:"status"`
	CreatedAt            time.Time                `bson:"created_at"`
	UpdatedAt            time.Time                `bson:"updated_at"`
	ReminderAt           time.Time                `bson:"reminder_at,omitempty"`
	ReminderSentAt       time.Time                `bson:"reminder_sent_at,omitempty"`
	AutoCloseAt          time.Time                `bson:"auto_close_at,omitempty"`
	CreatedByUserID      int64                    `bson:"created_by_user_id"`
	CreatedByName        string                   `bson:"created_by_name"`
	PayerUserID          int64                    `bson:"payer_user_id"`
	PayerName            string                   `bson:"payer_name"`
	SourcePhotoFileID    string                   `bson:"source_photo_file_id"`
	SourcePhotoMessageID int                      `bson:"source_photo_message_id"`
	MenuMessageID        int                      `bson:"menu_message_id"`
	MerchantName         string                   `bson:"merchant_name"`
	RecognitionAttempts  int                      `bson:"recognition_attempts"`
	Items                []billItemDocument       `bson:"items"`
	Assignments          []billAssignmentDocument `bson:"assignments"`
	ServiceAmount        string                   `bson:"service_amount"`
	TotalAmount          string                   `bson:"total_amount"`
	ItemsSubtotal        string                   `bson:"items_subtotal"`
}

type billItemDocument struct {
	Index                int    `bson:"index"`
	Name                 string `bson:"name"`
	Quantity             int    `bson:"quantity"`
	UnitPrice            string `bson:"unit_price"`
	LineTotal            string `bson:"line_total"`
	ExpectedParticipants int    `bson:"expected_participants,omitempty"`
	Assigned             int    `bson:"assigned"`
	Remaining            int    `bson:"remaining"`
}

type billAssignmentDocument struct {
	UserID    int64  `bson:"user_id"`
	UserName  string `bson:"user_name"`
	ItemIndex int    `bson:"item_index"`
	Quantity  int    `bson:"quantity"`
}

func billSessionDocumentFromDomain(session domain.BillSession) (billSessionDocument, error) {
	var objectID primitive.ObjectID
	var err error
	if session.ID != "" {
		objectID, err = primitive.ObjectIDFromHex(session.ID)
		if err != nil {
			return billSessionDocument{}, err
		}
	}

	items := make([]billItemDocument, 0, len(session.Items))
	for _, item := range session.Items {
		items = append(items, billItemDocument{
			Index:                item.Index,
			Name:                 item.Name,
			Quantity:             item.Quantity,
			UnitPrice:            item.UnitPrice.String(),
			LineTotal:            item.LineTotal.String(),
			ExpectedParticipants: item.ExpectedParticipants,
			Assigned:             item.Assigned,
			Remaining:            item.Remaining,
		})
	}

	assignments := make([]billAssignmentDocument, 0, len(session.Assignments))
	for _, assignment := range session.Assignments {
		assignments = append(assignments, billAssignmentDocument{
			UserID:    assignment.UserID,
			UserName:  assignment.UserName,
			ItemIndex: assignment.ItemIndex,
			Quantity:  assignment.Quantity,
		})
	}

	return billSessionDocument{
		ID:                   objectID,
		ChatID:               session.ChatID,
		ChatTitle:            session.ChatTitle,
		Status:               session.Status,
		CreatedAt:            session.CreatedAt,
		UpdatedAt:            session.UpdatedAt,
		ReminderAt:           session.ReminderAt,
		ReminderSentAt:       session.ReminderSentAt,
		AutoCloseAt:          session.AutoCloseAt,
		CreatedByUserID:      session.CreatedByUserID,
		CreatedByName:        session.CreatedByName,
		PayerUserID:          session.PayerUserID,
		PayerName:            session.PayerName,
		SourcePhotoFileID:    session.SourcePhotoFileID,
		SourcePhotoMessageID: session.SourcePhotoMessageID,
		MenuMessageID:        session.MenuMessageID,
		MerchantName:         session.MerchantName,
		RecognitionAttempts:  session.RecognitionAttempts,
		Items:                items,
		Assignments:          assignments,
		ServiceAmount:        session.ServiceAmount.String(),
		TotalAmount:          session.TotalAmount.String(),
		ItemsSubtotal:        session.ItemsSubtotal.String(),
	}, nil
}

func (d billSessionDocument) toDomain() (domain.BillSession, error) {
	serviceAmount, err := decimal.NewFromString(d.ServiceAmount)
	if err != nil {
		return domain.BillSession{}, err
	}
	totalAmount, err := decimal.NewFromString(d.TotalAmount)
	if err != nil {
		return domain.BillSession{}, err
	}
	itemsSubtotal, err := decimal.NewFromString(d.ItemsSubtotal)
	if err != nil {
		return domain.BillSession{}, err
	}

	items := make([]domain.BillItem, 0, len(d.Items))
	for _, item := range d.Items {
		unitPrice, err := decimal.NewFromString(item.UnitPrice)
		if err != nil {
			return domain.BillSession{}, err
		}
		lineTotal, err := decimal.NewFromString(item.LineTotal)
		if err != nil {
			return domain.BillSession{}, err
		}
		items = append(items, domain.BillItem{
			Index:                item.Index,
			Name:                 item.Name,
			Quantity:             item.Quantity,
			UnitPrice:            unitPrice,
			LineTotal:            lineTotal,
			ExpectedParticipants: max(item.ExpectedParticipants, 1),
			Assigned:             item.Assigned,
			Remaining:            item.Remaining,
		})
	}

	assignments := make([]domain.BillAssignment, 0, len(d.Assignments))
	for _, assignment := range d.Assignments {
		assignments = append(assignments, domain.BillAssignment{
			UserID:    assignment.UserID,
			UserName:  assignment.UserName,
			ItemIndex: assignment.ItemIndex,
			Quantity:  assignment.Quantity,
		})
	}

	return domain.BillSession{
		ID:                   d.ID.Hex(),
		ChatID:               d.ChatID,
		ChatTitle:            d.ChatTitle,
		Status:               d.Status,
		CreatedAt:            d.CreatedAt,
		UpdatedAt:            d.UpdatedAt,
		ReminderAt:           d.ReminderAt,
		ReminderSentAt:       d.ReminderSentAt,
		AutoCloseAt:          d.AutoCloseAt,
		CreatedByUserID:      d.CreatedByUserID,
		CreatedByName:        d.CreatedByName,
		PayerUserID:          d.PayerUserID,
		PayerName:            d.PayerName,
		SourcePhotoFileID:    d.SourcePhotoFileID,
		SourcePhotoMessageID: d.SourcePhotoMessageID,
		MenuMessageID:        d.MenuMessageID,
		MerchantName:         d.MerchantName,
		RecognitionAttempts:  d.RecognitionAttempts,
		Items:                items,
		Assignments:          assignments,
		ServiceAmount:        serviceAmount,
		TotalAmount:          totalAmount,
		ItemsSubtotal:        itemsSubtotal,
	}, nil
}
