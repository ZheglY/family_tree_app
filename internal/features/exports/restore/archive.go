package restore

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

const maxArchiveEntries = 10000

var (
	ErrInvalidBackup   = errors.New("invalid family tree backup")
	ErrBackupTooLarge  = errors.New("family tree backup is too large")
	ErrRestoreConflict = errors.New("family tree backup conflicts with target")
)

type Archive struct {
	Manifest manifest.Manifest
	Files    map[string][]byte
}

func ParseZIP(body []byte, maxUncompressedBytes int64) (Archive, error) {
	if len(body) == 0 || maxUncompressedBytes < 1 || int64(len(body)) > maxUncompressedBytes {
		return Archive{}, ErrBackupTooLarge
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return Archive{}, fmt.Errorf("%w: open ZIP: %v", ErrInvalidBackup, err)
	}
	if len(reader.File) < 2 || len(reader.File) > maxArchiveEntries {
		return Archive{}, fmt.Errorf("%w: unexpected entry count", ErrInvalidBackup)
	}
	entries := make(map[string][]byte, len(reader.File))
	var total int64
	for _, file := range reader.File {
		if !safeArchivePath(file.Name) || file.FileInfo().IsDir() {
			return Archive{}, fmt.Errorf("%w: unsafe ZIP path", ErrInvalidBackup)
		}
		if _, exists := entries[file.Name]; exists {
			return Archive{}, fmt.Errorf("%w: duplicate ZIP path", ErrInvalidBackup)
		}
		if file.UncompressedSize64 > uint64(maxUncompressedBytes-total) {
			return Archive{}, ErrBackupTooLarge
		}
		opened, err := file.Open()
		if err != nil {
			return Archive{}, fmt.Errorf("%w: open %s: %v", ErrInvalidBackup, file.Name, err)
		}
		entryBody, readErr := io.ReadAll(io.LimitReader(opened, maxUncompressedBytes-total+1))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil {
			return Archive{}, fmt.Errorf("%w: read %s", ErrInvalidBackup, file.Name)
		}
		total += int64(len(entryBody))
		if total > maxUncompressedBytes {
			return Archive{}, ErrBackupTooLarge
		}
		entries[file.Name] = entryBody
	}
	manifestBody, hasManifest := entries["manifest.json"]
	checksumsBody, hasChecksums := entries["checksums.sha256"]
	if !hasManifest || !hasChecksums {
		return Archive{}, fmt.Errorf("%w: manifest or checksums are missing", ErrInvalidBackup)
	}
	checksums, err := parseChecksums(checksumsBody)
	if err != nil {
		return Archive{}, err
	}
	if len(checksums) != len(entries)-1 {
		return Archive{}, fmt.Errorf("%w: checksum entry set differs from ZIP", ErrInvalidBackup)
	}
	for name, entryBody := range entries {
		if name == "checksums.sha256" {
			continue
		}
		expected, exists := checksums[name]
		if !exists || checksum(entryBody) != expected {
			return Archive{}, fmt.Errorf("%w: checksum mismatch for %s", ErrInvalidBackup, name)
		}
	}
	var backupManifest manifest.Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backupManifest); err != nil {
		return Archive{}, fmt.Errorf("%w: decode manifest: %v", ErrInvalidBackup, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Archive{}, fmt.Errorf("%w: manifest contains extra JSON", ErrInvalidBackup)
	}
	if backupManifest.Schema.Name != domain.ManifestSchemaName ||
		backupManifest.Schema.Version != domain.ManifestSchemaVersion ||
		backupManifest.Export.Format != domain.FormatZIPBackup ||
		backupManifest.Tree.ID == uuid.Nil {
		return Archive{}, fmt.Errorf("%w: unsupported manifest schema", ErrInvalidBackup)
	}
	if err := validateManifestRelations(backupManifest); err != nil {
		return Archive{}, err
	}
	expectedFiles, err := expectedManifestFiles(backupManifest)
	if err != nil {
		return Archive{}, err
	}
	if len(entries) != len(expectedFiles)+2 {
		return Archive{}, fmt.Errorf("%w: ZIP contains unreferenced files", ErrInvalidBackup)
	}
	files := make(map[string][]byte, len(expectedFiles))
	for name, metadata := range expectedFiles {
		entryBody, exists := entries[name]
		if !exists || int64(len(entryBody)) != metadata.sizeBytes || checksum(entryBody) != metadata.checksum {
			return Archive{}, fmt.Errorf("%w: manifest metadata mismatch for %s", ErrInvalidBackup, name)
		}
		files[name] = entryBody
	}
	archive := Archive{Manifest: backupManifest, Files: files}
	if err := validateArchive(archive); err != nil {
		return Archive{}, err
	}
	return archive, nil
}

