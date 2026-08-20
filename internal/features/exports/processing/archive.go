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
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
)

func buildZIP(
	ctx context.Context,
	export domain.Export,
	snapshot manifest.Snapshot,
	objectStore storage.ProcessorObjectStore,
	maxArchiveBytes int64,
) ([]byte, error) {
	files := append([]manifest.SourceFile(nil), snapshot.Files...)
	sort.Slice(files, func(left int, right int) bool {
		return files[left].ArchivePath < files[right].ArchivePath
	})
	if err := applyArchivePaths(&snapshot.Manifest, files); err != nil {
		return nil, err
	}
	manifestBody, err := encodeManifest(snapshot.Manifest)
	if err != nil {
		return nil, err
	}
	expectedSize := int64(len(manifestBody))
	if expectedSize > maxArchiveBytes {
		return nil, domain.ErrExportArchiveTooLarge
	}
	for _, file := range files {
		if file.ObjectKey == "" || file.ArchivePath == "" || file.SizeBytes <= 0 ||
			len(file.ChecksumSHA256) != 64 {
			return nil, fmt.Errorf("%w: malformed source metadata", domain.ErrExportSourceInvalid)
		}
		if expectedSize > maxArchiveBytes || file.SizeBytes > maxArchiveBytes-expectedSize {
			return nil, domain.ErrExportArchiveTooLarge
		}
		expectedSize += file.SizeBytes
	}

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	if err := writeZIPEntry(writer, "manifest.json", manifestBody, zip.Deflate, export.CreatedAt); err != nil {
		return nil, err
	}
	manifestChecksum := sha256.Sum256(manifestBody)
	checksumLines := []string{fmt.Sprintf("%s  manifest.json", hex.EncodeToString(manifestChecksum[:]))}
	for _, file := range files {
		body, _, err := objectStore.DownloadObject(ctx, file.ObjectKey, file.SizeBytes)
		if err != nil {
			return nil, fmt.Errorf("download archive source %s: %w", file.ArchivePath, err)
		}
		actualChecksum := sha256.Sum256(body)
		actualChecksumHex := hex.EncodeToString(actualChecksum[:])
		if int64(len(body)) != file.SizeBytes || actualChecksumHex != file.ChecksumSHA256 {
			return nil, fmt.Errorf("%w: source %s differs from metadata", domain.ErrExportSourceInvalid, file.ArchivePath)
		}
		if err := writeZIPEntry(writer, file.ArchivePath, body, zip.Store, export.CreatedAt); err != nil {
			return nil, err
		}
		checksumLines = append(checksumLines, fmt.Sprintf("%s  %s", actualChecksumHex, file.ArchivePath))
	}
	checksums := []byte(strings.Join(checksumLines, "\n") + "\n")
	if err := writeZIPEntry(writer, "checksums.sha256", checksums, zip.Deflate, export.CreatedAt); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish export archive: %w", err)
	}
	if int64(archive.Len()) > maxArchiveBytes {
		return nil, domain.ErrExportArchiveTooLarge
	}
	return archive.Bytes(), nil
}

func applyArchivePaths(value *manifest.Manifest, files []manifest.SourceFile) error {
	assets := make(map[string]string)
	variants := make(map[string]string)
	seenPaths := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, exists := seenPaths[file.ArchivePath]; exists {
			return fmt.Errorf("%w: duplicate archive path", domain.ErrExportSourceInvalid)
		}
		seenPaths[file.ArchivePath] = struct{}{}
		if file.VariantKind == "" {
			assets[file.MediaID.String()] = file.ArchivePath
		} else {
			variants[file.MediaID.String()+"/"+file.VariantKind] = file.ArchivePath
		}
	}
	for index := range value.MediaAssets {
		value.MediaAssets[index].ArchivePath = assets[value.MediaAssets[index].ID.String()]
	}
	for index := range value.MediaVariants {
		key := value.MediaVariants[index].MediaID.String() + "/" + value.MediaVariants[index].Kind
		value.MediaVariants[index].ArchivePath = variants[key]
	}
	return nil
}

func writeZIPEntry(
	writer *zip.Writer,
	name string,
	body []byte,
	method uint16,
	modifiedAt time.Time,
) error {
	header := &zip.FileHeader{Name: name, Method: method}
	header.SetModTime(modifiedAt.UTC())
	header.SetMode(0o644)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create export archive entry %s: %w", name, err)
	}
	if _, err := entry.Write(body); err != nil {
		return fmt.Errorf("write export archive entry %s: %w", name, err)
	}
	return nil
}
