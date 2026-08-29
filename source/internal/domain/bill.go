package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type BillSessionStatus string

const (
	BillSessionActive    BillSessionStatus = "active"
	BillSessionFinished  BillSessionStatus = "finished"
	BillSessionCancelled BillSessionStatus = "cancelled"
)

type BillSession struct {
	ID                   string            `bson:"_id,omitempty"`
	ChatID               int64             `bson:"chat_id"`
	ChatTitle            string            `bson:"chat_title"`
	Status               BillSessionStatus `bson:"status"`
	CreatedAt            time.Time         `bson:"created_at"`
	UpdatedAt            time.Time         `bson:"updated_at"`
	AutoCloseAt          time.Time         `bson:"auto_close_at,omitempty"`
	CreatedByUserID      int64             `bson:"created_by_user_id"`
	CreatedByName        string            `bson:"created_by_name"`
	PayerUserID          int64             `bson:"payer_user_id"`
	PayerName            string            `bson:"payer_name"`
	SourcePhotoFileID    string            `bson:"source_photo_file_id"`
	SourcePhotoMessageID int               `bson:"source_photo_message_id"`
	MenuMessageID        int               `bson:"menu_message_id"`
	MerchantName         string            `bson:"merchant_name"`
	RecognitionAttempts  int               `bson:"recognition_attempts"`
	Items                []BillItem        `bson:"items"`
	Assignments          []BillAssignment  `bson:"assignments"`
	ServiceAmount        decimal.Decimal   `bson:"service_amount"`
	TotalAmount          decimal.Decimal   `bson:"total_amount"`
	ItemsSubtotal        decimal.Decimal   `bson:"items_subtotal"`
}

type BillItem struct {
	Index                int             `bson:"index"`
	Name                 string          `bson:"name"`
	Quantity             int             `bson:"quantity"`
	UnitPrice            decimal.Decimal `bson:"unit_price"`
	LineTotal            decimal.Decimal `bson:"line_total"`
	ExpectedParticipants int             `bson:"expected_participants"`
	Assigned             int             `bson:"assigned"`
	Remaining            int             `bson:"remaining"`
}

func (i BillItem) EffectiveQuantity() int {
	if i.Quantity == 1 {
		if i.ExpectedParticipants > 1 {
			return i.ExpectedParticipants
		}
		return 1
	}
	return i.Quantity
}

func (i BillItem) IsSharedSingleton() bool {
	return i.Quantity == 1 && i.EffectiveQuantity() > 1
}

func (i BillItem) ProgressCapacity() int {
	return max(i.EffectiveQuantity(), i.Assigned)
}

type BillAssignment struct {
	UserID    int64  `bson:"user_id"`
	UserName  string `bson:"user_name"`
	ItemIndex int    `bson:"item_index"`
	Quantity  int    `bson:"quantity"`
}

type BillParticipantSummary struct {
	UserID       int64
	UserName     string
	ItemsTotal   decimal.Decimal
	ServiceShare decimal.Decimal
	GrandTotal   decimal.Decimal
}

type ParsedReceipt struct {
	MerchantName  string
	Attempts      int
	Items         []ParsedReceiptItem
	ServiceAmount decimal.Decimal
	TotalAmount   decimal.Decimal
}

type ParsedReceiptItem struct {
	Name      string
	Quantity  int
	UnitPrice decimal.Decimal
	LineTotal decimal.Decimal
}
