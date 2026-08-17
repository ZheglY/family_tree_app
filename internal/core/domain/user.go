package domain

import (
	"time"

	"github.com/google/uuid"
)

/*
Status (UserStatus) — нельзя считать, что любой созданный аккаунт уже имеет
полный доступ. После регистрации он будет pending_verification,
после подтверждения — active.
*/
type User struct {
	ID              uuid.UUID
	Email           string
	NormalizedEmail string // email для поиска и уникальности
	DisplayName     string
	Status          UserStatus
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	Version         int
}

type UserStatus string

const (
	UserStatusPendingVerification UserStatus = "pending_verification"
	UserStatusActive              UserStatus = "active"
	UserStatusBlocked             UserStatus = "blocked"
)

func NewUser(
	id uuid.UUID,
	email string,
	normalizedEmail string,
	displayName string,
	status UserStatus,
	now time.Time,
) *User {
	return &User{
		ID:              id,
		Email:           email,
		NormalizedEmail: normalizedEmail,
		DisplayName:     displayName,
		Status:          status,
		CreatedAt:       now,
		UpdatedAt:       now,
		Version:         1,
	}
}
