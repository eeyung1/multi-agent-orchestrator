package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/locking"
)

func TestWorkerReportRetriesOnlyUnacquiredLock(t *testing.T) {
	for _, mode := range []string{"contention", "fenced", "cancelled", "already-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "already-cancelled" {
				cancel()
			}
			fenced := errors.New("stale worker generation")
			attempts := 0
			err := retryWorkerLock(ctx, func() error {
				attempts++
				switch mode {
				case "fenced":
					return fenced
				case "cancelled":
					cancel()
				}
				if attempts < 3 {
					return fmt.Errorf("state acquisition: %w", locking.ErrLocked)
				}
				return nil
			})
			switch mode {
			case "contention":
				if err != nil || attempts != 3 {
					t.Fatalf("lost valid report to transient contention: %d %v", attempts, err)
				}
			case "fenced":
				if !errors.Is(err, fenced) || attempts != 1 {
					t.Fatalf("fenced report retried: %d %v", attempts, err)
				}
			default:
				want := 1
				if mode == "already-cancelled" {
					want = 0
				}
				if !errors.Is(err, context.Canceled) || attempts != want {
					t.Fatalf("cancellation failed to stop report retry: %d %v", attempts, err)
				}
			}
		})
	}
}
