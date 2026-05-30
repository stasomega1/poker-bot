package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"poker-bot/internal/domain"
	"poker-bot/internal/repository"
	mongorepo "poker-bot/internal/repository/mongo"

	"github.com/shopspring/decimal"
)

type BillService struct {
	billRepo repository.BillSessionRepository
	chatRepo repository.AllowedChatRepository
	ocr      ReceiptOCR
	mu       sync.Mutex
}

func NewBillService(billRepo repository.BillSessionRepository, chatRepo repository.AllowedChatRepository, ocr ReceiptOCR) *BillService {
	return &BillService{
		billRepo: billRepo,
		chatRepo: chatRepo,
		ocr:      ocr,
	}
}

func (s *BillService) CreateFromReceipt(ctx context.Context, chatID int64, chatTitle string, creatorUserID int64, creatorName string, payerUserID int64, payerName string, photoFileID string, photoMessageID int, image []byte, mimeType string, onRetry func()) (domain.BillSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureBillSessionCanBeCreated(ctx, chatID); err != nil {
		return domain.BillSession{}, err
	}
	if s.ocr == nil {
		return domain.BillSession{}, fmt.Errorf("OCR для чеков не настроен")
	}

	parsed, err := s.ocr.ParseReceipt(ctx, image, mimeType, onRetry)
	if err != nil {
		return domain.BillSession{}, err
	}
	return s.createSessionFromParsedReceipt(ctx, chatID, chatTitle, creatorUserID, creatorName, payerUserID, payerName, photoFileID, photoMessageID, parsed)
}

func (s *BillService) CreateDebugReceipt(ctx context.Context, chatID int64, chatTitle string, creatorUserID int64, creatorName string, payerUserID int64, payerName string) (domain.BillSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureBillSessionCanBeCreated(ctx, chatID); err != nil {
		return domain.BillSession{}, err
	}

	return s.createSessionFromParsedReceipt(ctx, chatID, chatTitle, creatorUserID, creatorName, payerUserID, payerName, "", 0, debugParsedReceipt())
}

func (s *BillService) createSessionFromParsedReceipt(ctx context.Context, chatID int64, chatTitle string, creatorUserID int64, creatorName string, payerUserID int64, payerName string, photoFileID string, photoMessageID int, parsed domain.ParsedReceipt) (domain.BillSession, error) {
	if len(parsed.Items) == 0 {
		return domain.BillSession{}, fmt.Errorf("не удалось распознать позиции чека")
	}

	items := make([]domain.BillItem, 0, len(parsed.Items))
	itemsSubtotal := decimal.Zero
	for i, item := range parsed.Items {
		items = append(items, domain.BillItem{
			Index:     i + 1,
			Name:      strings.TrimSpace(item.Name),
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			LineTotal: item.LineTotal,
			Assigned:  0,
			Remaining: item.Quantity,
		})
		itemsSubtotal = itemsSubtotal.Add(item.LineTotal)
	}

	session := domain.BillSession{
		ChatID:               chatID,
		ChatTitle:            chatTitle,
		Status:               domain.BillSessionActive,
		CreatedByUserID:      creatorUserID,
		CreatedByName:        creatorName,
		PayerUserID:          payerUserID,
		PayerName:            strings.TrimSpace(payerName),
		SourcePhotoFileID:    photoFileID,
		SourcePhotoMessageID: photoMessageID,
		MerchantName:         parsed.MerchantName,
		RecognitionAttempts:  parsed.Attempts,
		Items:                items,
		Assignments:          []domain.BillAssignment{},
		ServiceAmount:        parsed.ServiceAmount,
		TotalAmount:          parsed.TotalAmount,
		ItemsSubtotal:        itemsSubtotal,
	}

	return s.billRepo.Create(ctx, session)
}

func (s *BillService) GetActive(ctx context.Context, chatID int64) (domain.BillSession, error) {
	return s.billRepo.FindActiveByChatID(ctx, chatID)
}

