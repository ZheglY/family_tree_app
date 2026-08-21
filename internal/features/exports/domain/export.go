package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	FormatJSONBackup = "json_backup"
	FormatZIPBackup  = "zip_backup"
	FormatPDF        = "pdf"
	FormatPNG        = "png"
	FormatSVG        = "svg"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusExpired   = "expired"

	ManifestSchemaName    = "family_tree_backup"
	ManifestSchemaVersion = 1
)

var (
	ErrInvalidExport           = errors.New("invalid export")
	ErrExportNotFound          = errors.New("export was not found")
	ErrExportAccessDenied      = errors.New("export access denied")
	ErrExportRequestConflict   = errors.New("export request conflict")
	ErrExportStateConflict     = errors.New("export state conflict")
	ErrExportResultUnavailable = errors.New("export result is unavailable")
	ErrExportTreeUnavailable   = errors.New("export tree is unavailable")
	ErrExportArchiveTooLarge   = errors.New("export archive is too large")
	ErrExportVisualTooLarge    = errors.New("visual export is too large")
	ErrExportSourceInvalid     = errors.New("export source file is invalid")
)

type Export struct {
	ID                   uuid.UUID
	TreeID               uuid.UUID
	ClientRequestID      uuid.UUID
	RequestedBy          uuid.UUID
	Format               string
	SchemaVersion        int
	Parameters           json.RawMessage
	Status               string
	Progress             int
	ResultObjectKey      string
	ResultMIMEType       string
	ResultSizeBytes      int64
	ResultChecksumSHA256 string
	ErrorCode            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	StartedAt            *time.Time
	FinishedAt           *time.Time
	ExpiresAt            *time.Time
}

type CreateValues struct {
	ClientRequestID uuid.UUID
	Format          string
}

type CleanupCandidate struct {
	TreeID          uuid.UUID
	ExportID        uuid.UUID
	ResultObjectKey string
}

func New(
	id uuid.UUID,
	treeID uuid.UUID,
	requestedBy uuid.UUID,
	values CreateValues,
	now time.Time,
) (Export, error) {
	format := strings.TrimSpace(values.Format)
	if id == uuid.Nil || treeID == uuid.Nil || requestedBy == uuid.Nil ||
		values.ClientRequestID == uuid.Nil || !supportedFormat(format) || now.IsZero() {
		return Export{}, ErrInvalidExport
	}
	parameters := json.RawMessage(`{}`)
	return Export{
		ID:              id,
		TreeID:          treeID,
		ClientRequestID: values.ClientRequestID,
		RequestedBy:     requestedBy,
		Format:          format,
		SchemaVersion:   ManifestSchemaVersion,
		Parameters:      parameters,
		Status:          StatusQueued,
		Progress:        0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func ResultFilename(value Export) string {
	switch value.Format {
	case FormatZIPBackup:
		return fmt.Sprintf("family-tree-%s-backup.zip", value.TreeID)
	case FormatPDF, FormatPNG, FormatSVG:
		return fmt.Sprintf("family-tree-%s.%s", value.TreeID, value.Format)
	default:
		return fmt.Sprintf("family-tree-%s-export.json", value.TreeID)
	}
}

func CanDownload(value Export, now time.Time) bool {
	return value.Status == StatusCompleted && value.ExpiresAt != nil && now.Before(*value.ExpiresAt) &&
		value.ResultObjectKey != ""
}

func supportedFormat(format string) bool {
	return format == FormatJSONBackup || format == FormatZIPBackup ||
		format == FormatPDF || format == FormatPNG || format == FormatSVG
}
