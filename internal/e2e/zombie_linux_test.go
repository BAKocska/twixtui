//go:build linux

package e2e

import (
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestZombieStatusReadsTheKernelsRecord makes a real zombie: a child that exits
// with a known status and a parent that has not called wait on it. The kernel
// still holds the wait status in /proc, and that is what the harness falls back
// to when tmux never reaps the pane's process. Both branches of the encoding are
// checked: an exit code, and a death by signal.
func TestZombieStatusReadsTheKernelsRecord(t *testing.T) {
	// exit 3: the process has exited, this test is its parent and has not
	// waited yet, so it is a zombie until Wait below.
	cmd := exec.Command("sh", "-c", "exit 3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	waitUntilZombie(t, pid)
	if code, ok := zombieStatus(pid); !ok || code != 3 {
		t.Errorf("zombieStatus = %d, %t; want 3, true", code, ok)
	}
	_ = cmd.Wait()
	if _, ok := zombieStatus(pid); ok {
		t.Error("a reaped process still reported a zombie status")
	}

	// kill -9 on itself: death by signal, reported as 128+9 the way a shell does.
	cmd = exec.Command("sh", "-c", "kill -9 $$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid = strconv.Itoa(cmd.Process.Pid)
	waitUntilZombie(t, pid)
	if code, ok := zombieStatus(pid); !ok || code != 128+9 {
		t.Errorf("zombieStatus after SIGKILL = %d, %t; want 137, true", code, ok)
	}
	_ = cmd.Wait()
}

func waitUntilZombie(t *testing.T, pid string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := zombieStatus(pid); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process %s did not become a zombie", pid)
}
