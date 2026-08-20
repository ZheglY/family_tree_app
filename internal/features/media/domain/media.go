package domain

import (
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	KindPhoto    = "photo"
	KindDocument = "document"
	KindOther    = "other"

	StatusPending    = "pending"
	StatusUploaded   = "uploaded"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusRejected   = "rejected"
	StatusDeleted    = "deleted"

	RoleProfile  = "profile"
	RoleGallery  = "gallery"
	RoleDocument = "document"
	RoleOther    = "other"

	maxFilenameRunes    = 255
	maxCaptionRunes     = 500
	maxDescriptionRunes = 5000
	maxObjectKeyBytes   = 1024
	maxSortOrder        = 1000000
)

var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var supportedMIMEExtensions = map[string]map[string]struct{}{
	"image/jpeg":      {".jpg": {}, ".jpeg": {}},
	"image/png":       {".png": {}},
	"image/webp":      {".webp": {}},
	"application/pdf": {".pdf": {}},
}

type MediaAsset struct {
	ID               uuid.UUID
	TreeID           uuid.UUID
	ClientRequestID  uuid.UUID
	Kind             string
	Status           string
	ObjectKey        string
	OriginalFilename string
	MIMEType         string
	SizeBytes        int64
	ChecksumSHA256   string
	ETag             string
	Width            *int
	Height           *int
	Caption          string
	Description      string
	UploadedBy       uuid.UUID
	UploadedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
	Version          int
}

type PersonMedia struct {
	TreeID    uuid.UUID
	PersonID  uuid.UUID
	MediaID   uuid.UUID
	Role      string
	SortOrder int
	CreatedBy uuid.UUID
	CreatedAt time.Time
}

type CreateValues struct {
	ClientRequestID  uuid.UUID
	Kind             string
	OriginalFilename string
	MIMEType         string
	SizeBytes        int64
	ChecksumSHA256   string
}

type UpdateValues struct {
	Caption     *string
	Description *string
}

type UploadedObject struct {
	MIMEType       string
	SizeBytes      int64
	ChecksumSHA256 string
	ETag           string
}

type AttachmentValues struct {
	PersonID  uuid.UUID
	Role      string
	SortOrder int
}

func New(
	id uuid.UUID,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
	objectKey string,
	values CreateValues,
	maxUploadBytes int64,
	now time.Time,
) (MediaAsset, error) {
	if id == uuid.Nil || treeID == uuid.Nil || actorUserID == uuid.Nil ||
		values.ClientRequestID == uuid.Nil || now.IsZero() || maxUploadBytes <= 0 {
		return MediaAsset{}, ErrInvalidMedia
	}
	kind := strings.TrimSpace(values.Kind)
	filename, err := normalizeFilename(values.OriginalFilename)
	if err != nil {
		return MediaAsset{}, err
	}
	mimeType, err := normalizeMIME(values.MIMEType)
	if err != nil || !IsKind(kind) || !kindAcceptsMIME(kind, mimeType) ||
		!filenameMatchesMIME(filename, mimeType) {
		return MediaAsset{}, ErrInvalidMedia
	}
	checksum := strings.ToLower(strings.TrimSpace(values.ChecksumSHA256))
	objectKey = strings.TrimSpace(objectKey)
	if values.SizeBytes <= 0 || values.SizeBytes > maxUploadBytes ||
		!checksumPattern.MatchString(checksum) || objectKey == "" ||
		len(objectKey) > maxObjectKeyBytes {
		return MediaAsset{}, ErrInvalidMedia
	}
	return MediaAsset{
		ID:               id,
		TreeID:           treeID,
		ClientRequestID:  values.ClientRequestID,
		Kind:             kind,
		Status:           StatusPending,
		ObjectKey:        objectKey,
		OriginalFilename: filename,
		MIMEType:         mimeType,
		SizeBytes:        values.SizeBytes,
		ChecksumSHA256:   checksum,
		UploadedBy:       actorUserID,
		CreatedAt:        now,
		UpdatedAt:        now,
		Version:          1,
	}, nil
}

func ApplyUploadCompletion(
	asset MediaAsset,
	object UploadedObject,
	now time.Time,
) (MediaAsset, error) {
	if asset.DeletedAt != nil {
		return MediaAsset{}, ErrMediaNotFound
	}
	if asset.Status != StatusPending {
		return MediaAsset{}, ErrMediaStateConflict
	}
	mimeType, err := normalizeMIME(object.MIMEType)
	if err != nil || object.SizeBytes != asset.SizeBytes || mimeType != asset.MIMEType ||
		strings.ToLower(strings.TrimSpace(object.ChecksumSHA256)) != asset.ChecksumSHA256 {
		return MediaAsset{}, ErrUploadedObjectMismatch
	}
	if now.IsZero() {
		return MediaAsset{}, ErrInvalidMedia
	}
	asset.Status = StatusUploaded
	asset.ETag = strings.TrimSpace(object.ETag)
	asset.UploadedAt = &now
	asset.UpdatedAt = now
	asset.Version++
	return asset, nil
}

