package service

import (
	"context"
	"testing"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type fakeBillSessionRepository struct {
	session domain.BillSession
}

func (r *fakeBillSessionRepository) Create(ctx context.Context, session domain.BillSession) (domain.BillSession, error) {
	r.session = session
	return session, nil
}

func (r *fakeBillSessionRepository) FindActiveByChatID(ctx context.Context, chatID int64) (domain.BillSession, error) {
	return r.session, nil
}

func (r *fakeBillSessionRepository) FindLatestByChatIDsAndUserID(ctx context.Context, chatIDs []int64, userID int64) (domain.BillSession, error) {
	return r.session, nil
}

func (r *fakeBillSessionRepository) Update(ctx context.Context, session domain.BillSession) error {
	r.session = session
	return nil
}

func (r *fakeBillSessionRepository) FindByID(ctx context.Context, id string) (domain.BillSession, error) {
	return r.session, nil
}

func TestSplitItemIntoSinglesKeepsExistingAssignments(t *testing.T) {
	repo := &fakeBillSessionRepository{session: domain.BillSession{
		ID:        "bill-1",
		ChatID:    123,
		Status:    domain.BillSessionActive,
		CreatedAt: time.Now(),
		Items: []domain.BillItem{
			{
				Index:     1,
				Name:      "Syrniki",
				Quantity:  4,
				UnitPrice: decimal.NewFromInt(1000),
				LineTotal: decimal.NewFromInt(4000),
				Assigned:  3,
				Remaining: 1,
			},
			{
				Index:     2,
				Name:      "Tea",
				Quantity:  1,
				UnitPrice: decimal.NewFromInt(500),
				LineTotal: decimal.NewFromInt(500),
				Assigned:  1,
				Remaining: 0,
			},
		},
		Assignments: []domain.BillAssignment{
			{UserID: 10, UserName: "A", ItemIndex: 1, Quantity: 2},
			{UserID: 20, UserName: "B", ItemIndex: 1, Quantity: 1},
			{UserID: 30, UserName: "C", ItemIndex: 2, Quantity: 1},
		},
	}}
	service := NewBillService(repo, nil, nil)

	session, err := service.SplitItemIntoSingles(context.Background(), "bill-1", 1)
	if err != nil {
		t.Fatalf("SplitItemIntoSingles returned error: %v", err)
	}

	if len(session.Items) != 5 {
		t.Fatalf("expected 5 items after split, got %d", len(session.Items))
	}

	assignedByUser := map[int64]int{}
	for _, assignment := range session.Assignments {
		if assignment.UserID == 10 || assignment.UserID == 20 {
			if assignment.ItemIndex < 1 || assignment.ItemIndex > 4 {
				t.Fatalf("split assignment points to item %d, want one of split items 1..4", assignment.ItemIndex)
			}
			assignedByUser[assignment.UserID] += assignment.Quantity
		}
	}

	if assignedByUser[10] != 2 {
		t.Fatalf("expected user 10 to keep 2 selected units, got %d", assignedByUser[10])
	}
	if assignedByUser[20] != 1 {
		t.Fatalf("expected user 20 to keep 1 selected unit, got %d", assignedByUser[20])
	}

	if session.Items[0].Assigned != 1 || session.Items[1].Assigned != 1 || session.Items[2].Assigned != 1 || session.Items[3].Assigned != 0 {
		t.Fatalf("unexpected split item assignment counts: %+v", session.Items[:4])
	}
	if session.Items[4].Index != 5 || session.Assignments[len(session.Assignments)-1].ItemIndex != 5 {
		t.Fatalf("expected non-split item and assignment to be reindexed to 5, got item=%d assignment=%d", session.Items[4].Index, session.Assignments[len(session.Assignments)-1].ItemIndex)
	}
}
