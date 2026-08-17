package service

import (
	"context"

	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/google/uuid"
)

type RegisterCommand struct {
	Email       string
	Password    string
	DisplayName string
}

type RegisterResult struct {
	User                 model.User
	VerificationRequired bool
}

type LoginCommand struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
}

type RefreshCommand struct {
	RefreshToken string
	UserAgent    string
	IPAddress    string
}

type IdentityGateway interface {
	Register(context.Context, RegisterCommand) (RegisterResult, error)
	VerifyEmail(context.Context, string) (model.User, error)
	Login(context.Context, LoginCommand) (model.Session, error)
	Refresh(context.Context, RefreshCommand) (model.Session, error)
	Logout(context.Context, string) error
	LogoutAll(context.Context, uuid.UUID) (int64, error)
	GetAccessTokenKey(context.Context) (model.AccessTokenKey, error)
}

type AuthService struct {
	identity IdentityGateway
}

func NewAuthService(identity IdentityGateway) *AuthService {
	return &AuthService{identity: identity}
}

func (s *AuthService) Register(
	ctx context.Context,
	command RegisterCommand,
) (RegisterResult, error) {
	return s.identity.Register(ctx, command)
}

func (s *AuthService) VerifyEmail(
	ctx context.Context,
	token string,
) (model.User, error) {
	return s.identity.VerifyEmail(ctx, token)
}

func (s *AuthService) Login(
	ctx context.Context,
	command LoginCommand,
) (model.Session, error) {
	return s.identity.Login(ctx, command)
}

func (s *AuthService) Refresh(
	ctx context.Context,
	command RefreshCommand,
) (model.Session, error) {
	return s.identity.Refresh(ctx, command)
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	return s.identity.Logout(ctx, refreshToken)
}

func (s *AuthService) LogoutAll(
	ctx context.Context,
	userID uuid.UUID,
) (int64, error) {
	return s.identity.LogoutAll(ctx, userID)
}

func (s *AuthService) GetAccessTokenKey(
	ctx context.Context,
) (model.AccessTokenKey, error) {
	return s.identity.GetAccessTokenKey(ctx)
}
