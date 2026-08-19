package access

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	algorithm = "EdDSA"
	tokenType = "at+jwt"
)

var ErrInvalidToken = errors.New("invalid access token")

type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type claims struct {
	SessionID string `json:"sid"`
	TokenUse  string `json:"token_use"`
	jwt.RegisteredClaims
}

type Verifier struct {
	publicKey ed25519.PublicKey
	keyID     string
	issuer    string
	audience  string
	now       func() time.Time
}

func NewVerifier(key model.AccessTokenKey) (*Verifier, error) {
	if key.Algorithm != algorithm || strings.TrimSpace(key.KeyID) == "" ||
		strings.TrimSpace(key.Issuer) == "" || strings.TrimSpace(key.Audience) == "" {
		return nil, fmt.Errorf("access token key metadata is invalid")
	}

	publicKey, err := decodePublicKey(key.PublicKeyBase64)
	if err != nil {
		return nil, err
	}

	return &Verifier{
		publicKey: publicKey,
		keyID:     key.KeyID,
		issuer:    key.Issuer,
		audience:  key.Audience,
		now:       time.Now,
	}, nil
}

func (v *Verifier) Verify(rawToken string) (Principal, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Principal{}, ErrInvalidToken
	}

	tokenClaims := &claims{}
	parsed, err := jwt.ParseWithClaims(
		rawToken,
		tokenClaims,
		func(token *jwt.Token) (any, error) {
			if token.Header["kid"] != v.keyID || token.Header["typ"] != tokenType {
				return nil, ErrInvalidToken
			}
			return v.publicKey, nil
		},
		jwt.WithValidMethods([]string{algorithm}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil || !parsed.Valid || tokenClaims.TokenUse != "access" ||
		tokenClaims.IssuedAt == nil || tokenClaims.NotBefore == nil || tokenClaims.ExpiresAt == nil {
		return Principal{}, fmt.Errorf("%w: token validation failed", ErrInvalidToken)
	}

	userID, err := uuid.Parse(tokenClaims.Subject)
	if err != nil || userID == uuid.Nil {
		return Principal{}, fmt.Errorf("%w: subject is invalid", ErrInvalidToken)
	}
	sessionID, err := uuid.Parse(tokenClaims.SessionID)
	if err != nil || sessionID == uuid.Nil {
		return Principal{}, fmt.Errorf("%w: session is invalid", ErrInvalidToken)
	}

	return Principal{UserID: userID, SessionID: sessionID}, nil
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	value = strings.TrimSpace(value)
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("access token public key is invalid")
	}

	return ed25519.PublicKey(decoded), nil
}
