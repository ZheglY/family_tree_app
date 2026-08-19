package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	ErrEmptyBody          = errors.New("request body is empty")
	ErrMultipleJSONValues = errors.New("request body must contain a single JSON value")
)

// DecodeJSON rejects unknown fields and additional JSON values. A body size
// limit is applied by the HTTP middleware before this helper is called.
func DecodeJSON(body io.Reader, destination any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmptyBody
		}

		return fmt.Errorf("decode JSON request: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("decode trailing JSON data: %w", err)
		}

		return ErrMultipleJSONValues
	}

	return nil
}
