package manifest

import (
	"time"

	"github.com/google/uuid"
)

type Manifest struct {
	Schema               Schema                  `json:"schema"`
	Export               ExportMetadata          `json:"export"`
	Tree                 Tree                    `json:"tree"`
	Members              []TreeMember            `json:"members"`
	Persons              []Person                `json:"persons"`
	PersonNames          []PersonName            `json:"person_names"`
	ParentChildRelations []ParentChildRelation   `json:"parent_child_relations"`
	Unions               []FamilyUnion           `json:"unions"`
	UnionMembers         []UnionMember           `json:"union_members"`
	MediaAssets          []MediaAsset            `json:"media_assets"`
	MediaVariants        []MediaVariant          `json:"media_variants"`
	PersonMedia          []PersonMediaAttachment `json:"person_media"`
}

type Schema struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type ExportMetadata struct {
	ID          uuid.UUID `json:"id"`
	Format      string    `json:"format"`
	RequestedBy uuid.UUID `json:"requested_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type Tree struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	OwnerUserID  uuid.UUID  `json:"owner_user_id"`
	RootPersonID *uuid.UUID `json:"root_person_id,omitempty"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	Privacy      string     `json:"privacy"`
	Locale       string     `json:"locale"`
	Timezone     string     `json:"timezone"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	Version      int        `json:"version"`
}

type TreeMember struct {
	UserID     uuid.UUID  `json:"user_id"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	InvitedBy  *uuid.UUID `json:"invited_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

type Person struct {
	ID             uuid.UUID  `json:"id"`
	Sex            string     `json:"sex"`
	LifeStatus     string     `json:"life_status"`
	Biography      string     `json:"biography"`
	Notes          string     `json:"notes"`
	PrimaryMediaID *uuid.UUID `json:"primary_media_id,omitempty"`
	PrivacyLevel   string     `json:"privacy_level"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	UpdatedBy      uuid.UUID  `json:"updated_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Version        int        `json:"version"`
}

type PersonName struct {
	ID           uuid.UUID `json:"id"`
	PersonID     uuid.UUID `json:"person_id"`
	Type         string    `json:"type"`
	GivenName    string    `json:"given_name"`
	Patronymic   string    `json:"patronymic"`
	FamilyName   string    `json:"family_name"`
	Prefix       string    `json:"prefix"`
	Suffix       string    `json:"suffix"`
	FullText     string    `json:"full_text"`
	IsPreferred  bool      `json:"is_preferred"`
	LanguageCode string    `json:"language_code"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ParentChildRelation struct {
	ID             uuid.UUID  `json:"id"`
	ParentPersonID uuid.UUID  `json:"parent_person_id"`
	ChildPersonID  uuid.UUID  `json:"child_person_id"`
	RelationType   string     `json:"relation_type"`
	Confidence     string     `json:"confidence"`
	Note           string     `json:"note"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	Version        int        `json:"version"`
}

type FamilyUnion struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	EndReason string     `json:"end_reason"`
	Note      string     `json:"note"`
	CreatedBy uuid.UUID  `json:"created_by"`
	UpdatedBy uuid.UUID  `json:"updated_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Version   int        `json:"version"`
}

type UnionMember struct {
	UnionID   uuid.UUID `json:"union_id"`
	PersonID  uuid.UUID `json:"person_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type MediaAsset struct {
	ID               uuid.UUID  `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	OriginalFilename string     `json:"original_filename"`
	MIMEType         string     `json:"mime_type"`
	SizeBytes        int64      `json:"size_bytes"`
	ChecksumSHA256   string     `json:"checksum_sha256"`
	Width            *int       `json:"width,omitempty"`
	Height           *int       `json:"height,omitempty"`
	Caption          string     `json:"caption"`
	Description      string     `json:"description"`
	UploadedBy       uuid.UUID  `json:"uploaded_by"`
	UploadedAt       *time.Time `json:"uploaded_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	ProcessedAt      *time.Time `json:"processed_at,omitempty"`
	Version          int        `json:"version"`
}

type MediaVariant struct {
	ID             uuid.UUID `json:"id"`
	MediaID        uuid.UUID `json:"media_id"`
	Kind           string    `json:"kind"`
	MIMEType       string    `json:"mime_type"`
	SizeBytes      int64     `json:"size_bytes"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	CreatedAt      time.Time `json:"created_at"`
}

type PersonMediaAttachment struct {
	PersonID  uuid.UUID `json:"person_id"`
	MediaID   uuid.UUID `json:"media_id"`
	Role      string    `json:"role"`
	SortOrder int       `json:"sort_order"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
