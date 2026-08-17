package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	identityv1 "github.com/ZheglY/family_tree_app/gen/identity/v1"
	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type identityAPIStub struct {
	login func(context.Context, *identityv1.LoginRequest) (*identityv1.SessionResponse, error)
}

func (s identityAPIStub) Register(
	context.Context,
	*identityv1.RegisterRequest,
	...grpc.CallOption,
) (*identityv1.RegisterResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s identityAPIStub) VerifyEmail(
	context.Context,
	*identityv1.VerifyEmailRequest,
	...grpc.CallOption,
) (*identityv1.VerifyEmailResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s identityAPIStub) Login(
	ctx context.Context,
	request *identityv1.LoginRequest,
	_ ...grpc.CallOption,
) (*identityv1.SessionResponse, error) {
	return s.login(ctx, request)
}

func (s identityAPIStub) RefreshSession(
	context.Context,
	*identityv1.RefreshSessionRequest,
	...grpc.CallOption,
) (*identityv1.SessionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s identityAPIStub) Logout(
	context.Context,
	*identityv1.LogoutRequest,
	...grpc.CallOption,
) (*identityv1.LogoutResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s identityAPIStub) LogoutAll(
	context.Context,
	*identityv1.LogoutAllRequest,
	...grpc.CallOption,
) (*identityv1.LogoutAllResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s identityAPIStub) GetAccessTokenPublicKey(
	context.Context,
	*identityv1.GetAccessTokenPublicKeyRequest,
	...grpc.CallOption,
) (*identityv1.GetAccessTokenPublicKeyResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestClientLoginMapsSessionAndPropagatesRequestMetadata(t *testing.T) {
	userID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	client := &Client{
		timeout: time.Second,
		api: identityAPIStub{login: func(
			ctx context.Context,
			request *identityv1.LoginRequest,
		) (*identityv1.SessionResponse, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("Identity call has no deadline")
			}
			outgoingMetadata, ok := metadata.FromOutgoingContext(ctx)
			if !ok || len(outgoingMetadata.Get(requestIDMetadataKey)) != 1 ||
				outgoingMetadata.Get(requestIDMetadataKey)[0] != "request-123" {
				t.Fatalf("outgoing metadata = %#v", outgoingMetadata)
			}
			if request.GetUserAgent() != "browser" || request.GetIpAddress() != "127.0.0.1" {
				t.Fatalf("login request = %#v", request)
			}
			return &identityv1.SessionResponse{
				User: &identityv1.User{
					Id:          userID.String(),
					Email:       "family@example.com",
					DisplayName: "Family Member",
					Status:      identityv1.UserStatus_USER_STATUS_ACTIVE,
				},
				AccessToken:               "access-token",
				RefreshToken:              "refresh-token",
				AccessTokenExpiresAtUnix:  now.Add(15 * time.Minute).Unix(),
				RefreshTokenExpiresAtUnix: now.Add(24 * time.Hour).Unix(),
			}, nil
		}},
	}
	ctx := requestid.NewContext(context.Background(), "request-123")

	session, err := client.Login(ctx, service.LoginCommand{
		Email:     "family@example.com",
		Password:  "correct horse battery staple",
		UserAgent: "browser",
		IPAddress: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.User.ID != userID || session.RefreshToken != "refresh-token" {
		t.Fatalf("session = %#v", session)
	}
}

func TestClientMapsUnavailableIdentity(t *testing.T) {
	client := &Client{
		timeout: time.Second,
		api: identityAPIStub{login: func(
			context.Context,
			*identityv1.LoginRequest,
		) (*identityv1.SessionResponse, error) {
			return nil, status.Error(codes.Unavailable, "connection refused")
		}},
	}

	_, err := client.Login(context.Background(), service.LoginCommand{})
	if !errors.Is(err, apperrors.ErrServiceUnavailable) {
		t.Fatalf("Login() error = %v, want service unavailable", err)
	}
}