func (s *BillService) SetMenuMessageID(ctx context.Context, sessionID string, messageID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.billRepo.FindByID(ctx, sessionID)
	if err != nil {
		return err
	}
	session.MenuMessageID = messageID
	return s.billRepo.Update(ctx, session)
}

func (s *BillService) AdjustItem(ctx context.Context, sessionID string, userID int64, userName string, itemIndex int, delta int) (domain.BillSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.billRepo.FindByID(ctx, sessionID)
	if err != nil {
		return domain.BillSession{}, err
	}
	if session.Status != domain.BillSessionActive {
		return domain.BillSession{}, fmt.Errorf("счет уже закрыт")
	}

	itemPosition := -1
	for i := range session.Items {
		if session.Items[i].Index == itemIndex {
			itemPosition = i
			break
		}
	}
	if itemPosition == -1 {
		return domain.BillSession{}, fmt.Errorf("позиция не найдена")
	}

	item := &session.Items[itemPosition]
	assignmentPosition := -1
	for i := range session.Assignments {
		if session.Assignments[i].UserID == userID && session.Assignments[i].ItemIndex == itemIndex {
			assignmentPosition = i
			break
		}
	}

	if delta < 0 {
		if assignmentPosition == -1 || session.Assignments[assignmentPosition].Quantity < -delta {
			return domain.BillSession{}, fmt.Errorf("у вас нет такого количества этой позиции")
		}
	}

	if assignmentPosition == -1 && delta > 0 {
		session.Assignments = append(session.Assignments, domain.BillAssignment{
			UserID:    userID,
			UserName:  userName,
			ItemIndex: itemIndex,
			Quantity:  delta,
		})
	} else if assignmentPosition != -1 {
		session.Assignments[assignmentPosition].UserName = userName
		session.Assignments[assignmentPosition].Quantity += delta
		if session.Assignments[assignmentPosition].Quantity == 0 {
			session.Assignments = append(session.Assignments[:assignmentPosition], session.Assignments[assignmentPosition+1:]...)
		}
	}

	item.Assigned += delta
	item.Remaining = max(item.Quantity-item.Assigned, 0)
	if item.Assigned < 0 {
		return domain.BillSession{}, fmt.Errorf("некорректное состояние распределения")
	}

	if err := s.billRepo.Update(ctx, session); err != nil {
		return domain.BillSession{}, err
	}
	return session, nil
}

