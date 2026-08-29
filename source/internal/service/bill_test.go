package service

import (
	"context"
	"testing"
	"time"

	"poker-bot/internal/domain"
	mongorepo "poker-bot/internal/repository/mongo"

	"github.com/shopspring/decimal"
)

type fakeBillSessionRepository struct {
	session        domain.BillSession
	expiredSession []domain.BillSession
}

type fakeAllowedChatRepository struct{}

func (r *fakeAllowedChatRepository) CreateIfMissing(ctx context.Context, chat domain.AllowedChat) error {
	return nil
}

func (r *fakeAllowedChatRepository) FindActiveByChatID(ctx context.Context, chatID int64) (domain.AllowedChat, error) {
	return domain.AllowedChat{ChatID: chatID, Title: "Test Chat"}, nil
}

func (r *fakeAllowedChatRepository) ListActive(ctx context.Context) ([]domain.AllowedChat, error) {
	return nil, nil
}

func (r *fakeAllowedChatRepository) UpdateBuyInPrice(ctx context.Context, chatID int64, title string, price decimal.Decimal) (domain.AllowedChat, error) {
	return domain.AllowedChat{ChatID: chatID, Title: title, BuyInPriceKZT: price}, nil
}

func (r *fakeBillSessionRepository) Create(ctx context.Context, session domain.BillSession) (domain.BillSession, error) {
	r.session = session
	return session, nil
}

func (r *fakeBillSessionRepository) FindActiveByChatID(ctx context.Context, chatID int64) (domain.BillSession, error) {
	if r.session.ID == "" && r.session.Status == "" {
		return domain.BillSession{}, mongorepo.ErrBillSessionNotFound
	}
	return r.session, nil
}

func (r *fakeBillSessionRepository) FindLatestByChatIDsAndUserID(ctx context.Context, chatIDs []int64, userID int64) (domain.BillSession, error) {
	return r.session, nil
}

func (r *fakeBillSessionRepository) FindExpiredActive(ctx context.Context, before time.Time, limit int) ([]domain.BillSession, error) {
	return append([]domain.BillSession(nil), r.expiredSession...), nil
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

func TestCalculateSummaryDistributesServiceFromFullItemsSubtotal(t *testing.T) {
	service := NewBillService(nil, nil, nil)
	session := domain.BillSession{
		ItemsSubtotal: decimal.NewFromInt(10000),
		ServiceAmount: decimal.NewFromInt(1000),
		Assignments:   []domain.BillAssignment{{UserID: 10, UserName: "A", ItemIndex: 1, Quantity: 1}},
		Items:         []domain.BillItem{{Index: 1, Name: "Steak", Quantity: 1, UnitPrice: decimal.NewFromInt(1000), LineTotal: decimal.NewFromInt(1000)}},
	}

	summary := service.calculateSummary(session)
	if len(summary) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(summary))
	}

	if !summary[0].ItemsTotal.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("expected items total 1000, got %s", summary[0].ItemsTotal)
	}
	if !summary[0].ServiceShare.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("expected service share 100, got %s", summary[0].ServiceShare)
	}
	if !summary[0].GrandTotal.Equal(decimal.NewFromInt(1100)) {
		t.Fatalf("expected grand total 1100, got %s", summary[0].GrandTotal)
	}
}

func TestCalculateSummarySharedSingletonUsesExpectedParticipants(t *testing.T) {
	service := NewBillService(nil, nil, nil)
	session := domain.BillSession{
		ItemsSubtotal: decimal.NewFromInt(12000),
		Assignments: []domain.BillAssignment{
			{UserID: 10, UserName: "A", ItemIndex: 1, Quantity: 1},
		},
		Items: []domain.BillItem{
			{
				Index:                1,
				Name:                 "Hookah",
				Quantity:             1,
				ExpectedParticipants: 2,
				UnitPrice:            decimal.NewFromInt(12000),
				LineTotal:            decimal.NewFromInt(12000),
				Assigned:             1,
				Remaining:            1,
			},
		},
	}

	summary := service.calculateSummary(session)
	if len(summary) != 1 {
		t.Fatalf("expected 1 summary row, got %d", len(summary))
	}
	if !summary[0].ItemsTotal.Equal(decimal.NewFromInt(6000)) {
		t.Fatalf("expected items total 6000, got %s", summary[0].ItemsTotal)
	}
}

