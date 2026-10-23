package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
)

func TestOperationsCommandDispatchNeverActivates(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "orchestrator")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	root := t.TempDir()
	cmd := exec.Command(binary, "status", "--workspace", root, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status: %s %v", out, err)
	}
	var v operations.Snapshot
	if json.Unmarshal(out, &v) != nil || v.Initialized || v.Version != operations.Version {
		t.Fatalf("wrong operational entry path: %s", out)
	}
	cmd = exec.Command(binary, "status", "--workspace", root, "--watch", "--updates", "1", "--json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bounded watch: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(root, "multi-agent-orchestrator")); !os.IsNotExist(err) {
		t.Fatal("read-only commands activated or initialized project")
	}
	if runtime.GOOS != "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		watch := exec.CommandContext(ctx, binary, "status", "--workspace", root, "--watch", "--interval", "1h", "--json")
		stdout, err := watch.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := watch.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
			_ = watch.Wait()
			t.Fatal("watch failed before first frame", err)
		}
		if err := watch.Process.Signal(os.Interrupt); err != nil {
			_ = watch.Process.Kill()
			_ = watch.Wait()
			t.Fatal(err)
		}
		if err := watch.Wait(); err != nil {
			t.Fatal("Ctrl-C did not exit cleanly", err)
		}
	}
}
