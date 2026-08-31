package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestJSONLoggerWritesStructuredFields(t *testing.T) {
	folder := t.TempDir()
	log, err := NewLogger(LoggerConfig{Level: "INFO", Folder: folder, Format: "json"})
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	log.Info("request completed", zap.String("http_route", "/api/v1/trees/{tree_id}"))
	log.Close()

	files, err := filepath.Glob(filepath.Join(folder, "*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("log files = %v, error = %v", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(content, &entry); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	if got, want := entry["http_route"], "/api/v1/trees/{tree_id}"; got != want {
		t.Fatalf("http_route = %v, want %v", got, want)
	}
}

func TestLoggerConfigRejectsUnknownFormat(t *testing.T) {
	t.Setenv("LOGGER_FORMAT", "xml")
	if _, err := NewConfig(); err == nil {
		t.Fatal("NewConfig() accepted an unsupported logger format")
	}
}
