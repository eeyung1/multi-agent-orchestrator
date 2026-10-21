//go:build windows

package execution

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) { cmd.WaitDelay = time.Second }
func cleanupProcess(cmd *exec.Cmd)   {}
