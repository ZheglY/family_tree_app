package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
	"github.com/google/uuid"
)

type processorRepositoryStub struct {
	asset    domain.MediaAsset
	ready    bool
	rejected string
	variants []domain.MediaVariant
}

func (repository *processorRepositoryStub) AcquireForProcessing(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) (domain.MediaAsset, error) {
	return repository.asset, nil
}

func (repository *processorRepositoryStub) MarkProcessingReady(
	_ context.Context,
	_ domain.MediaAsset,
	variants []domain.MediaVariant,
	_ *int,
	_ *int,
	_ time.Time,
) error {
	repository.ready = true
	repository.variants = variants
	return nil
}

func (repository *processorRepositoryStub) RejectProcessing(
	_ context.Context,
	_ domain.MediaAsset,
	reason string,
	_ time.Time,
) error {
	repository.rejected = reason
	return nil
}

type processorObjectStoreStub struct {
	body []byte
	puts []storage.PutInput
}

func (objectStore *processorObjectStoreStub) DownloadObject(
	context.Context,
	string,
	int64,
) ([]byte, storage.ObjectInfo, error) {
	return objectStore.body, storage.ObjectInfo{SizeBytes: int64(len(objectStore.body))}, nil
}

func (objectStore *processorObjectStoreStub) PutObject(
	_ context.Context,
	input storage.PutInput,
) (storage.ObjectInfo, error) {
	objectStore.puts = append(objectStore.puts, input)
	return storage.ObjectInfo{SizeBytes: int64(len(input.Body))}, nil
}

func (objectStore *processorObjectStoreStub) DeleteObject(context.Context, string) error {
	return nil
}

func TestProcessorValidatesAndBuildsImageVariants(t *testing.T) {
	t.Parallel()
	body := testPNG(t)
	asset := processingAsset(body)
	repository := &processorRepositoryStub{asset: asset}
	objectStore := &processorObjectStoreStub{body: body}
	processor := NewProcessor(repository, objectStore)
	payload, err := json.Marshal(mediajob.ProcessPayload{TreeID: asset.TreeID, MediaID: asset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), jobs.Job{
		Kind: mediajob.KindProcess, Payload: payload,
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !repository.ready || repository.rejected != "" || len(repository.variants) != 2 ||
		len(objectStore.puts) != 2 {
		t.Fatalf(
			"processor result: ready=%t rejected=%q variants=%d puts=%d",
			repository.ready,
			repository.rejected,
			len(repository.variants),
			len(objectStore.puts),
		)
	}
	for _, variant := range repository.variants {
		if variant.MIMEType != "image/jpeg" || variant.Width != 2 || variant.Height != 1 ||
			variant.ChecksumSHA256 == "" || len(variant.ObjectKey) == 0 {
			t.Fatalf("variant = %#v", variant)
		}
	}
}

func TestProcessorRejectsActualChecksumMismatch(t *testing.T) {
	t.Parallel()
	body := testPNG(t)
	asset := processingAsset(body)
	asset.ChecksumSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repository := &processorRepositoryStub{asset: asset}
	processor := NewProcessor(repository, &processorObjectStoreStub{body: body})
	payload, _ := json.Marshal(mediajob.ProcessPayload{TreeID: asset.TreeID, MediaID: asset.ID})
	if err := processor.Handle(context.Background(), jobs.Job{
		Kind: mediajob.KindProcess, Payload: payload,
	}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if repository.ready || repository.rejected == "" {
		t.Fatalf("processor result: ready=%t rejected=%q", repository.ready, repository.rejected)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 1))
	value.Set(0, 0, color.RGBA{R: 120, G: 40, B: 20, A: 255})
	value.Set(1, 0, color.RGBA{R: 20, G: 80, B: 160, A: 180})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, value); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func processingAsset(body []byte) domain.MediaAsset {
	return domain.MediaAsset{
		ID:             uuid.New(),
		TreeID:         uuid.New(),
		Status:         domain.StatusProcessing,
		ObjectKey:      "trees/tree/media/media/original",
		MIMEType:       "image/png",
		SizeBytes:      int64(len(body)),
		ChecksumSHA256: checksum(body),
	}
}
