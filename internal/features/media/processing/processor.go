package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	standarddraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
	"github.com/google/uuid"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	thumbnailMaxEdge = 320
	previewMaxEdge   = 1600
	maxImagePixels   = 80_000_000
)

var errInvalidMediaContent = errors.New("invalid media content")

type Repository interface {
	AcquireForProcessing(context.Context, uuid.UUID, uuid.UUID, time.Time) (domain.MediaAsset, error)
	MarkProcessingReady(context.Context, domain.MediaAsset, []domain.MediaVariant, *int, *int, time.Time) error
	RejectProcessing(context.Context, domain.MediaAsset, string, time.Time) error
}

type Processor struct {
	repository  Repository
	objectStore storage.ProcessorObjectStore
	now         func() time.Time
	newID       func() uuid.UUID
}

func NewProcessor(repository Repository, objectStore storage.ProcessorObjectStore) *Processor {
	return &Processor{
		repository:  repository,
		objectStore: objectStore,
		now: func() time.Time {
			return time.Now().UTC()
		},
		newID: uuid.New,
	}
}

func (processor *Processor) Handle(ctx context.Context, job jobs.Job) error {
	var payload mediajob.ProcessPayload
	if job.Kind != mediajob.KindProcess || json.Unmarshal(job.Payload, &payload) != nil ||
		payload.TreeID == uuid.Nil || payload.MediaID == uuid.Nil {
		return fmt.Errorf("%w: malformed processing payload", errInvalidMediaContent)
	}
	asset, err := processor.repository.AcquireForProcessing(
		ctx,
		payload.TreeID,
		payload.MediaID,
		processor.now(),
	)
	if errors.Is(err, domain.ErrMediaNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if asset.Status == domain.StatusReady || asset.Status == domain.StatusRejected ||
		asset.Status == domain.StatusDeleted || asset.DeletedAt != nil {
		return nil
	}
	if asset.Status != domain.StatusProcessing {
		return domain.ErrMediaStateConflict
	}

	body, _, err := processor.objectStore.DownloadObject(ctx, asset.ObjectKey, asset.SizeBytes)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return processor.reject(ctx, asset, "uploaded object was not found")
	}
	if err != nil {
		return processor.retryOrReject(ctx, job, asset, err)
	}
	result, err := inspect(asset, body)
	if err != nil {
		return processor.reject(ctx, asset, err.Error())
	}
	variants := make([]domain.MediaVariant, 0, len(result.variants))
	for _, generated := range result.variants {
		objectKey := variantObjectKey(asset.ObjectKey, generated.kind)
		checksum := checksum(generated.body)
		if _, err := processor.objectStore.PutObject(ctx, storage.PutInput{
			ObjectKey:      objectKey,
			ContentType:    generated.mimeType,
			ChecksumSHA256: checksum,
			Body:           generated.body,
		}); err != nil {
			return processor.retryOrReject(ctx, job, asset, err)
		}
		variants = append(variants, domain.MediaVariant{
			ID:             processor.newID(),
			TreeID:         asset.TreeID,
			MediaID:        asset.ID,
			Kind:           generated.kind,
			ObjectKey:      objectKey,
			MIMEType:       generated.mimeType,
			SizeBytes:      int64(len(generated.body)),
			ChecksumSHA256: checksum,
			Width:          generated.width,
			Height:         generated.height,
			CreatedAt:      processor.now(),
		})
	}
	err = processor.repository.MarkProcessingReady(
		ctx,
		asset,
		variants,
		result.width,
		result.height,
		processor.now(),
	)
	if err != nil {
		return processor.retryOrReject(ctx, job, asset, err)
	}
	return nil
}

func (processor *Processor) reject(
	ctx context.Context,
	asset domain.MediaAsset,
	reason string,
) error {
	if err := processor.repository.RejectProcessing(ctx, asset, reason, processor.now()); err != nil {
		return err
	}
	return nil
}

func (processor *Processor) retryOrReject(
	ctx context.Context,
	job jobs.Job,
	asset domain.MediaAsset,
	cause error,
) error {
	if job.Attempts < job.MaxAttempts {
		return cause
	}
	if err := processor.repository.RejectProcessing(
		ctx,
		asset,
		"processing failed after all retry attempts",
		processor.now(),
	); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

type inspection struct {
	width    *int
	height   *int
	variants []generatedVariant
}

type generatedVariant struct {
	kind     string
	mimeType string
	body     []byte
	width    int
	height   int
}

func inspect(asset domain.MediaAsset, body []byte) (inspection, error) {
	if int64(len(body)) != asset.SizeBytes {
		return inspection{}, fmt.Errorf("%w: actual size does not match upload intent", errInvalidMediaContent)
	}
	if checksum(body) != asset.ChecksumSHA256 {
		return inspection{}, fmt.Errorf("%w: SHA-256 checksum does not match upload intent", errInvalidMediaContent)
	}
	actualMIME := detectMIME(body)
	if actualMIME == "" || actualMIME != asset.MIMEType {
		return inspection{}, fmt.Errorf("%w: magic bytes do not match declared MIME type", errInvalidMediaContent)
	}
	if actualMIME == "application/pdf" {
		return inspection{}, nil
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 ||
		int64(configuration.Width)*int64(configuration.Height) > maxImagePixels {
		return inspection{}, fmt.Errorf("%w: image dimensions or encoding are invalid", errInvalidMediaContent)
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return inspection{}, fmt.Errorf("%w: image could not be decoded", errInvalidMediaContent)
	}
	width, height := configuration.Width, configuration.Height
	variants := make([]generatedVariant, 0, 2)
	for _, variant := range []struct {
		kind    string
		maxEdge int
	}{
		{kind: "thumbnail", maxEdge: thumbnailMaxEdge},
		{kind: "preview", maxEdge: previewMaxEdge},
	} {
		generated, err := resizeJPEG(decoded, variant.kind, variant.maxEdge)
		if err != nil {
			return inspection{}, err
		}
		variants = append(variants, generated)
	}
	return inspection{width: &width, height: &height, variants: variants}, nil
}

func resizeJPEG(source image.Image, kind string, maxEdge int) (generatedVariant, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := width, height
	if width > maxEdge || height > maxEdge {
		if width >= height {
			targetWidth = maxEdge
			targetHeight = max(1, height*maxEdge/width)
		} else {
			targetHeight = maxEdge
			targetWidth = max(1, width*maxEdge/height)
		}
	}
	destination := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	standarddraw.Draw(destination, destination.Bounds(), &image.Uniform{C: color.White}, image.Point{}, standarddraw.Src)
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, standarddraw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, destination, &jpeg.Options{Quality: 85}); err != nil {
		return generatedVariant{}, fmt.Errorf("encode %s: %w", kind, err)
	}
	return generatedVariant{
		kind:     kind,
		mimeType: "image/jpeg",
		body:     output.Bytes(),
		width:    targetWidth,
		height:   targetHeight,
	}, nil
}

func detectMIME(body []byte) string {
	switch {
	case len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff:
		return "image/jpeg"
	case len(body) >= 8 && bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP":
		return "image/webp"
	case len(body) >= 5 && string(body[:5]) == "%PDF-":
		return "application/pdf"
	default:
		return ""
	}
}

func checksum(body []byte) string {
	value := sha256.Sum256(body)
	return hex.EncodeToString(value[:])
}

func variantObjectKey(originalObjectKey string, kind string) string {
	base := strings.TrimSuffix(originalObjectKey, "/original")
	return fmt.Sprintf("%s/variants/%s.jpg", base, kind)
}