func ApplyUpdate(
	asset MediaAsset,
	expectedVersion int,
	values UpdateValues,
	now time.Time,
) (MediaAsset, error) {
	if expectedVersion <= 0 || asset.Version != expectedVersion {
		return MediaAsset{}, ErrMediaVersionConflict
	}
	if asset.DeletedAt != nil || asset.Status == StatusDeleted {
		return MediaAsset{}, ErrMediaNotFound
	}
	if values.Caption == nil && values.Description == nil {
		return MediaAsset{}, fmt.Errorf("%w: no fields to update", ErrInvalidMedia)
	}
	caption := asset.Caption
	description := asset.Description
	if values.Caption != nil {
		caption = strings.TrimSpace(*values.Caption)
	}
	if values.Description != nil {
		description = strings.TrimSpace(*values.Description)
	}
	if now.IsZero() || utf8.RuneCountInString(caption) > maxCaptionRunes ||
		utf8.RuneCountInString(description) > maxDescriptionRunes {
		return MediaAsset{}, ErrInvalidMedia
	}
	asset.Caption = caption
	asset.Description = description
	asset.UpdatedAt = now
	asset.Version++
	return asset, nil
}

func NewAttachment(
	treeID uuid.UUID,
	mediaID uuid.UUID,
	actorUserID uuid.UUID,
	values AttachmentValues,
	now time.Time,
) (PersonMedia, error) {
	role := strings.TrimSpace(values.Role)
	if role == "" {
		role = RoleOther
	}
	if treeID == uuid.Nil || mediaID == uuid.Nil || actorUserID == uuid.Nil ||
		values.PersonID == uuid.Nil || now.IsZero() || !IsRole(role) ||
		values.SortOrder < 0 || values.SortOrder > maxSortOrder {
		return PersonMedia{}, ErrInvalidMedia
	}
	return PersonMedia{
		TreeID:    treeID,
		PersonID:  values.PersonID,
		MediaID:   mediaID,
		Role:      role,
		SortOrder: values.SortOrder,
		CreatedBy: actorUserID,
		CreatedAt: now,
	}, nil
}

func IsKind(value string) bool {
	return value == KindPhoto || value == KindDocument || value == KindOther
}

func IsRole(value string) bool {
	return value == RoleProfile || value == RoleGallery ||
		value == RoleDocument || value == RoleOther
}

func CanAttach(status string) bool {
	return status == StatusUploaded || status == StatusProcessing || status == StatusReady
}

func CanDownload(status string) bool {
	return CanAttach(status)
}

func SameUploadRequest(asset MediaAsset, values CreateValues) bool {
	filename, filenameErr := normalizeFilename(values.OriginalFilename)
	mimeType, mimeErr := normalizeMIME(values.MIMEType)
	return filenameErr == nil && mimeErr == nil &&
		asset.ClientRequestID == values.ClientRequestID &&
		asset.Kind == strings.TrimSpace(values.Kind) &&
		asset.OriginalFilename == filename && asset.MIMEType == mimeType &&
		asset.SizeBytes == values.SizeBytes &&
		asset.ChecksumSHA256 == strings.ToLower(strings.TrimSpace(values.ChecksumSHA256))
}

func normalizeFilename(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = path.Base(value)
	if value == "." || value == "/" || value == "" ||
		utf8.RuneCountInString(value) > maxFilenameRunes {
		return "", ErrInvalidMedia
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidMedia
		}
	}
	return value, nil
}

func normalizeMIME(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return "", ErrInvalidMedia
	}
	mediaType = strings.ToLower(mediaType)
	if _, supported := supportedMIMEExtensions[mediaType]; !supported {
		return "", ErrInvalidMedia
	}
	return mediaType, nil
}

func kindAcceptsMIME(kind string, mimeType string) bool {
	if kind == KindPhoto {
		return strings.HasPrefix(mimeType, "image/")
	}
	return true
}

func filenameMatchesMIME(filename string, mimeType string) bool {
	extension := strings.ToLower(path.Ext(filename))
	_, supported := supportedMIMEExtensions[mimeType][extension]
	return supported
}