func (s *BillService) SplitItemIntoSingles(ctx context.Context, sessionID string, itemIndex int) (domain.BillSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.billRepo.FindByID(ctx, sessionID)
	if err != nil {
		return domain.BillSession{}, err
	}
	if session.Status != domain.BillSessionActive {
		return domain.BillSession{}, fmt.Errorf("счет уже закрыт")
	}

	targetPos := -1
	for i := range session.Items {
		if session.Items[i].Index == itemIndex {
			targetPos = i
			break
		}
	}
	if targetPos == -1 {
		return domain.BillSession{}, fmt.Errorf("позиция не найдена")
	}

	target := session.Items[targetPos]
	if target.Quantity <= 1 {
		return domain.BillSession{}, fmt.Errorf("позицию нельзя разбить")
	}

	newItems := make([]domain.BillItem, 0, len(session.Items)-1+target.Quantity)
	oldIndexToNew := make(map[int]int, len(session.Items))
	splitItemIndexes := make([]int, 0, target.Quantity)

	for i, item := range session.Items {
		if i != targetPos {
			newItems = append(newItems, item)
			continue
		}

		unitLineTotal := target.LineTotal.Div(decimal.NewFromInt(int64(target.Quantity)))
		for j := 0; j < target.Quantity; j++ {
			newItems = append(newItems, domain.BillItem{
				Name:      target.Name,
				Quantity:  1,
				UnitPrice: target.UnitPrice,
				LineTotal: unitLineTotal,
				Assigned:  0,
				Remaining: 1,
			})
			splitItemIndexes = append(splitItemIndexes, len(newItems)-1)
		}
	}

	for i := range newItems {
		oldIndex := newItems[i].Index
		newItems[i].Index = i + 1
		if oldIndex != 0 {
			oldIndexToNew[oldIndex] = newItems[i].Index
		}
	}

	for i, itemPosition := range splitItemIndexes {
		splitItemIndexes[i] = newItems[itemPosition].Index
	}

	assignments := make([]domain.BillAssignment, 0, len(session.Assignments)+target.Assigned)
	splitAssignmentByUserAndItem := make(map[string]int)
	splitUnitPosition := 0
	for _, assignment := range session.Assignments {
		if assignment.ItemIndex == target.Index {
			for i := 0; i < assignment.Quantity; i++ {
				itemIndex := splitItemIndexes[splitUnitPosition%len(splitItemIndexes)]
				splitUnitPosition++
				key := strconv.FormatInt(assignment.UserID, 10) + ":" + strconv.Itoa(itemIndex)
				existingPos, ok := splitAssignmentByUserAndItem[key]
				if ok {
					assignments[existingPos].Quantity++
				} else {
					splitAssignmentByUserAndItem[key] = len(assignments)
					assignments = append(assignments, domain.BillAssignment{
						UserID:    assignment.UserID,
						UserName:  assignment.UserName,
						ItemIndex: itemIndex,
						Quantity:  1,
					})
				}
				newItems[itemIndex-1].Assigned++
				newItems[itemIndex-1].Remaining = max(newItems[itemIndex-1].Quantity-newItems[itemIndex-1].Assigned, 0)
			}
			continue
		}

		newIndex, ok := oldIndexToNew[assignment.ItemIndex]
		if !ok {
			return domain.BillSession{}, fmt.Errorf("не удалось обновить назначения после разбивки")
		}
		assignment.ItemIndex = newIndex
		assignments = append(assignments, assignment)
	}

	session.Items = newItems
	session.Assignments = assignments
	if err := s.billRepo.Update(ctx, session); err != nil {
		return domain.BillSession{}, err
	}
	return session, nil
}

func (s *BillService) Cancel(ctx context.Context, chatID int64) (domain.BillSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.billRepo.FindActiveByChatID(ctx, chatID)
	if err != nil {
		return domain.BillSession{}, err
	}
	session.Status = domain.BillSessionCancelled
	if err := s.billRepo.Update(ctx, session); err != nil {
		return domain.BillSession{}, err
	}
	return session, nil
}

func (s *BillService) Finish(ctx context.Context, chatID int64, force bool) (domain.BillSession, []domain.BillParticipantSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.billRepo.FindActiveByChatID(ctx, chatID)
	if err != nil {
		return domain.BillSession{}, nil, err
	}
	if !force {
		for _, item := range session.Items {
			if item.Remaining != 0 {
				return domain.BillSession{}, nil, fmt.Errorf("не все позиции распределены")
			}
		}
	}

	summary := s.calculateSummary(session)
	session.Status = domain.BillSessionFinished
	if err := s.billRepo.Update(ctx, session); err != nil {
		return domain.BillSession{}, nil, err
	}
	return session, summary, nil
}

