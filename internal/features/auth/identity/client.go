package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	identityv1 "github.com/ZheglY/family_tree_app/gen/identity/v1"
	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const requestIDMetadataKey = "x-request-id"

type Client struct {
	connection *grpc.ClientConn
	api        identityv1.IdentityServiceClient
	timeout    time.Duration
}

func NewClient(config Config) (*Client, error) {
	transportCredentials, err := newTransportCredentials(config)
	if err != nil {
		return nil, err
	}

	connection, err := grpc.NewClient(
		config.Address,
		grpc.WithTransportCredentials(transportCredentials),
	)
	if err != nil {
		return nil, fmt.Errorf("create Identity gRPC connection: %w", err)
	}

	return &Client{
		connection: connection,
		api:        identityv1.NewIdentityServiceClient(connection),
		timeout:    config.Timeout,
	}, nil
}

func newTransportCredentials(config Config) (credentials.TransportCredentials, error) {
	if !config.TLSEnabled {
		return insecure.NewCredentials(), nil
	}

	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system CA pool: %w", err)
	}
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if config.TLSCAFile != "" {
		certificate, err := os.ReadFile(config.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Identity CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(certificate) {
			return nil, fmt.Errorf("Identity CA file does not contain a valid certificate")
		}
	}

	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
		ServerName: config.TLSServerName,
	}), nil
}

func (c *Client) Close() error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.Close()
}

func (c *Client) Register(
	ctx context.Context,
	command service.RegisterCommand,
) (service.RegisterResult, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.Register(callContext, &identityv1.RegisterRequest{
		Email:       command.Email,
		Password:    command.Password,
		DisplayName: command.DisplayName,
	})
	if err != nil {
		return service.RegisterResult{}, mapError("register", err)
	}
	user, err := mapUser(response.GetUser())
	if err != nil {
		return service.RegisterResult{}, fmt.Errorf("map Identity registration response: %w", err)
	}

	return service.RegisterResult{
		User:                 user,
		VerificationRequired: response.GetVerificationRequired(),
	}, nil
}

func (c *Client) VerifyEmail(
	ctx context.Context,
	token string,
) (model.User, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.VerifyEmail(callContext, &identityv1.VerifyEmailRequest{Token: token})
	if err != nil {
		return model.User{}, mapError("verify email", err)
	}
	user, err := mapUser(response.GetUser())
	if err != nil {
		return model.User{}, fmt.Errorf("map Identity verification response: %w", err)
	}

	return user, nil
}

func (c *Client) Login(
	ctx context.Context,
	command service.LoginCommand,
) (model.Session, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.Login(callContext, &identityv1.LoginRequest{
		Email:     command.Email,
		Password:  command.Password,
		UserAgent: command.UserAgent,
		IpAddress: command.IPAddress,
	})
	if err != nil {
		return model.Session{}, mapError("login", err)
	}

	return mapSession(response)
}

func (c *Client) Refresh(
	ctx context.Context,
	command service.RefreshCommand,
) (model.Session, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.RefreshSession(callContext, &identityv1.RefreshSessionRequest{
		RefreshToken: command.RefreshToken,
		UserAgent:    command.UserAgent,
		IpAddress:    command.IPAddress,
	})
	if err != nil {
		return model.Session{}, mapError("refresh session", err)
	}

	return mapSession(response)
}

func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	if _, err := c.api.Logout(callContext, &identityv1.LogoutRequest{
		RefreshToken: refreshToken,
	}); err != nil {
		return mapError("logout", err)
	}

	return nil
}

func (c *Client) LogoutAll(
	ctx context.Context,
	userID uuid.UUID,
) (int64, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.LogoutAll(callContext, &identityv1.LogoutAllRequest{
		UserId: userID.String(),
	})
	if err != nil {
		return 0, mapError("logout all", err)
	}

	return response.GetRevokedSessionCount(), nil
}

func (c *Client) GetAccessTokenKey(ctx context.Context) (model.AccessTokenKey, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.GetAccessTokenPublicKey(
		callContext,
		&identityv1.GetAccessTokenPublicKeyRequest{},
	)
	if err != nil {
		return model.AccessTokenKey{}, mapError("get access token public key", err)
	}

	return model.AccessTokenKey{
		KeyID:           response.GetKeyId(),
		Algorithm:       response.GetAlgorithm(),
		PublicKeyBase64: response.GetPublicKeyBase64(),
		Issuer:          response.GetIssuer(),
		Audience:        response.GetAudience(),
	}, nil
}

func (c *Client) GetUser(
	ctx context.Context,
	userID uuid.UUID,
) (model.User, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.GetUser(callContext, &identityv1.GetUserRequest{
		UserId: userID.String(),
	})
	if err != nil {
		return model.User{}, mapError("get user", err)
	}
	user, err := mapUser(response.GetUser())
	if err != nil {
		return model.User{}, fmt.Errorf("map Identity user response: %w", err)
	}

	return user, nil
}

