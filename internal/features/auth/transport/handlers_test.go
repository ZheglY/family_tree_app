package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/ZheglY/family_tree_app/internal/features/auth/ratelimit"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
)

type authServiceStub struct {
	session         model.Session
	refreshedWith   string
	logoutAllUserID uuid.UUID
	revokedCount    int64
	sessions        []model.UserSession
	revokedSession  uuid.UUID
	changePassword  service.ChangePasswordCommand
	forgotEmail     string
	resetPassword   service.ResetPasswordCommand
	err             error
}

type rateLimiterStub struct {
	decision ratelimit.Decision
	err      error
	scopes   []string
}

func (s *rateLimiterStub) Allow(
	_ context.Context,
	scope string,
	_ string,
	_ ratelimit.Rule,
) (ratelimit.Decision, error) {
	s.scopes = append(s.scopes, scope)
	return s.decision, s.err
}

func (s *authServiceStub) Register(
	context.Context,
	service.RegisterCommand,
) (service.RegisterResult, error) {
	return service.RegisterResult{User: s.session.User, VerificationRequired: true}, s.err
}

func (s *authServiceStub) VerifyEmail(context.Context, string) (model.User, error) {
	return s.session.User, s.err
}

func (s *authServiceStub) Login(
	context.Context,
	service.LoginCommand,
) (model.Session, error) {
	return s.session, s.err
}

func (s *authServiceStub) Refresh(
	_ context.Context,
	command service.RefreshCommand,
) (model.Session, error) {
	s.refreshedWith = command.RefreshToken
	return s.session, s.err
}

func (s *authServiceStub) Logout(context.Context, string) error {
	return s.err
}

func (s *authServiceStub) LogoutAll(
	_ context.Context,
	userID uuid.UUID,
) (int64, error) {
	s.logoutAllUserID = userID
	return s.revokedCount, s.err
}

func (s *authServiceStub) GetUser(context.Context, uuid.UUID) (model.User, error) {
	return s.session.User, s.err
}

func (s *authServiceStub) ListSessions(
	context.Context,
	uuid.UUID,
) ([]model.UserSession, error) {
	return s.sessions, s.err
}

func (s *authServiceStub) RevokeSession(
	_ context.Context,
	_ uuid.UUID,
	sessionID uuid.UUID,
) error {
	s.revokedSession = sessionID
	return s.err
}

func (s *authServiceStub) ChangePassword(
	_ context.Context,
	command service.ChangePasswordCommand,
) error {
	s.changePassword = command
	return s.err
}

func (s *authServiceStub) ForgotPassword(_ context.Context, email string) error {
	s.forgotEmail = email
	return s.err
}

func (s *authServiceStub) ResetPassword(
	_ context.Context,
	command service.ResetPasswordCommand,
) error {
	s.resetPassword = command
	return s.err
}

