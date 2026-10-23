// Package locking serializes local controller operations. Contention is retried;
// an existing lock is never removed or taken over based on its age.
package locking

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrLocked means acquisition failed before entering the protected operation.
// Callers may retry within their own bounded context; it never permits takeover.
var ErrLocked = errors.New("coordination is locked")

func Acquire(path string) (func(), error) {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Mkdir(path, 0700)
		if err == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
