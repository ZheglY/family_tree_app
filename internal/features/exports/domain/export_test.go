package domain

import (
	"errors"
	"strings"
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
		ClientRequestID: uuid.New(), Format: "unsupported",
	}, time.Now().UTC())
	if !errors.Is(err, ErrInvalidExport) {
		t.Fatalf("error = %v, want ErrInvalidExport", err)
	}
}

func TestGEDCOMExportFormatAndFilename(t *testing.T) {
	t.Parallel()
	export, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		ClientRequestID: uuid.New(), Format: FormatGEDCOM,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if filename := ResultFilename(export); !strings.HasSuffix(filename, ".ged") {
		t.Fatalf("filename = %q", filename)
	}
}

func TestNewZIPExportUsesArchiveFilename(t *testing.T) {
	t.Parallel()
	export, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		ClientRequestID: uuid.New(), Format: FormatZIPBackup,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if filename := ResultFilename(export); !strings.HasSuffix(filename, "-backup.zip") {
		t.Fatalf("filename = %q", filename)
	}
}

func TestVisualExportFormatsAndFilenames(t *testing.T) {
	t.Parallel()
	for _, format := range []string{FormatPDF, FormatPNG, FormatSVG} {
		export, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
			ClientRequestID: uuid.New(), Format: format,
		}, time.Now().UTC())
		if err != nil {
			t.Fatalf("New(%s) error = %v", format, err)
		}
		if filename := ResultFilename(export); !strings.HasSuffix(filename, "."+format) {
			t.Fatalf("ResultFilename(%s) = %q", format, filename)
		}
	}
}
