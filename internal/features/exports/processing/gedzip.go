package processing

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/gedcom"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

const gedzipMIMEType = "application/vnd.familysearch.gedcom+zip"

type gedzipSource struct {
	Source   manifest.SourceFile
	Path     string
	MIMEType string
}

func buildGEDZIP(
	ctx context.Context,
	export domain.Export,
	snapshot manifest.Snapshot,
	objectStore storage.ProcessorObjectStore,
	maxArchiveBytes int64,
) ([]byte, error) {
	sources, mediaFiles, err := prepareGEDZIPSources(snapshot)
	if err != nil {
		return nil, err
	}
	gedcomBody, err := gedcom.RenderWithMedia(snapshot.Manifest, mediaFiles)
	if err != nil {
		return nil, err
	}
	expectedSize := int64(len(gedcomBody))
	if expectedSize > maxArchiveBytes {
		return nil, domain.ErrExportArchiveTooLarge
	}
	for _, source := range sources {
		if source.Source.ObjectKey == "" || source.Source.SizeBytes <= 0 ||
			!validGEDZIPSHA256(source.Source.ChecksumSHA256) {
			return nil, fmt.Errorf("%w: malformed GEDZIP source metadata", domain.ErrExportSourceInvalid)
		}
		if source.Source.SizeBytes > maxArchiveBytes-expectedSize {
			return nil, domain.ErrExportArchiveTooLarge
		}
		expectedSize += source.Source.SizeBytes
	}

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	if err := writeZIPEntry(writer, "gedcom.ged", gedcomBody, zip.Deflate, export.CreatedAt); err != nil {
		return nil, err
	}
	for _, source := range sources {
		body, _, err := objectStore.DownloadObject(ctx, source.Source.ObjectKey, source.Source.SizeBytes)
		if err != nil {
			return nil, fmt.Errorf("download GEDZIP source %s: %w", source.Path, err)
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != source.Source.SizeBytes ||
			hex.EncodeToString(digest[:]) != source.Source.ChecksumSHA256 {
			return nil, fmt.Errorf("%w: GEDZIP source %s differs from metadata", domain.ErrExportSourceInvalid, source.Path)
		}
		if err := writeZIPEntry(writer, source.Path, body, zip.Store, export.CreatedAt); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish GEDZIP archive: %w", err)
	}
	if int64(archive.Len()) > maxArchiveBytes {
		return nil, domain.ErrExportArchiveTooLarge
	}
	return archive.Bytes(), nil
}

func prepareGEDZIPSources(snapshot manifest.Snapshot) ([]gedzipSource, []gedcom.MediaFile, error) {
	assets := make(map[uuid.UUID]manifest.MediaAsset)
	for _, asset := range snapshot.Manifest.MediaAssets {
		if asset.ID != uuid.Nil && asset.DeletedAt == nil && gedzipMediaStatus(asset.Status) {
			assets[asset.ID] = asset
		}
	}
	variants := make(map[string]manifest.MediaVariant)
	for _, variant := range snapshot.Manifest.MediaVariants {
		variants[variant.MediaID.String()+"/"+variant.Kind] = variant
	}
	sources := make([]gedzipSource, 0, len(snapshot.Files))
	seenPaths := make(map[string]struct{}, len(snapshot.Files))
	seenSources := make(map[string]struct{}, len(snapshot.Files))
	for _, source := range snapshot.Files {
		asset, exists := assets[source.MediaID]
		if !exists {
			return nil, nil, fmt.Errorf("%w: GEDZIP source references inactive media", domain.ErrExportSourceInvalid)
		}
		mimeType := strings.TrimSpace(asset.MIMEType)
		filename := "original"
		sourceKey := source.MediaID.String() + "/original"
		if source.VariantKind != "" {
			variant, exists := variants[source.MediaID.String()+"/"+source.VariantKind]
			if !exists || !safeGEDZIPName(source.VariantKind) {
				return nil, nil, fmt.Errorf("%w: GEDZIP variant metadata is invalid", domain.ErrExportSourceInvalid)
			}
			mimeType = strings.TrimSpace(variant.MIMEType)
			filename = "variants/" + source.VariantKind
			sourceKey = source.MediaID.String() + "/variant/" + source.VariantKind
		}
		extension, exists := gedzipExtension(mimeType)
		if !exists {
			return nil, nil, fmt.Errorf("%w: unsupported GEDZIP media type", domain.ErrExportSourceInvalid)
		}
		path := "media/" + source.MediaID.String() + "/" + filename + extension
		if _, exists := seenSources[sourceKey]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate GEDZIP media source", domain.ErrExportSourceInvalid)
		}
		if _, exists := seenPaths[path]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate GEDZIP path", domain.ErrExportSourceInvalid)
		}
		seenSources[sourceKey] = struct{}{}
		seenPaths[path] = struct{}{}
		sources = append(sources, gedzipSource{Source: source, Path: path, MIMEType: mimeType})
	}
	sort.Slice(sources, func(left int, right int) bool { return sources[left].Path < sources[right].Path })
	mediaFiles := make([]gedcom.MediaFile, 0, len(sources))
	for _, source := range sources {
		mediaFiles = append(mediaFiles, gedcom.MediaFile{
			MediaID: source.Source.MediaID, VariantKind: source.Source.VariantKind,
			Path: source.Path, MIMEType: source.MIMEType,
		})
	}
	return sources, mediaFiles, nil
}

func gedzipMediaStatus(status string) bool {
	return status == "uploaded" || status == "processing" || status == "ready"
}

func validGEDZIPSHA256(value string) bool {
	if value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func gedzipExtension(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	case "application/pdf":
		return ".pdf", true
	default:
		return "", false
	}
}

func safeGEDZIPName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}
