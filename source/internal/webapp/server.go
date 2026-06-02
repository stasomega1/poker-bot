package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"poker-bot/internal/domain"
	"poker-bot/internal/service"
)

//go:embed static
var staticFiles embed.FS

type BillNotifier interface {
	UserHasAccessToChat(ctx context.Context, chatID, userID int64) (bool, string, error)
	RefreshBillMessage(ctx context.Context, session domain.BillSession)
	PublishBillFinished(ctx context.Context, session domain.BillSession, summary []domain.BillParticipantSummary)
	PublishBillCancelled(ctx context.Context, session domain.BillSession)
}

type Server struct {
	httpServer *http.Server
	bill       *service.BillService
	validator  *InitDataValidator
	notifier   BillNotifier
	devAuth    *AuthData
}

func NewServer(addr string, bill *service.BillService, validator *InitDataValidator, notifier BillNotifier, devAuth *AuthData) (*Server, error) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}

	s := &Server{
		bill:      bill,
		validator: validator,
		notifier:  notifier,
		devAuth:   devAuth,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/webapp/bills/", s.handleBillAPI)
	mux.Handle("/app/", http.StripPrefix("/app/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusFound)
	})

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		log.Printf("webapp http server started: addr=%s", s.httpServer.Addr)
		err := s.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleBillAPI(w http.ResponseWriter, r *http.Request) {
	setAPIHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	auth, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/webapp/bills/"), "/")
	parts := splitPath(path)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "bill not found")
		return
	}
	sessionID := parts[0]

	session, summary, ok := s.loadBillForUser(w, r.Context(), sessionID, auth.User)
	if !ok {
		return
	}

	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		writeJSON(w, http.StatusOK, presentBill(session, summary, auth.User))
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "items" && parts[3] == "adjust":
		s.handleAdjust(w, r, session, parts[2], auth.User)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "items" && parts[3] == "expected-participants":
		s.handleExpectedParticipants(w, r, session, parts[2], auth.User)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "items" && parts[3] == "split":
		s.handleSplit(w, r, session, parts[2], auth.User)
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "finish":
		s.handleFinish(w, r, session, auth.User)
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "cancel":
		s.handleCancel(w, r, session, auth.User)
	default:
		writeError(w, http.StatusNotFound, "endpoint not found")
	}
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (AuthData, bool) {
	raw := r.Header.Get("X-Telegram-Init-Data")
	if raw == "" {
		raw = r.URL.Query().Get("initData")
	}
	if strings.TrimSpace(raw) == "" && s.devAuth != nil && isLocalRequest(r) {
		return *s.devAuth, true
	}
	auth, err := s.validator.Validate(raw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return AuthData{}, false
	}
	return auth, true
}

func (s *Server) loadBillForUser(w http.ResponseWriter, ctx context.Context, sessionID string, user TelegramUser) (domain.BillSession, []domain.BillParticipantSummary, bool) {
	session, summary, err := s.bill.SummaryBySessionID(ctx, sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("bill not found: %v", err))
		return domain.BillSession{}, nil, false
	}
	if s.notifier != nil {
		allowed, _, err := s.notifier.UserHasAccessToChat(ctx, session.ChatID, user.ID)
		if err != nil {
			writeError(w, http.StatusForbidden, "could not verify chat membership")
			return domain.BillSession{}, nil, false
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "no access to this chat")
			return domain.BillSession{}, nil, false
		}
	}
	return session, summary, true
}

func (s *Server) handleAdjust(w http.ResponseWriter, r *http.Request, session domain.BillSession, rawItemIndex string, user TelegramUser) {
	if session.Status != domain.BillSessionActive {
		writeError(w, http.StatusConflict, "bill is not active")
		return
	}

	itemIndex, err := strconv.Atoi(rawItemIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item index")
		return
	}

	var request struct {
		Delta int `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if request.Delta != 1 && request.Delta != -1 {
		writeError(w, http.StatusBadRequest, "delta must be 1 or -1")
		return
	}

	updated, err := s.bill.AdjustItem(r.Context(), session.ID, user.ID, DisplayUserName(user), itemIndex, request.Delta)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyRefresh(r.Context(), updated)
	s.respondUpdatedBill(w, r.Context(), updated.ID, user)
}

func (s *Server) handleSplit(w http.ResponseWriter, r *http.Request, session domain.BillSession, rawItemIndex string, user TelegramUser) {
	if !canManageBill(session, user) {
		writeError(w, http.StatusForbidden, "only bill creator or payer can split items")
		return
	}
	itemIndex, err := strconv.Atoi(rawItemIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item index")
		return
	}

	updated, err := s.bill.SplitItemIntoSingles(r.Context(), session.ID, itemIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyRefresh(r.Context(), updated)
	s.respondUpdatedBill(w, r.Context(), updated.ID, user)
}

func (s *Server) handleExpectedParticipants(w http.ResponseWriter, r *http.Request, session domain.BillSession, rawItemIndex string, user TelegramUser) {
	if session.Status != domain.BillSessionActive {
		writeError(w, http.StatusConflict, "bill is not active")
		return
	}

	itemIndex, err := strconv.Atoi(rawItemIndex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item index")
		return
	}

	var request struct {
		ExpectedParticipants int `json:"expectedParticipants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	updated, err := s.bill.SetExpectedParticipants(r.Context(), session.ID, user.ID, itemIndex, request.ExpectedParticipants)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifyRefresh(r.Context(), updated)
	s.respondUpdatedBill(w, r.Context(), updated.ID, user)
}

func (s *Server) handleFinish(w http.ResponseWriter, r *http.Request, session domain.BillSession, user TelegramUser) {
	if !canManageBill(session, user) {
		writeError(w, http.StatusForbidden, "only bill creator or payer can finish the bill")
		return
	}

	var request struct {
		Force bool `json:"force"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}

	updated, summary, err := s.bill.FinishBySessionID(r.Context(), session.ID, request.Force)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.notifier != nil {
		s.notifier.RefreshBillMessage(r.Context(), updated)
		s.notifier.PublishBillFinished(r.Context(), updated, summary)
	}
	writeJSON(w, http.StatusOK, presentBill(updated, summary, user))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, session domain.BillSession, user TelegramUser) {
	if !canManageBill(session, user) {
		writeError(w, http.StatusForbidden, "only bill creator or payer can cancel the bill")
		return
	}

	updated, err := s.bill.CancelBySessionID(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.notifier != nil {
		s.notifier.PublishBillCancelled(r.Context(), updated)
	}
	s.respondUpdatedBill(w, r.Context(), updated.ID, user)
}

func (s *Server) respondUpdatedBill(w http.ResponseWriter, ctx context.Context, sessionID string, user TelegramUser) {
	session, summary, err := s.bill.SummaryBySessionID(ctx, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, presentBill(session, summary, user))
}

func (s *Server) notifyRefresh(ctx context.Context, session domain.BillSession) {
	if s.notifier != nil {
		s.notifier.RefreshBillMessage(ctx, session)
	}
}

func canManageBill(session domain.BillSession, user TelegramUser) bool {
	return session.CreatedByUserID == user.ID || (session.PayerUserID != 0 && session.PayerUserID == user.ID)
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func isLocalRequest(r *http.Request) bool {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	switch host {
	case "127.0.0.1", "localhost":
		return true
	default:
		return false
	}
}

func setAPIHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Telegram-Init-Data")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