func (s *BillService) HasUnassignedItems(ctx context.Context, chatID int64) (bool, error) {
	session, err := s.billRepo.FindActiveByChatID(ctx, chatID)
	if err != nil {
		return false, err
	}
	for _, item := range session.Items {
		if item.Remaining != 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *BillService) Summary(ctx context.Context, chatID int64) (domain.BillSession, []domain.BillParticipantSummary, error) {
	session, err := s.billRepo.FindActiveByChatID(ctx, chatID)
	if err != nil {
		return domain.BillSession{}, nil, err
	}
	return session, s.calculateSummary(session), nil
}

func (s *BillService) MySummary(ctx context.Context, chatID int64, userID int64) (domain.BillSession, domain.BillParticipantSummary, error) {
	session, summary, err := s.Summary(ctx, chatID)
	if err != nil {
		return domain.BillSession{}, domain.BillParticipantSummary{}, err
	}

	for _, row := range summary {
		if row.UserID == userID {
			return session, row, nil
		}
	}
	return session, domain.BillParticipantSummary{}, fmt.Errorf("у вас пока нет выбранных позиций")
}

func (s *BillService) LatestUserSummary(ctx context.Context, chatIDs []int64, userID int64) (domain.BillSession, domain.BillParticipantSummary, error) {
	session, err := s.billRepo.FindLatestByChatIDsAndUserID(ctx, chatIDs, userID)
	if err != nil {
		if err == mongorepo.ErrBillSessionNotFound {
			return domain.BillSession{}, domain.BillParticipantSummary{}, fmt.Errorf("для вас не найдено ни одного счета")
		}
		return domain.BillSession{}, domain.BillParticipantSummary{}, err
	}

	summary := s.calculateSummary(session)
	for _, row := range summary {
		if row.UserID == userID {
			return session, row, nil
		}
	}

	return domain.BillSession{}, domain.BillParticipantSummary{}, fmt.Errorf("для вас не найдено ни одного счета")
}

func (s *BillService) calculateSummary(session domain.BillSession) []domain.BillParticipantSummary {
	userNameByID := make(map[int64]string)
	subtotalByUser := make(map[int64]decimal.Decimal)
	totalAssigned := decimal.Zero

	for _, assignment := range session.Assignments {
		userNameByID[assignment.UserID] = assignment.UserName
	}

	itemByIndex := make(map[int]domain.BillItem, len(session.Items))
	for _, item := range session.Items {
		itemByIndex[item.Index] = item
	}

	assignmentsByItem := make(map[int][]domain.BillAssignment)
	for _, assignment := range session.Assignments {
		assignmentsByItem[assignment.ItemIndex] = append(assignmentsByItem[assignment.ItemIndex], assignment)
	}

	for itemIndex, assignments := range assignmentsByItem {
		item := itemByIndex[itemIndex]
		assignedQuantity := 0
		for _, assignment := range assignments {
			assignedQuantity += assignment.Quantity
		}

		if assignedQuantity > item.Quantity {
			share := item.LineTotal.Div(decimal.NewFromInt(int64(assignedQuantity)))
			for _, assignment := range assignments {
				linePart := share.Mul(decimal.NewFromInt(int64(assignment.Quantity)))
				subtotalByUser[assignment.UserID] = subtotalByUser[assignment.UserID].Add(linePart)
				totalAssigned = totalAssigned.Add(linePart)
			}
			continue
		}

		for _, assignment := range assignments {
			linePart := item.UnitPrice.Mul(decimal.NewFromInt(int64(assignment.Quantity)))
			subtotalByUser[assignment.UserID] = subtotalByUser[assignment.UserID].Add(linePart)
			totalAssigned = totalAssigned.Add(linePart)
		}
	}

	summary := make([]domain.BillParticipantSummary, 0, len(subtotalByUser))
	for userID, subtotal := range subtotalByUser {
		serviceShare := decimal.Zero
		if totalAssigned.GreaterThan(decimal.Zero) && session.ServiceAmount.GreaterThan(decimal.Zero) {
			serviceShare = session.ServiceAmount.Mul(subtotal).Div(totalAssigned)
		}
		summary = append(summary, domain.BillParticipantSummary{
			UserID:       userID,
			UserName:     userNameByID[userID],
			ItemsTotal:   subtotal,
			ServiceShare: serviceShare,
			GrandTotal:   subtotal.Add(serviceShare),
		})
	}

	sort.Slice(summary, func(i, j int) bool {
		if summary[i].GrandTotal.Equal(summary[j].GrandTotal) {
			return summary[i].UserName < summary[j].UserName
		}
		return summary[i].GrandTotal.GreaterThan(summary[j].GrandTotal)
	})

	return summary
}

func (s *BillService) ensureChatAllowed(ctx context.Context, chatID int64) error {
	if _, err := s.chatRepo.FindActiveByChatID(ctx, chatID); err != nil {
		if err == mongorepo.ErrChatNotFound {
			return fmt.Errorf("чат не зарегистрирован для игры")
		}
		return err
	}
	return nil
}

func (s *BillService) ensureBillSessionCanBeCreated(ctx context.Context, chatID int64) error {
	if err := s.ensureChatAllowed(ctx, chatID); err != nil {
		return err
	}
	if _, err := s.billRepo.FindActiveByChatID(ctx, chatID); err == nil {
		return fmt.Errorf("в чате уже есть активный счет")
	} else if err != nil && err != mongorepo.ErrBillSessionNotFound {
		return err
	}
	return nil
}

func debugParsedReceipt() domain.ParsedReceipt {
	return domain.ParsedReceipt{
		MerchantName:  "Ne Gorchit на Маркова 79",
		ServiceAmount: decimal.NewFromInt(15862),
		TotalAmount:   decimal.NewFromInt(174482),
		Items: []domain.ParsedReceiptItem{
			{Name: "Kronenbourg Blanc 0,5", Quantity: 13, UnitPrice: decimal.NewFromInt(3290), LineTotal: decimal.NewFromInt(42770)},
			{Name: "Арахис 100 гр", Quantity: 2, UnitPrice: decimal.NewFromInt(1990), LineTotal: decimal.NewFromInt(3980)},
			{Name: "Чечил 100 гр", Quantity: 2, UnitPrice: decimal.NewFromInt(2290), LineTotal: decimal.NewFromInt(4580)},
			{Name: "NEW Рамен сливочный", Quantity: 1, UnitPrice: decimal.NewFromInt(3990), LineTotal: decimal.NewFromInt(3990)},
			{Name: "Говядина (топпинг)", Quantity: 1, UnitPrice: decimal.NewFromInt(890), LineTotal: decimal.NewFromInt(890)},
			{Name: "Gold Табак Gold", Quantity: 4, UnitPrice: decimal.NewFromInt(8500), LineTotal: decimal.NewFromInt(34000)},
			{Name: "Long Island", Quantity: 2, UnitPrice: decimal.NewFromInt(3990), LineTotal: decimal.NewFromInt(7980)},
			{Name: "Газированные напитки 0.25 стекло (в ассортименте)", Quantity: 6, UnitPrice: decimal.NewFromInt(1290), LineTotal: decimal.NewFromInt(7740)},
			{Name: "Карбонара Говядина", Quantity: 2, UnitPrice: decimal.NewFromInt(3990), LineTotal: decimal.NewFromInt(7980)},
			{Name: "Guinness", Quantity: 1, UnitPrice: decimal.NewFromInt(4590), LineTotal: decimal.NewFromInt(4590)},
			{Name: "Лимон к чаю", Quantity: 1, UnitPrice: decimal.NewFromInt(700), LineTotal: decimal.NewFromInt(700)},
			{Name: "Замена Табак Gold", Quantity: 2, UnitPrice: decimal.NewFromInt(5000), LineTotal: decimal.NewFromInt(10000)},
			{Name: "Вода tassay 0.25 стекло в ассорт", Quantity: 1, UnitPrice: decimal.NewFromInt(990), LineTotal: decimal.NewFromInt(990)},
			{Name: "NEW Вок с курицей и соусом сладкий чили", Quantity: 1, UnitPrice: decimal.NewFromInt(4990), LineTotal: decimal.NewFromInt(4990)},
			{Name: "NEW Кесадилья с чили кон карне", Quantity: 1, UnitPrice: decimal.NewFromInt(3990), LineTotal: decimal.NewFromInt(3990)},
			{Name: "NEW Салат с креветками и рукколой", Quantity: 1, UnitPrice: decimal.NewFromInt(4390), LineTotal: decimal.NewFromInt(4390)},
			{Name: "Марроканский чай", Quantity: 1, UnitPrice: decimal.NewFromInt(3790), LineTotal: decimal.NewFromInt(3790)},
			{Name: "NEW Бедро цыпленка под соусом тонкацу", Quantity: 2, UnitPrice: decimal.NewFromInt(3990), LineTotal: decimal.NewFromInt(7980)},
			{Name: "Вода Tassay Excellent", Quantity: 1, UnitPrice: decimal.NewFromInt(3290), LineTotal: decimal.NewFromInt(3290)},
		},
	}
}
