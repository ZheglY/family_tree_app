package identity

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	identityv1 "github.com/ZheglY/family_tree_app/gen/identity/v1"
	"github.com/ZheglY/family_tree_app/internal/core/requestid"
	"github.com/ZheglY/family_tree_app/internal/features/auth/service"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type identityTestServer struct {
	identityv1.UnimplementedIdentityServiceServer
}

func (s identityTestServer) Login(
	ctx context.Context,
	request *identityv1.LoginRequest,
) (*identityv1.SessionResponse, error) {
	incomingMetadata, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(incomingMetadata.Get(requestIDMetadataKey)) != 1 ||
		incomingMetadata.Get(requestIDMetadataKey)[0] != "integration-request" {
		return nil, fmt.Errorf("incoming gRPC metadata = %#v", incomingMetadata)
	}
	if request.GetEmail() != "family@example.com" {
		return nil, fmt.Errorf("login request = %#v", request)
	}
	now := time.Now().UTC()
	return &identityv1.SessionResponse{
		User: &identityv1.User{
			Id:          uuid.NewString(),
			Email:       request.GetEmail(),
			DisplayName: "Family Member",
			Status:      identityv1.UserStatus_USER_STATUS_ACTIVE,
		},
		AccessToken:               "access-token",
		RefreshToken:              "refresh-token",
		AccessTokenExpiresAtUnix:  now.Add(15 * time.Minute).Unix(),
		RefreshTokenExpiresAtUnix: now.Add(24 * time.Hour).Unix(),
	}, nil
}

func TestClientLoginOverGRPCIntegration(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	identityv1.RegisterIdentityServiceServer(grpcServer, identityTestServer{})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create gRPC client connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := &Client{
		connection: connection,
		api:        identityv1.NewIdentityServiceClient(connection),
		timeout:    time.Second,
	}
	ctx := requestid.NewContext(context.Background(), "integration-request")

	session, err := client.Login(ctx, service.LoginCommand{
		Email:    "family@example.com",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.AccessToken != "access-token" || session.RefreshToken != "refresh-token" {
		t.Fatalf("session = %#v", session)
	}
}
