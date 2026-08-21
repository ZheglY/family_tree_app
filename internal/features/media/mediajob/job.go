package mediajob

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	KindProcess = "media.process"
	KindCleanup = "media.cleanup"
)

type ProcessPayload struct {
	TreeID  uuid.UUID `json:"tree_id"`
	MediaID uuid.UUID `json:"media_id"`
}

type CleanupPayload struct {
	PendingBefore time.Time `json:"pending_before"`
	DeletedBefore time.Time `json:"deleted_before"`
	BatchSize     int       `json:"batch_size"`
}

func Encode(value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode media job payload: %w", err)
	}
	return payload, nil
}
