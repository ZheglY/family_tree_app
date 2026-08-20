package transport

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/request"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
)

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type userResponse struct {
	User model.User `json:"user"`
}

type registerResponse struct {
	User                 model.User `json:"user"`
	VerificationRequired bool       `json:"verification_required"`
}

type sessionResponse struct {
	User                 model.User `json:"user"`
	AccessToken          string     `json:"access_token"`
	AccessTokenExpiresAt time.Time  `json:"access_token_expires_at"`
}

type logoutAllResponse struct {
	RevokedSessionCount int64 `json:"revoked_session_count"`
}

func (h *Handler) Register(rw http.ResponseWriter, httpRequest *http.Request) {
	var body registerRequest
	if !decodeRequest(rw, httpRequest, &body) {
		return
	}
	if !h.allowAuthAttempt(
		rw, httpRequest, "register", body.Email, registerIPRule, registerAccountRule,
	) {
		return
	}

	result, err := h.service.Register(httpRequest.Context(), service.RegisterCommand{
		Email:       body.Email,
		Password:    body.Password,
		DisplayName: body.DisplayName,
	})
	if err != nil {
		writeError(rw, httpRequest, err, "Registration failed")
		return
	}

	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).JSONResponse(
		registerResponse{User: result.User, VerificationRequired: result.VerificationRequired},
		http.StatusCreated,
	)
}

func (h *Handler) VerifyEmail(rw http.ResponseWriter, httpRequest *http.Request) {
	var body verifyEmailRequest
	if !decodeRequest(rw, httpRequest, &body) {
		return
	}
	if !h.allowAuthAttempt(
		rw, httpRequest, "verify", "", verifyIPRule, noAccountRule,
	) {
		return
	}

	user, err := h.service.VerifyEmail(httpRequest.Context(), body.Token)
	if err != nil {
		writeError(rw, httpRequest, err, "Email verification failed")
		return
	}

	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).JSONResponse(
		userResponse{User: user},
		http.StatusOK,
	)
}

func (h *Handler) Login(rw http.ResponseWriter, httpRequest *http.Request) {
	var body loginRequest
	if !decodeRequest(rw, httpRequest, &body) {
		return
	}
	if !h.allowAuthAttempt(
		rw, httpRequest, "login", body.Email, loginIPRule, loginAccountRule,
	) {
		return
	}

	session, err := h.service.Login(httpRequest.Context(), service.LoginCommand{
		Email:     body.Email,
		Password:  body.Password,
		UserAgent: httpRequest.UserAgent(),
		IPAddress: remoteIPAddress(httpRequest),
	})
	if err != nil {
		writeError(rw, httpRequest, err, "Login failed")
		return
	}

	h.refreshCookie.Set(rw, session.RefreshToken, session.RefreshTokenExpiresAt, time.Now().UTC())
	writeSession(rw, httpRequest, session)
}

func (h *Handler) Refresh(rw http.ResponseWriter, httpRequest *http.Request) {
	if !h.allowAuthAttempt(
		rw, httpRequest, "refresh", "", refreshIPRule, noAccountRule,
	) {
		return
	}
	refreshToken, ok := h.refreshCookie.Read(httpRequest)
	if !ok {
		writeError(rw, httpRequest, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}

	session, err := h.service.Refresh(httpRequest.Context(), service.RefreshCommand{
		RefreshToken: refreshToken,
		UserAgent:    httpRequest.UserAgent(),
		IPAddress:    remoteIPAddress(httpRequest),
	})
	if err != nil {
		if errors.Is(err, apperrors.ErrInvalidCredentials) || errors.Is(err, apperrors.ErrUnauthorized) {
			h.refreshCookie.Clear(rw)
		}
		writeError(rw, httpRequest, err, "Session refresh failed")
		return
	}

	h.refreshCookie.Set(rw, session.RefreshToken, session.RefreshTokenExpiresAt, time.Now().UTC())
	writeSession(rw, httpRequest, session)
}

func (h *Handler) Logout(rw http.ResponseWriter, httpRequest *http.Request) {
	refreshToken, ok := h.refreshCookie.Read(httpRequest)
	h.refreshCookie.Clear(rw)
	if ok {
		if err := h.service.Logout(httpRequest.Context(), refreshToken); err != nil {
			writeError(rw, httpRequest, err, "Logout failed")
			return
		}
	}

	rw.WriteHeader(http.StatusNoContent)
}

func (h *Handler) LogoutAll(rw http.ResponseWriter, httpRequest *http.Request) {
	principal, ok := access.PrincipalFromContext(httpRequest.Context())
	if !ok {
		writeError(rw, httpRequest, apperrors.ErrUnauthorized, "Authentication is required")
		return
	}

	revokedCount, err := h.service.LogoutAll(httpRequest.Context(), principal.UserID)
	if err != nil {
		writeError(rw, httpRequest, err, "Logout failed")
		return
	}
	h.refreshCookie.Clear(rw)
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).JSONResponse(
		logoutAllResponse{RevokedSessionCount: revokedCount},
		http.StatusOK,
	)
}

func (h *Handler) ForgotPassword(rw http.ResponseWriter, httpRequest *http.Request) {
	var body forgotPasswordRequest
	if !decodeRequest(rw, httpRequest, &body) {
		return
	}
	if !h.allowAuthAttempt(
		rw, httpRequest, "forgot", body.Email, forgotIPRule, forgotAccountRule,
	) {
		return
	}

	if err := h.service.ForgotPassword(httpRequest.Context(), body.Email); err != nil {
		writeError(rw, httpRequest, err, "Password recovery request failed")
		return
	}
	// The same response is returned regardless of whether the account exists.
	rw.WriteHeader(http.StatusAccepted)
}

func (h *Handler) ResetPassword(rw http.ResponseWriter, httpRequest *http.Request) {
	var body resetPasswordRequest
	if !decodeRequest(rw, httpRequest, &body) {
		return
	}
	if !h.allowAuthAttempt(
		rw, httpRequest, "reset", "", resetIPRule, noAccountRule,
	) {
		return
	}

	if err := h.service.ResetPassword(httpRequest.Context(), service.ResetPasswordCommand{
		Token:       body.Token,
		NewPassword: body.NewPassword,
	}); err != nil {
		writeError(rw, httpRequest, err, "Password reset failed")
		return
	}
	h.refreshCookie.Clear(rw)
	rw.WriteHeader(http.StatusNoContent)
}

func decodeRequest(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	destination any,
) bool {
	if err := request.DecodeJSON(httpRequest.Body, destination); err != nil {
		writeError(
			rw,
			httpRequest,
			fmt.Errorf("%w: %v", apperrors.ErrInvalidArgument, err),
			"Request body is invalid",
		)
		return false
	}
	return true
}

func writeSession(rw http.ResponseWriter, httpRequest *http.Request, session model.Session) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).JSONResponse(
		sessionResponse{
			User:                 session.User,
			AccessToken:          session.AccessToken,
			AccessTokenExpiresAt: session.AccessTokenExpiresAt,
		},
		http.StatusOK,
	)
}

func writeError(
	rw http.ResponseWriter,
	httpRequest *http.Request,
	err error,
	message string,
) {
	response.NewHTTPResponseHandler(logger.FromContext(httpRequest.Context()), rw).
		ErrorResponse(err, message)
}

func remoteIPAddress(httpRequest *http.Request) string {
	host, _, err := net.SplitHostPort(httpRequest.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(httpRequest.RemoteAddr) != nil {
		return httpRequest.RemoteAddr
	}
	return ""
}