func validateArchive(archive Archive) error {
	if archive.Manifest.Schema.Name != domain.ManifestSchemaName ||
		archive.Manifest.Schema.Version != domain.ManifestSchemaVersion ||
		archive.Manifest.Export.Format != domain.FormatZIPBackup {
		return fmt.Errorf("%w: unsupported manifest schema", ErrInvalidBackup)
	}
	if err := validateManifestRelations(archive.Manifest); err != nil {
		return err
	}
	expected, err := expectedManifestFiles(archive.Manifest)
	if err != nil {
		return err
	}
	if len(expected) != len(archive.Files) {
		return fmt.Errorf("%w: file set differs from manifest", ErrInvalidBackup)
	}
	for name, metadata := range expected {
		body, exists := archive.Files[name]
		if !exists || int64(len(body)) != metadata.sizeBytes || checksum(body) != metadata.checksum {
			return fmt.Errorf("%w: file metadata mismatch for %s", ErrInvalidBackup, name)
		}
	}
	return nil
}

func validateManifestRelations(value manifest.Manifest) error {
	if value.Tree.OwnerUserID == uuid.Nil || strings.TrimSpace(value.Tree.Name) == "" || value.Tree.Version < 1 {
		return fmt.Errorf("%w: tree metadata is invalid", ErrInvalidBackup)
	}
	members := make(map[uuid.UUID]struct{}, len(value.Members))
	activeOwners := 0
	for _, member := range value.Members {
		if member.UserID == uuid.Nil {
			return fmt.Errorf("%w: member ID is empty", ErrInvalidBackup)
		}
		if _, exists := members[member.UserID]; exists {
			return fmt.Errorf("%w: duplicate member", ErrInvalidBackup)
		}
		members[member.UserID] = struct{}{}
		if member.Role == "owner" && member.Status == "active" {
			activeOwners++
			if member.UserID != value.Tree.OwnerUserID {
				return fmt.Errorf("%w: active owner differs from tree owner", ErrInvalidBackup)
			}
		}
	}
	if activeOwners != 1 {
		return fmt.Errorf("%w: tree must have one active owner", ErrInvalidBackup)
	}
	persons := make(map[uuid.UUID]struct{}, len(value.Persons))
	preferredNames := make(map[uuid.UUID]int, len(value.Persons))
	for _, person := range value.Persons {
		if person.ID == uuid.Nil {
			return fmt.Errorf("%w: person ID is empty", ErrInvalidBackup)
		}
		if _, exists := persons[person.ID]; exists {
			return fmt.Errorf("%w: duplicate person", ErrInvalidBackup)
		}
		persons[person.ID] = struct{}{}
	}
	names := make(map[uuid.UUID]struct{}, len(value.PersonNames))
	for _, name := range value.PersonNames {
		if name.ID == uuid.Nil {
			return fmt.Errorf("%w: person name ID is empty", ErrInvalidBackup)
		}
		if _, exists := names[name.ID]; exists {
			return fmt.Errorf("%w: duplicate person name", ErrInvalidBackup)
		}
		names[name.ID] = struct{}{}
		if _, exists := persons[name.PersonID]; !exists {
			return fmt.Errorf("%w: name references unknown person", ErrInvalidBackup)
		}
		if name.IsPreferred {
			preferredNames[name.PersonID]++
		}
	}
	for personID := range persons {
		if preferredNames[personID] != 1 {
			return fmt.Errorf("%w: person must have one preferred name", ErrInvalidBackup)
		}
	}
	relations := make(map[uuid.UUID]struct{}, len(value.ParentChildRelations))
	activeRelations := make(map[string]struct{}, len(value.ParentChildRelations))
	children := make(map[uuid.UUID][]uuid.UUID, len(value.Persons))
	indegrees := make(map[uuid.UUID]int, len(value.Persons))
	for personID := range persons {
		indegrees[personID] = 0
	}
	for _, relation := range value.ParentChildRelations {
		if relation.ID == uuid.Nil || relation.ParentPersonID == relation.ChildPersonID {
			return fmt.Errorf("%w: relation identity is invalid", ErrInvalidBackup)
		}
		if _, exists := relations[relation.ID]; exists {
			return fmt.Errorf("%w: duplicate relation", ErrInvalidBackup)
		}
		relations[relation.ID] = struct{}{}
		if _, exists := persons[relation.ParentPersonID]; !exists {
			return fmt.Errorf("%w: relation parent is unknown", ErrInvalidBackup)
		}
		if _, exists := persons[relation.ChildPersonID]; !exists {
			return fmt.Errorf("%w: relation child is unknown", ErrInvalidBackup)
		}
		if relation.DeletedAt == nil {
			key := relation.ParentPersonID.String() + "/" + relation.ChildPersonID.String() +
				"/" + relation.RelationType
			if _, exists := activeRelations[key]; exists {
				return fmt.Errorf("%w: duplicate active relation", ErrInvalidBackup)
			}
			activeRelations[key] = struct{}{}
			children[relation.ParentPersonID] = append(
				children[relation.ParentPersonID],
				relation.ChildPersonID,
			)
			indegrees[relation.ChildPersonID]++
		}
	}
	queue := make([]uuid.UUID, 0, len(persons))
	for personID, indegree := range indegrees {
		if indegree == 0 {
			queue = append(queue, personID)
		}
	}
	visited := 0
	for len(queue) > 0 {
		personID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		for _, childID := range children[personID] {
			indegrees[childID]--
			if indegrees[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}
	if visited != len(persons) {
		return fmt.Errorf("%w: active parent-child graph contains a cycle", ErrInvalidBackup)
	}
	unions := make(map[uuid.UUID]struct{}, len(value.Unions))
	for _, union := range value.Unions {
		if union.ID == uuid.Nil {
			return fmt.Errorf("%w: union ID is empty", ErrInvalidBackup)
		}
		if _, exists := unions[union.ID]; exists {
			return fmt.Errorf("%w: duplicate union", ErrInvalidBackup)
		}
		unions[union.ID] = struct{}{}
	}
	unionMembers := make(map[string]struct{}, len(value.UnionMembers))
	for _, member := range value.UnionMembers {
		if _, exists := unions[member.UnionID]; !exists {
			return fmt.Errorf("%w: union member references unknown union", ErrInvalidBackup)
		}
		if _, exists := persons[member.PersonID]; !exists {
			return fmt.Errorf("%w: union member references unknown person", ErrInvalidBackup)
		}
		key := member.UnionID.String() + "/" + member.PersonID.String()
		if _, exists := unionMembers[key]; exists {
			return fmt.Errorf("%w: duplicate union member", ErrInvalidBackup)
		}
		unionMembers[key] = struct{}{}
	}
	mediaIDs := make(map[uuid.UUID]struct{}, len(value.MediaAssets))
	for _, asset := range value.MediaAssets {
		if asset.ID == uuid.Nil {
			return fmt.Errorf("%w: media ID is empty", ErrInvalidBackup)
		}
		if _, exists := mediaIDs[asset.ID]; exists {
			return fmt.Errorf("%w: duplicate media", ErrInvalidBackup)
		}
		mediaIDs[asset.ID] = struct{}{}
	}
	variantIDs := make(map[uuid.UUID]struct{}, len(value.MediaVariants))
	for _, variant := range value.MediaVariants {
		if variant.ID == uuid.Nil {
			return fmt.Errorf("%w: media variant ID is empty", ErrInvalidBackup)
		}
		if _, exists := variantIDs[variant.ID]; exists {
			return fmt.Errorf("%w: duplicate media variant", ErrInvalidBackup)
		}
		variantIDs[variant.ID] = struct{}{}
		if _, exists := mediaIDs[variant.MediaID]; !exists {
			return fmt.Errorf("%w: media variant references unknown media", ErrInvalidBackup)
		}
	}
	attachments := make(map[string]struct{}, len(value.PersonMedia))
	for _, attachment := range value.PersonMedia {
		if _, exists := persons[attachment.PersonID]; !exists {
			return fmt.Errorf("%w: attachment references unknown person", ErrInvalidBackup)
		}
		if _, exists := mediaIDs[attachment.MediaID]; !exists {
			return fmt.Errorf("%w: attachment references unknown media", ErrInvalidBackup)
		}
		key := attachment.PersonID.String() + "/" + attachment.MediaID.String()
		if _, exists := attachments[key]; exists {
			return fmt.Errorf("%w: duplicate media attachment", ErrInvalidBackup)
		}
		attachments[key] = struct{}{}
	}
	if value.Tree.RootPersonID != nil {
		if _, exists := persons[*value.Tree.RootPersonID]; !exists {
			return fmt.Errorf("%w: root person is unknown", ErrInvalidBackup)
		}
	}
	if value.Tree.CoverMediaID != nil {
		if _, exists := mediaIDs[*value.Tree.CoverMediaID]; !exists {
			return fmt.Errorf("%w: cover media is unknown", ErrInvalidBackup)
		}
	}
	for _, person := range value.Persons {
		if person.PrimaryMediaID != nil {
			if _, exists := mediaIDs[*person.PrimaryMediaID]; !exists {
				return fmt.Errorf("%w: primary media is unknown", ErrInvalidBackup)
			}
		}
	}
	return nil
}

type fileMetadata struct {
	sizeBytes int64
	checksum  string
}

func expectedManifestFiles(value manifest.Manifest) (map[string]fileMetadata, error) {
	result := make(map[string]fileMetadata)
	mediaIDs := make(map[uuid.UUID]struct{}, len(value.MediaAssets))
	mediaRequiresFiles := make(map[uuid.UUID]bool, len(value.MediaAssets))
	for _, asset := range value.MediaAssets {
		if asset.ID == uuid.Nil {
			return nil, fmt.Errorf("%w: media ID is empty", ErrInvalidBackup)
		}
		if _, exists := mediaIDs[asset.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate media ID", ErrInvalidBackup)
		}
		mediaIDs[asset.ID] = struct{}{}
		requiresFile := asset.DeletedAt == nil &&
			(asset.Status == "uploaded" || asset.Status == "processing" || asset.Status == "ready")
		if requiresFile && asset.ArchivePath == "" {
			return nil, fmt.Errorf("%w: active media file is missing", ErrInvalidBackup)
		}
		mediaRequiresFiles[asset.ID] = requiresFile
		if asset.ArchivePath == "" {
			continue
		}
		if asset.ArchivePath != "media/"+asset.ID.String()+"/original" {
			return nil, fmt.Errorf("%w: media archive path is not canonical", ErrInvalidBackup)
		}
		if err := addExpectedFile(result, asset.ArchivePath, asset.SizeBytes, asset.ChecksumSHA256); err != nil {
			return nil, err
		}
	}
	for _, variant := range value.MediaVariants {
		if variant.ID == uuid.Nil || variant.MediaID == uuid.Nil {
			return nil, fmt.Errorf("%w: media variant ID is empty", ErrInvalidBackup)
		}
		if _, exists := mediaIDs[variant.MediaID]; !exists {
			return nil, fmt.Errorf("%w: media variant references unknown media", ErrInvalidBackup)
		}
		if mediaRequiresFiles[variant.MediaID] && variant.ArchivePath == "" {
			return nil, fmt.Errorf("%w: active media variant file is missing", ErrInvalidBackup)
		}
		if variant.ArchivePath == "" {
			continue
		}
		if variant.ArchivePath != "media/"+variant.MediaID.String()+"/variants/"+variant.Kind {
			return nil, fmt.Errorf("%w: media variant archive path is not canonical", ErrInvalidBackup)
		}
		if err := addExpectedFile(result, variant.ArchivePath, variant.SizeBytes, variant.ChecksumSHA256); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func addExpectedFile(result map[string]fileMetadata, name string, sizeBytes int64, expectedChecksum string) error {
	if !safeArchivePath(name) || name == "manifest.json" || name == "checksums.sha256" ||
		sizeBytes <= 0 || !validChecksum(expectedChecksum) {
		return fmt.Errorf("%w: invalid manifest file reference", ErrInvalidBackup)
	}
	if _, exists := result[name]; exists {
		return fmt.Errorf("%w: duplicate manifest file reference", ErrInvalidBackup)
	}
	result[name] = fileMetadata{sizeBytes: sizeBytes, checksum: expectedChecksum}
	return nil
}

func parseChecksums(body []byte) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || !validChecksum(parts[0]) || !safeArchivePath(parts[1]) ||
			parts[1] == "checksums.sha256" {
			return nil, fmt.Errorf("%w: malformed checksum list", ErrInvalidBackup)
		}
		if _, exists := result[parts[1]]; exists {
			return nil, fmt.Errorf("%w: duplicate checksum path", ErrInvalidBackup)
		}
		result[parts[1]] = parts[0]
	}
	return result, nil
}

func safeArchivePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !strings.HasPrefix(value, "/") &&
		path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "../")
}

func validChecksum(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func checksum(body []byte) string {
	value := sha256.Sum256(body)
	return hex.EncodeToString(value[:])
}
