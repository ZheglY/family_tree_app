package request

import (
	"errors"
	"strings"
	"testing"
)

type payload struct {
	Name string `json:"name"`
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		want    payload
		wantErr error
	}{
		{
			name: "valid body",
			body: `{"name":"Anna"}`,
			want: payload{Name: "Anna"},
		},
		{
			name:    "empty body",
			body:    "",
			wantErr: ErrEmptyBody,
		},
		{
			name:    "multiple values",
			body:    `{"name":"Anna"} {"name":"Maria"}`,
			wantErr: ErrMultipleJSONValues,
		},
		{
			name:    "unknown field",
			body:    `{"name":"Anna","age":30}`,
			wantErr: errors.New("unknown field"),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var got payload
			err := DecodeJSON(strings.NewReader(test.body), &got)

			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("DecodeJSON() error = %v", err)
				}
				if got != test.want {
					t.Fatalf("payload = %#v, want %#v", got, test.want)
				}
				return
			}

			if err == nil {
				t.Fatal("DecodeJSON() error = nil, want error")
			}

			if errors.Is(test.wantErr, ErrEmptyBody) ||
				errors.Is(test.wantErr, ErrMultipleJSONValues) {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("DecodeJSON() error = %v, want %v", err, test.wantErr)
				}
				return
			}

			if !strings.Contains(err.Error(), test.wantErr.Error()) {
				t.Fatalf("DecodeJSON() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