func (c *Client) ListSessions(
	ctx context.Context,
	userID uuid.UUID,
) ([]model.UserSession, error) {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	response, err := c.api.ListSessions(callContext, &identityv1.ListSessionsRequest{
		UserId: userID.String(),
	})
	if err != nil {
		return nil, mapError("list sessions", err)
	}
	sessions := make([]model.UserSession, 0, len(response.GetSessions()))
	for _, sessionResponse := range response.GetSessions() {
		session, err := mapUserSession(sessionResponse)
		if err != nil {
			return nil, fmt.Errorf("map Identity session response: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

func (c *Client) RevokeSession(
	ctx context.Context,
	userID uuid.UUID,
	sessionID uuid.UUID,
) error {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	if _, err := c.api.RevokeSession(callContext, &identityv1.RevokeSessionRequest{
		UserId:    userID.String(),
		SessionId: sessionID.String(),
	}); err != nil {
		return mapError("revoke session", err)
	}

	return nil
}

func (c *Client) ChangePassword(
	ctx context.Context,
	command service.ChangePasswordCommand,
) error {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	if _, err := c.api.ChangePassword(callContext, &identityv1.ChangePasswordRequest{
		UserId:          command.UserID.String(),
		CurrentPassword: command.CurrentPassword,
		NewPassword:     command.NewPassword,
	}); err != nil {
		return mapError("change password", err)
	}
	return nil
}

func (c *Client) ForgotPassword(ctx context.Context, email string) error {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	if _, err := c.api.ForgotPassword(callContext, &identityv1.ForgotPasswordRequest{
		Email: email,
	}); err != nil {
		return mapError("forgot password", err)
	}
	return nil
}

func (c *Client) ResetPassword(
	ctx context.Context,
	command service.ResetPasswordCommand,
) error {
	callContext, cancel := c.callContext(ctx)
	defer cancel()

	if _, err := c.api.ResetPassword(callContext, &identityv1.ResetPasswordRequest{
		Token:       command.Token,
		NewPassword: command.NewPassword,
	}); err != nil {
		return mapError("reset password", err)
	}
	return nil
}

func (c *Client) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if id := requestid.FromContext(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, requestIDMetadataKey, id)
	}
	return context.WithTimeout(ctx, c.timeout)
}

func mapUser(user *identityv1.User) (model.User, error) {
	if user == nil {
		return model.User{}, errors.New("Identity response does not contain a user")
	}
	userID, err := uuid.Parse(user.GetId())
	if err != nil {
		return model.User{}, fmt.Errorf("invalid user ID: %w", err)
	}

	return model.User{
		ID:          userID,
		Email:       user.GetEmail(),
		DisplayName: user.GetDisplayName(),
		Status:      mapUserStatus(user.GetStatus()),
	}, nil
}

func mapUserStatus(status identityv1.UserStatus) string {
	switch status {
	case identityv1.UserStatus_USER_STATUS_PENDING:
		return "pending"
	case identityv1.UserStatus_USER_STATUS_ACTIVE:
		return "active"
	case identityv1.UserStatus_USER_STATUS_BLOCKED:
		return "blocked"
	case identityv1.UserStatus_USER_STATUS_DELETING:
		return "deleting"
	default:
		return "unspecified"
	}
}

func mapSession(response *identityv1.SessionResponse) (model.Session, error) {
	if response == nil || response.GetAccessToken() == "" || response.GetRefreshToken() == "" ||
		response.GetAccessTokenExpiresAtUnix() <= 0 || response.GetRefreshTokenExpiresAtUnix() <= 0 {
		return model.Session{}, errors.New("Identity response contains an invalid session")
	}
	user, err := mapUser(response.GetUser())
	if err != nil {
		return model.Session{}, err
	}

	return model.Session{
		User:                  user,
		AccessToken:           response.GetAccessToken(),
		RefreshToken:          response.GetRefreshToken(),
		AccessTokenExpiresAt:  time.Unix(response.GetAccessTokenExpiresAtUnix(), 0).UTC(),
		RefreshTokenExpiresAt: time.Unix(response.GetRefreshTokenExpiresAtUnix(), 0).UTC(),
	}, nil
}

func mapUserSession(response *identityv1.UserSession) (model.UserSession, error) {
	if response == nil || response.GetCreatedAtUnix() <= 0 ||
		response.GetLastUsedAtUnix() <= 0 || response.GetExpiresAtUnix() <= 0 {
		return model.UserSession{}, errors.New("Identity response contains an invalid user session")
	}
	sessionID, err := uuid.Parse(response.GetId())
	if err != nil {
		return model.UserSession{}, fmt.Errorf("invalid session ID: %w", err)
	}

	return model.UserSession{
		ID:         sessionID,
		UserAgent:  response.GetUserAgent(),
		IPAddress:  response.GetIpAddress(),
		CreatedAt:  time.Unix(response.GetCreatedAtUnix(), 0).UTC(),
		LastUsedAt: time.Unix(response.GetLastUsedAtUnix(), 0).UTC(),
		ExpiresAt:  time.Unix(response.GetExpiresAtUnix(), 0).UTC(),
	}, nil
}

func mapError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: Identity %s deadline exceeded", apperrors.ErrServiceUnavailable, operation)
	}

	switch status.Code(err) {
	case codes.InvalidArgument:
		return fmt.Errorf("%w: Identity rejected %s", apperrors.ErrInvalidArgument, operation)
	case codes.AlreadyExists:
		return fmt.Errorf("%w: Identity rejected %s", apperrors.ErrEmailAlreadyTaken, operation)
	case codes.Unauthenticated:
		return fmt.Errorf("%w: Identity rejected %s", apperrors.ErrInvalidCredentials, operation)
	case codes.PermissionDenied:
		return fmt.Errorf("%w: Identity rejected %s", apperrors.ErrForbidden, operation)
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: Identity rejected %s", apperrors.ErrUnprocessable, operation)
	case codes.NotFound:
		return fmt.Errorf("%w: Identity could not find resource for %s", apperrors.ErrNotFound, operation)
	case codes.Unavailable, codes.DeadlineExceeded:
		return fmt.Errorf("%w: Identity %s unavailable", apperrors.ErrServiceUnavailable, operation)
	default:
		return fmt.Errorf("Identity %s: %w", operation, err)
	}
}
