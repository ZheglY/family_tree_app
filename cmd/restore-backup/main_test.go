package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZheglY/family_tree_app/internal/features/exports/restore"
)

func TestReadLimited(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "backup.zip")
	want := []byte("small backup")
	if err := os.WriteFile(filename, want, 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := readLimited(filename, int64(len(want)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("readLimited() = %q, want %q", body, want)
	}
	if _, err := readLimited(filename, int64(len(want)-1)); !errors.Is(err, restore.ErrBackupTooLarge) {
		t.Fatalf("oversized readLimited() error = %v", err)
	}
}

func TestRunRequiresOneArchivePath(t *testing.T) {
	t.Parallel()
	if err := run(t.Context(), nil); err == nil {
		t.Fatal("run() accepted empty arguments")
	}
}
