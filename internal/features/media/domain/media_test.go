package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewMediaNormalizesUploadIntent(t *testing.T) {
	t.Parallel()
	asset, err := New(
		uuid.New(), uuid.New(), uuid.New(), "trees/a/media/b/original",
		CreateValues{
			ClientRequestID:  uuid.New(),
			Kind:             KindPhoto,
			OriginalFilename: `C:\archive\Portrait.JPG`,
			MIMEType:         "image/jpeg; charset=binary",
			SizeBytes:        1024,
			ChecksumSHA256:   strings.Repeat("A", 64),
		},
		25*1024*1024,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if asset.OriginalFilename != "Portrait.JPG" || asset.MIMEType != "image/jpeg" ||
		asset.ChecksumSHA256 != strings.Repeat("a", 64) || asset.Status != StatusPending {
		t.Fatalf("asset = %#v", asset)
	}
}

func TestNewMediaRejectsMIMEExtensionMismatchAndOversize(t *testing.T) {
	t.Parallel()
	values := CreateValues{
		ClientRequestID: uuid.New(), Kind: KindPhoto, OriginalFilename: "portrait.pdf",
		MIMEType: "image/jpeg", SizeBytes: 1024, ChecksumSHA256: strings.Repeat("a", 64),
	}
	if _, err := New(uuid.New(), uuid.New(), uuid.New(), "key", values, 2048, time.Now().UTC()); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("extension mismatch error = %v", err)
	}
	values.OriginalFilename = "portrait.jpg"
	values.SizeBytes = 4096
	if _, err := New(uuid.New(), uuid.New(), uuid.New(), "key", values, 2048, time.Now().UTC()); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestApplyUploadCompletionVerifiesHeadMetadata(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	checksum := strings.Repeat("b", 64)
	asset, err := New(
		uuid.New(), uuid.New(), uuid.New(), "key",
		CreateValues{
			ClientRequestID: uuid.New(), Kind: KindDocument, OriginalFilename: "record.pdf",
			MIMEType: "application/pdf", SizeBytes: 512, ChecksumSHA256: checksum,
		},
		1024,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyUploadCompletion(asset, UploadedObject{
		MIMEType: "application/pdf", SizeBytes: 511, ChecksumSHA256: checksum,
	}, now); !errors.Is(err, ErrUploadedObjectMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	completed, err := ApplyUploadCompletion(asset, UploadedObject{
		MIMEType: "application/pdf", SizeBytes: 512, ChecksumSHA256: checksum, ETag: "etag",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyUploadCompletion() error = %v", err)
	}
	if completed.Status != StatusUploaded || completed.Version != 2 || completed.UploadedAt == nil {
		t.Fatalf("completed = %#v", completed)
	}
}
