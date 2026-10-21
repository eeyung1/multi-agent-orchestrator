// Package locking serializes local controller operations. Contention is retried;
// an existing lock is never removed or taken over based on its age.
package locking

import (
	"fmt"
	"os"
	"time"
)

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
			return nil, fmt.Errorf("coordination is locked: %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
