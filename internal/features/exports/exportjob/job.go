package exportjob

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	KindGenerate = "export.generate"
	KindCleanup  = "export.cleanup"
	KindDelete   = "export.delete"
)

type GeneratePayload struct {
	TreeID   uuid.UUID `json:"tree_id"`
	ExportID uuid.UUID `json:"export_id"`
}

type CleanupPayload struct {
	ExpiredBefore time.Time `json:"expired_before"`
	BatchSize     int       `json:"batch_size"`
}

type DeletePayload struct {
	TreeID   uuid.UUID `json:"tree_id"`
	ExportID uuid.UUID `json:"export_id"`
}

func Encode(value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode export job payload: %w", err)
	}
	return payload, nil
}
