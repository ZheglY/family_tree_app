package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewExportAndDownloadAvailability(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	export, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		ClientRequestID: uuid.New(), Format: FormatJSONBackup,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if export.Status != StatusQueued || export.SchemaVersion != ManifestSchemaVersion ||
		string(export.Parameters) != "{}" {
		t.Fatalf("export = %#v", export)
	}
	expiresAt := now.Add(time.Hour)
	export.Status = StatusCompleted
	export.ResultObjectKey = "trees/tree/exports/export/manifest.json"
	export.ExpiresAt = &expiresAt
	if !CanDownload(export, now) || CanDownload(export, expiresAt) {
		t.Fatalf("unexpected download availability")
	}
}

func TestNewExportRejectsUnsupportedFormat(t *testing.T) {
	t.Parallel()
	_, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		ClientRequestID: uuid.New(), Format: "gedcom",
	}, time.Now().UTC())
	if !errors.Is(err, ErrInvalidExport) {
		t.Fatalf("error = %v, want ErrInvalidExport", err)
	}
}
