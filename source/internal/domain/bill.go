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
	CreatedByUserID      int64             `bson:"created_by_user_id"`
	CreatedByName        string            `bson:"created_by_name"`
	PayerUserID          int64             `bson:"payer_user_id"`
	PayerName            string            `bson:"payer_name"`
	SourcePhotoFileID    string            `bson:"source_photo_file_id"`
	SourcePhotoMessageID int               `bson:"source_photo_message_id"`
	MenuMessageID        int               `bson:"menu_message_id"`
	MerchantName         string            `bson:"merchant_name"`
	Items                []BillItem        `bson:"items"`
	Assignments          []BillAssignment  `bson:"assignments"`
	ServiceAmount        decimal.Decimal   `bson:"service_amount"`
	TotalAmount          decimal.Decimal   `bson:"total_amount"`
	ItemsSubtotal        decimal.Decimal   `bson:"items_subtotal"`
}

type BillItem struct {
	Index     int             `bson:"index"`
	Name      string          `bson:"name"`
	Quantity  int             `bson:"quantity"`
	UnitPrice decimal.Decimal `bson:"unit_price"`
	LineTotal decimal.Decimal `bson:"line_total"`
	Assigned  int             `bson:"assigned"`
	Remaining int             `bson:"remaining"`
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
