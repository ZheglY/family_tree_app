package transport

import (
	"context"
	"net/http"

	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
)

type AuthService interface {
	Register(context.Context, service.RegisterCommand) (service.RegisterResult, error)
	VerifyEmail(context.Context, string) (model.User, error)
	Login(context.Context, service.LoginCommand) (model.Session, error)
	Refresh(context.Context, service.RefreshCommand) (model.Session, error)
	Logout(context.Context, string) error
	LogoutAll(context.Context, uuid.UUID) (int64, error)
	GetUser(context.Context, uuid.UUID) (model.User, error)
	ListSessions(context.Context, uuid.UUID) ([]model.UserSession, error)
	RevokeSession(context.Context, uuid.UUID, uuid.UUID) error
	ChangePassword(context.Context, service.ChangePasswordCommand) error
	ForgotPassword(context.Context, string) error
	ResetPassword(context.Context, service.ResetPasswordCommand) error
}

type Handler struct {
	service       AuthService
	refreshCookie *RefreshCookie
	requireAccess coremiddleware.Middleware
	rateLimiter   AuthRateLimiter
}

func NewHandler(
	service AuthService,
	refreshCookie *RefreshCookie,
	requireAccess coremiddleware.Middleware,
	rateLimiter AuthRateLimiter,
) *Handler {
	return &Handler{
		service:       service,
		refreshCookie: refreshCookie,
		requireAccess: requireAccess,
		rateLimiter:   rateLimiter,
	}
}

func (h *Handler) Routes() []server.Route {
	return []server.Route{
		{Method: http.MethodPost, Path: "/auth/register", Handler: h.Register},
		{Method: http.MethodPost, Path: "/auth/verify-email", Handler: h.VerifyEmail},
		{Method: http.MethodPost, Path: "/auth/login", Handler: h.Login},
		{Method: http.MethodPost, Path: "/auth/refresh", Handler: h.Refresh},
		{Method: http.MethodPost, Path: "/auth/logout", Handler: h.Logout},
		{Method: http.MethodPost, Path: "/auth/forgot-password", Handler: h.ForgotPassword},
		{Method: http.MethodPost, Path: "/auth/reset-password", Handler: h.ResetPassword},
		{
			Method:     http.MethodPost,
			Path:       "/auth/logout-all",
			Handler:    h.LogoutAll,
			Middleware: []coremiddleware.Middleware{h.requireAccess},
		},
		{
			Method:     http.MethodPost,
			Path:       "/users/me/change-password",
			Handler:    h.ChangePassword,
			Middleware: []coremiddleware.Middleware{h.requireAccess},
		},
		{
			Method:     http.MethodGet,
			Path:       "/users/me",
			Handler:    h.GetMe,
			Middleware: []coremiddleware.Middleware{h.requireAccess},
		},
		{
			Method:     http.MethodGet,
			Path:       "/users/me/sessions",
			Handler:    h.ListSessions,
			Middleware: []coremiddleware.Middleware{h.requireAccess},
		},
		{
			Method:     http.MethodDelete,
			Path:       "/users/me/sessions/{session_id}",
			Handler:    h.RevokeSession,
			Middleware: []coremiddleware.Middleware{h.requireAccess},
		},
	}
}