func TestSetExpectedParticipantsRecalculatesRemaining(t *testing.T) {
	repo := &fakeBillSessionRepository{session: domain.BillSession{
		ID:     "bill-1",
		Status: domain.BillSessionActive,
		Items: []domain.BillItem{
			{
				Index:                1,
				Name:                 "Hookah",
				Quantity:             1,
				ExpectedParticipants: 1,
				UnitPrice:            decimal.NewFromInt(12000),
				LineTotal:            decimal.NewFromInt(12000),
				Assigned:             1,
				Remaining:            0,
			},
		},
		Assignments: []domain.BillAssignment{
			{UserID: 10, UserName: "A", ItemIndex: 1, Quantity: 1},
		},
	}}
	service := NewBillService(repo, nil, nil)

	session, err := service.SetExpectedParticipants(context.Background(), "bill-1", 10, 1, 2)
	if err != nil {
		t.Fatalf("SetExpectedParticipants returned error: %v", err)
	}

	if session.Items[0].ExpectedParticipants != 2 {
		t.Fatalf("expected participants 2, got %d", session.Items[0].ExpectedParticipants)
	}
	if session.Items[0].Remaining != 1 {
		t.Fatalf("expected remaining 1, got %d", session.Items[0].Remaining)
	}
}

func TestCreateDebugReceiptSetsAutoCloseAt(t *testing.T) {
	repo := &fakeBillSessionRepository{}
	service := NewBillService(repo, &fakeAllowedChatRepository{}, nil)
	service.SetAutoCloseAfter(72 * time.Hour)

	before := time.Now().UTC()
	session, err := service.CreateDebugReceipt(context.Background(), 123, "Test Chat", 10, "Creator", 20, "Payer")
	if err != nil {
		t.Fatalf("CreateDebugReceipt returned error: %v", err)
	}

	if session.AutoCloseAt.IsZero() {
		t.Fatal("expected auto close time to be set")
	}
	minExpected := before.Add(72 * time.Hour).Add(-2 * time.Second)
	maxExpected := before.Add(72 * time.Hour).Add(2 * time.Second)
	if session.AutoCloseAt.Before(minExpected) || session.AutoCloseAt.After(maxExpected) {
		t.Fatalf("unexpected auto close time: %s", session.AutoCloseAt)
	}
}

func TestCloseExpiredSessionsFinishesAssignedBillsAndCancelsEmptyBills(t *testing.T) {
	repo := &fakeBillSessionRepository{
		expiredSession: []domain.BillSession{
			{
				ID:            "bill-finished",
				Status:        domain.BillSessionActive,
				ItemsSubtotal: decimal.NewFromInt(1000),
				ServiceAmount: decimal.NewFromInt(100),
				Items: []domain.BillItem{
					{Index: 1, Name: "Tea", Quantity: 1, UnitPrice: decimal.NewFromInt(1000), LineTotal: decimal.NewFromInt(1000), Assigned: 1, Remaining: 0},
				},
				Assignments: []domain.BillAssignment{
					{UserID: 10, UserName: "A", ItemIndex: 1, Quantity: 1},
				},
			},
			{
				ID:     "bill-cancelled",
				Status: domain.BillSessionActive,
				Items: []domain.BillItem{
					{Index: 1, Name: "Coffee", Quantity: 1, UnitPrice: decimal.NewFromInt(900), LineTotal: decimal.NewFromInt(900), Assigned: 0, Remaining: 1},
				},
			},
		},
	}
	service := NewBillService(repo, nil, nil)

	closed, err := service.CloseExpiredSessions(context.Background(), time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("CloseExpiredSessions returned error: %v", err)
	}
	if len(closed) != 2 {
		t.Fatalf("expected 2 closed sessions, got %d", len(closed))
	}

	if closed[0].Session.Status != domain.BillSessionFinished {
		t.Fatalf("expected first bill to be finished, got %s", closed[0].Session.Status)
	}
	if len(closed[0].Summary) != 1 {
		t.Fatalf("expected first bill summary to be generated, got %d rows", len(closed[0].Summary))
	}
	if closed[1].Session.Status != domain.BillSessionCancelled {
		t.Fatalf("expected second bill to be cancelled, got %s", closed[1].Session.Status)
	}
	if len(closed[1].Summary) != 0 {
		t.Fatalf("expected cancelled bill to have no summary rows, got %d", len(closed[1].Summary))
	}
}
