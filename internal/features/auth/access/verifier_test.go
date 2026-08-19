package access

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/auth/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestVerifierAcceptsExpectedAccessToken(t *testing.T) {
	verifier, privateKey, now := testVerifier(t)
	userID := uuid.New()
	sessionID := uuid.New()
	rawToken := signToken(t, privateKey, now, userID, sessionID, jwt.SigningMethodEdDSA)

	principal, err := verifier.Verify(rawToken)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.UserID != userID || principal.SessionID != sessionID {
		t.Fatalf("principal = %#v", principal)
	}
}

func TestVerifierRejectsUnexpectedAlgorithm(t *testing.T) {
	verifier, _, now := testVerifier(t)
	userID := uuid.New()
	sessionID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, testClaims(now, userID, sessionID))
	token.Header["kid"] = "test-key"
	token.Header["typ"] = tokenType
	rawToken, err := token.SignedString([]byte("attacker-controlled-secret"))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	if _, err := verifier.Verify(rawToken); err == nil {
		t.Fatal("Verify() error = nil, want algorithm rejection")
	}
}

func TestVerifierRejectsExpiredToken(t *testing.T) {
	verifier, privateKey, now := testVerifier(t)
	verifier.now = func() time.Time { return now.Add(time.Hour) }
	rawToken := signToken(t, privateKey, now, uuid.New(), uuid.New(), jwt.SigningMethodEdDSA)

	if _, err := verifier.Verify(rawToken); err == nil {
		t.Fatal("Verify() error = nil, want expiry error")
	}
}

func testVerifier(t *testing.T) (*Verifier, ed25519.PrivateKey, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	verifier, err := NewVerifier(model.AccessTokenKey{
		KeyID:           "test-key",
		Algorithm:       algorithm,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		Issuer:          "test-identity",
		Audience:        "test-family-api",
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return now.Add(time.Minute) }
	return verifier, privateKey, now
}

func signToken(
	t *testing.T,
	privateKey ed25519.PrivateKey,
	now time.Time,
	userID uuid.UUID,
	sessionID uuid.UUID,
	method jwt.SigningMethod,
) string {
	t.Helper()
	token := jwt.NewWithClaims(method, testClaims(now, userID, sessionID))
	token.Header["kid"] = "test-key"
	token.Header["typ"] = tokenType
	rawToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return rawToken
}

func testClaims(now time.Time, userID, sessionID uuid.UUID) claims {
	return claims{
		SessionID: sessionID.String(),
		TokenUse:  "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-identity",
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{"test-family-api"},
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
}
