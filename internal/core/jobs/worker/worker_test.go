package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
)

func TestCallHandlerConvertsPanicToError(t *testing.T) {
	t.Parallel()
	err := callHandler(context.Background(), HandlerFunc(func(context.Context, jobs.Job) error {
		panic("broken handler")
	}), jobs.Job{})
	if err == nil || !strings.Contains(err.Error(), "broken handler") {
		t.Fatalf("callHandler() error = %v", err)
	}
}

func TestRetryDelayIsExponentialAndCapped(t *testing.T) {
	t.Parallel()
	worker := Worker{config: Config{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  5 * time.Second,
	}}
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 4, want: 5 * time.Second},
		{attempt: 20, want: 5 * time.Second},
	} {
		if got := worker.retryDelay(test.attempt); got != test.want {
			t.Fatalf("retryDelay(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
}
