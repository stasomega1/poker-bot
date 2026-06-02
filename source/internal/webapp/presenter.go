package webapp

import (
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type billResponse struct {
	Session     billSessionResponse      `json:"session"`
	Items       []billItemResponse       `json:"items"`
	Assignments []billAssignmentResponse `json:"assignments"`
	Summary     []billSummaryResponse    `json:"summary"`
	Me          currentUserBillResponse  `json:"me"`
	Permissions billPermissionsResponse  `json:"permissions"`
	Progress    billProgressResponse     `json:"progress"`
}

type billSessionResponse struct {
	ID                  string `json:"id"`
	ChatID              int64  `json:"chatId"`
	ChatTitle           string `json:"chatTitle"`
	Status              string `json:"status"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
	CreatedByUserID     int64  `json:"createdByUserId"`
	CreatedByName       string `json:"createdByName"`
	PayerUserID         int64  `json:"payerUserId"`
	PayerName           string `json:"payerName"`
	MerchantName        string `json:"merchantName"`
	RecognitionAttempts int    `json:"recognitionAttempts"`
	ServiceAmount       string `json:"serviceAmount"`
	TotalAmount         string `json:"totalAmount"`
	ItemsSubtotal       string `json:"itemsSubtotal"`
}

type billItemResponse struct {
	Index                int    `json:"index"`
	Name                 string `json:"name"`
	Quantity             int    `json:"quantity"`
	ExpectedParticipants int    `json:"expectedParticipants"`
	EffectiveQuantity    int    `json:"effectiveQuantity"`
	UnitPrice            string `json:"unitPrice"`
	LineTotal            string `json:"lineTotal"`
	Assigned             int    `json:"assigned"`
	Remaining            int    `json:"remaining"`
	MyQuantity           int    `json:"myQuantity"`
}

type billAssignmentResponse struct {
	UserID    int64  `json:"userId"`
	UserName  string `json:"userName"`
	ItemIndex int    `json:"itemIndex"`
	Quantity  int    `json:"quantity"`
}

type billSummaryResponse struct {
	UserID       int64  `json:"userId"`
	UserName     string `json:"userName"`
	ItemsTotal   string `json:"itemsTotal"`
	ServiceShare string `json:"serviceShare"`
	GrandTotal   string `json:"grandTotal"`
}

type currentUserBillResponse struct {
	UserID       int64  `json:"userId"`
	UserName     string `json:"userName"`
	ItemsTotal   string `json:"itemsTotal"`
	ServiceShare string `json:"serviceShare"`
	GrandTotal   string `json:"grandTotal"`
	HasItems     bool   `json:"hasItems"`
}

type billPermissionsResponse struct {
	CanAdjust bool `json:"canAdjust"`
	CanSplit  bool `json:"canSplit"`
	CanFinish bool `json:"canFinish"`
	CanCancel bool `json:"canCancel"`
}

type billProgressResponse struct {
	AssignedUnits  int `json:"assignedUnits"`
	TotalUnits     int `json:"totalUnits"`
	RemainingUnits int `json:"remainingUnits"`
}

func presentBill(session domain.BillSession, summary []domain.BillParticipantSummary, user TelegramUser) billResponse {
	myQuantityByItem := make(map[int]int)
	for _, assignment := range session.Assignments {
		if assignment.UserID == user.ID {
			myQuantityByItem[assignment.ItemIndex] += assignment.Quantity
		}
	}

	items := make([]billItemResponse, 0, len(session.Items))
	assignedUnits := 0
	totalUnits := 0
	remainingUnits := 0
	for _, item := range session.Items {
		assignedUnits += item.Assigned
		totalUnits += item.ProgressCapacity()
		remainingUnits += item.Remaining
		items = append(items, billItemResponse{
			Index:                item.Index,
			Name:                 item.Name,
			Quantity:             item.Quantity,
			ExpectedParticipants: item.ExpectedParticipants,
			EffectiveQuantity:    item.EffectiveQuantity(),
			UnitPrice:            decimalString(item.UnitPrice),
			LineTotal:            decimalString(item.LineTotal),
			Assigned:             item.Assigned,
			Remaining:            item.Remaining,
			MyQuantity:           myQuantityByItem[item.Index],
		})
	}

	assignments := make([]billAssignmentResponse, 0, len(session.Assignments))
	for _, assignment := range session.Assignments {
		assignments = append(assignments, billAssignmentResponse{
			UserID:    assignment.UserID,
			UserName:  assignment.UserName,
			ItemIndex: assignment.ItemIndex,
			Quantity:  assignment.Quantity,
		})
	}

	summaryRows := make([]billSummaryResponse, 0, len(summary))
	me := currentUserBillResponse{
		UserID:   user.ID,
		UserName: DisplayUserName(user),
	}
	for _, row := range summary {
		responseRow := billSummaryResponse{
			UserID:       row.UserID,
			UserName:     row.UserName,
			ItemsTotal:   decimalString(row.ItemsTotal),
			ServiceShare: decimalString(row.ServiceShare),
			GrandTotal:   decimalString(row.GrandTotal),
		}
		summaryRows = append(summaryRows, responseRow)
		if row.UserID == user.ID {
			me.ItemsTotal = responseRow.ItemsTotal
			me.ServiceShare = responseRow.ServiceShare
			me.GrandTotal = responseRow.GrandTotal
			me.HasItems = true
		}
	}

	isOrganizer := session.CreatedByUserID == user.ID || (session.PayerUserID != 0 && session.PayerUserID == user.ID)
	isActive := session.Status == domain.BillSessionActive

	return billResponse{
		Session: billSessionResponse{
			ID:                  session.ID,
			ChatID:              session.ChatID,
			ChatTitle:           session.ChatTitle,
			Status:              string(session.Status),
			CreatedAt:           formatTime(session.CreatedAt),
			UpdatedAt:           formatTime(session.UpdatedAt),
			CreatedByUserID:     session.CreatedByUserID,
			CreatedByName:       session.CreatedByName,
			PayerUserID:         session.PayerUserID,
			PayerName:           session.PayerName,
			MerchantName:        session.MerchantName,
			RecognitionAttempts: session.RecognitionAttempts,
			ServiceAmount:       decimalString(session.ServiceAmount),
			TotalAmount:         decimalString(session.TotalAmount),
			ItemsSubtotal:       decimalString(session.ItemsSubtotal),
		},
		Items:       items,
		Assignments: assignments,
		Summary:     summaryRows,
		Me:          me,
		Permissions: billPermissionsResponse{
			CanAdjust: isActive,
			CanSplit:  isActive && isOrganizer,
			CanFinish: isActive && isOrganizer,
			CanCancel: isActive && isOrganizer,
		},
		Progress: billProgressResponse{
			AssignedUnits:  assignedUnits,
			TotalUnits:     totalUnits,
			RemainingUnits: remainingUnits,
		},
	}
}

func decimalString(value decimal.Decimal) string {
	return value.StringFixedBank(2)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
