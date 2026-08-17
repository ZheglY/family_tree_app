package model

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
}

type Session struct {
	User                  User
	AccessToken           string
	RefreshToken          string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

type AccessTokenKey struct {
	KeyID           string
	Algorithm       string
	PublicKeyBase64 string
	Issuer          string
	Audience        string
}