func TestLoginSetsProtectedRefreshCookieWithoutLeakingTokenInJSON(t *testing.T) {
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"email":"family@example.com","password":"correct horse battery staple"}`),
	)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("User-Agent", "test browser")
	recorder := serveHandler(handler.Login, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "refresh-token") {
		t.Fatalf("refresh token leaked in response: %s", recorder.Body)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Value != "refresh-token" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/v1/auth" {
		t.Fatalf("refresh cookie = %#v", cookie)
	}
}

func TestRefreshReadsCookieAndRotatesIt(t *testing.T) {
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "family_tree_refresh", Value: "old-refresh-token"})
	recorder := serveHandler(handler.Refresh, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if serviceStub.refreshedWith != "old-refresh-token" {
		t.Fatalf("service refresh token = %q", serviceStub.refreshedWith)
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 1 || cookies[0].Value != "refresh-token" {
		t.Fatalf("rotated cookies = %#v", cookies)
	}
}

func TestRefreshWithoutCookieReturnsUnauthorizedEnvelope(t *testing.T) {
	handler := testHandler(t, &authServiceStub{session: testSession()})
	recorder := serveHandler(
		handler.Refresh,
		httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil),
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	var envelope response.ErrorEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != "unauthorized" || envelope.Error.RequestID == "" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestLogoutAllUsesAuthenticatedPrincipal(t *testing.T) {
	userID := uuid.New()
	serviceStub := &authServiceStub{session: testSession(), revokedCount: 3}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout-all", nil)
	request = request.WithContext(access.NewPrincipalContext(
		request.Context(),
		access.Principal{UserID: userID, SessionID: uuid.New()},
	))
	recorder := serveHandler(handler.LogoutAll, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if serviceStub.logoutAllUserID != userID {
		t.Fatalf("logout-all user ID = %s, want %s", serviceStub.logoutAllUserID, userID)
	}
}

func TestRegisterRejectsUnknownJSONField(t *testing.T) {
	handler := testHandler(t, &authServiceStub{session: testSession()})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/register",
		strings.NewReader(`{"email":"family@example.com","unexpected":true}`),
	)
	recorder := serveHandler(handler.Register, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestListSessionsMarksCurrentSession(t *testing.T) {
	currentSessionID := uuid.New()
	serviceStub := &authServiceStub{
		session: testSession(),
		sessions: []model.UserSession{
			{ID: currentSessionID, UserAgent: "current browser", CreatedAt: time.Now(), LastUsedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
			{ID: uuid.New(), UserAgent: "other browser", CreatedAt: time.Now(), LastUsedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/sessions", nil)
	request = request.WithContext(access.NewPrincipalContext(
		request.Context(),
		access.Principal{UserID: serviceStub.session.User.ID, SessionID: currentSessionID},
	))
	recorder := serveHandler(handler.ListSessions, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body sessionsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode sessions response: %v", err)
	}
	if len(body.Items) != 2 || !body.Items[0].Current || body.Items[1].Current {
		t.Fatalf("sessions response = %#v", body.Items)
	}
}

func TestRevokeCurrentSessionClearsRefreshCookie(t *testing.T) {
	currentSessionID := uuid.New()
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/users/me/sessions/"+currentSessionID.String(),
		nil,
	)
	request.SetPathValue("session_id", currentSessionID.String())
	request = request.WithContext(access.NewPrincipalContext(
		request.Context(),
		access.Principal{UserID: serviceStub.session.User.ID, SessionID: currentSessionID},
	))
	recorder := serveHandler(handler.RevokeSession, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if serviceStub.revokedSession != currentSessionID {
		t.Fatalf("revoked session = %s, want %s", serviceStub.revokedSession, currentSessionID)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("cleared cookies = %#v", cookies)
	}
}

func TestChangePasswordUsesAuthenticatedPrincipalAndClearsCookie(t *testing.T) {
	userID := uuid.New()
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/users/me/change-password",
		strings.NewReader(`{"current_password":"current value","new_password":"new correct horse battery staple"}`),
	)
	request = request.WithContext(access.NewPrincipalContext(
		request.Context(),
		access.Principal{UserID: userID, SessionID: uuid.New()},
	))
	recorder := serveHandler(handler.ChangePassword, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if serviceStub.changePassword.UserID != userID ||
		serviceStub.changePassword.CurrentPassword != "current value" ||
		serviceStub.changePassword.NewPassword != "new correct horse battery staple" {
		t.Fatalf("change password command = %#v", serviceStub.changePassword)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("cleared cookies = %#v", cookies)
	}
}

func TestForgotPasswordReturnsGenericAcceptedResponse(t *testing.T) {
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/forgot-password",
		strings.NewReader(`{"email":"missing@example.com"}`),
	)
	recorder := serveHandler(handler.ForgotPassword, request)

	if recorder.Code != http.StatusAccepted || recorder.Body.Len() != 0 {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if serviceStub.forgotEmail != "missing@example.com" {
		t.Fatalf("forgot email = %q", serviceStub.forgotEmail)
	}
}

func TestResetPasswordClearsRefreshCookie(t *testing.T) {
	serviceStub := &authServiceStub{session: testSession()}
	handler := testHandler(t, serviceStub)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/reset-password",
		strings.NewReader(`{"token":"reset-token","new_password":"new correct horse battery staple"}`),
	)
	recorder := serveHandler(handler.ResetPassword, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if serviceStub.resetPassword.Token != "reset-token" ||
		serviceStub.resetPassword.NewPassword != "new correct horse battery staple" {
		t.Fatalf("reset command = %#v", serviceStub.resetPassword)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("cleared cookies = %#v", cookies)
	}
}

func TestForgotPasswordRateLimitReturnsRetryAfter(t *testing.T) {
	serviceStub := &authServiceStub{session: testSession()}
	limiter := &rateLimiterStub{decision: ratelimit.Decision{
		RetryAfter: 30 * time.Second,
	}}
	handler := testHandlerWithLimiter(t, serviceStub, limiter)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/forgot-password",
		strings.NewReader(`{"email":"family@example.com"}`),
	)
	recorder := serveHandler(handler.ForgotPassword, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("Retry-After") != "30" {
		t.Fatalf("Retry-After = %q", recorder.Header().Get("Retry-After"))
	}
	if serviceStub.forgotEmail != "" {
		t.Fatal("forgot-password service called after rate limit rejection")
	}
}

func TestRateLimitBackendFailureReturnsServiceUnavailable(t *testing.T) {
	handler := testHandlerWithLimiter(
		t,
		&authServiceStub{session: testSession()},
		&rateLimiterStub{err: errors.New("Redis unavailable")},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"email":"family@example.com","password":"password"}`),
	)
	recorder := serveHandler(handler.Login, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
}

func testHandler(t *testing.T, serviceStub AuthService) *Handler {
	return testHandlerWithLimiter(t, serviceStub, &rateLimiterStub{
		decision: ratelimit.Decision{Allowed: true},
	})
}

func testHandlerWithLimiter(
	t *testing.T,
	serviceStub AuthService,
	limiter AuthRateLimiter,
) *Handler {
	t.Helper()
	refreshCookie, err := NewRefreshCookie(CookieConfig{
		Name:     "family_tree_refresh",
		Path:     "/api/v1/auth",
		Secure:   true,
		SameSite: "strict",
	})
	if err != nil {
		t.Fatalf("NewRefreshCookie() error = %v", err)
	}
	return NewHandler(
		serviceStub,
		refreshCookie,
		func(next http.Handler) http.Handler { return next },
		limiter,
	)
}

func serveHandler(handler http.HandlerFunc, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	wrapped := coremiddleware.ChainMiddleware(
		handler,
		coremiddleware.RequestID(),
		coremiddleware.Logger(logger.NewNop()),
	)
	wrapped.ServeHTTP(recorder, request)
	return recorder
}

func testSession() model.Session {
	return model.Session{
		User: model.User{
			ID:          uuid.New(),
			Email:       "family@example.com",
			DisplayName: "Family Member",
			Status:      "active",
		},
		AccessToken:           "access-token",
		RefreshToken:          "refresh-token",
		AccessTokenExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
		RefreshTokenExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
}
