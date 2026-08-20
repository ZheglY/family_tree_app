package transport

import (
	"fmt"
	"net/http"
	"time"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
)

type sessionItemResponse struct {
	ID         uuid.UUID `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

type sessionsResponse struct {
	Items []sessionItemResponse `json:"items"`
}

func (h *Handler) GetMe(rw http.ResponseWriter, request *http.Request) {
	principal, ok := access.PrincipalFromContext(request.Context())
	if !ok {
		writeError(rw, request, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}

	user, err := h.service.GetUser(request.Context(), principal.UserID)
	if err != nil {
		writeError(rw, request, err, "User profile could not be loaded")
		return
	}
	response.NewHTTPResponseHandler(logger.FromContext(request.Context()), rw).JSONResponse(
		userResponse{User: user},
		http.StatusOK,
	)
}

func (h *Handler) ListSessions(rw http.ResponseWriter, request *http.Request) {
	principal, ok := access.PrincipalFromContext(request.Context())
	if !ok {
		writeError(rw, request, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}

	sessions, err := h.service.ListSessions(request.Context(), principal.UserID)
	if err != nil {
		writeError(rw, request, err, "Sessions could not be loaded")
		return
	}
	items := make([]sessionItemResponse, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, mapSessionItem(session, principal.SessionID))
	}
	response.NewHTTPResponseHandler(logger.FromContext(request.Context()), rw).JSONResponse(
		sessionsResponse{Items: items},
		http.StatusOK,
	)
}

func (h *Handler) RevokeSession(rw http.ResponseWriter, request *http.Request) {
	principal, ok := access.PrincipalFromContext(request.Context())
	if !ok {
		writeError(rw, request, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		writeError(
			rw,
			request,
			fmt.Errorf("%w: session ID is invalid", apperrors.ErrInvalidArgument),
			"Session ID is invalid",
		)
		return
	}

	if err := h.service.RevokeSession(request.Context(), principal.UserID, sessionID); err != nil {
		writeError(rw, request, err, "Session could not be revoked")
		return
	}
	if sessionID == principal.SessionID {
		h.refreshCookie.Clear(rw)
	}
	rw.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) ChangePassword(rw http.ResponseWriter, request *http.Request) {
	principal, ok := access.PrincipalFromContext(request.Context())
	if !ok {
		writeError(rw, request, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}
	var body changePasswordRequest
	if !decodeRequest(rw, request, &body) {
		return
	}

	if err := h.service.ChangePassword(request.Context(), service.ChangePasswordCommand{
		UserID:          principal.UserID,
		CurrentPassword: body.CurrentPassword,
		NewPassword:     body.NewPassword,
	}); err != nil {
		writeError(rw, request, err, "Password change failed")
		return
	}
	h.refreshCookie.Clear(rw)
	rw.WriteHeader(http.StatusNoContent)
}

func mapSessionItem(session model.UserSession, currentSessionID uuid.UUID) sessionItemResponse {
	return sessionItemResponse{
		ID:         session.ID,
		UserAgent:  session.UserAgent,
		IPAddress:  session.IPAddress,
		CreatedAt:  session.CreatedAt,
		LastUsedAt: session.LastUsedAt,
		ExpiresAt:  session.ExpiresAt,
		Current:    session.ID == currentSessionID,
	}
}
