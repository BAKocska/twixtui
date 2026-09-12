//go:build unix || windows

package profile

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	crossProcessDirEnv  = "TWIXTUI_PROFILE_HELPER_DIR"
	crossProcessNameEnv = "TWIXTUI_PROFILE_HELPER_NAME"
	helperStartedLine   = "profile-helper: started"
	helperStoredLine    = "profile-helper: stored"
)

// TestAnotherProcessWaitsForThisOnesWrite covers the case the store's mutex
// cannot reach: two twixtui processes sharing one configuration directory, each
// reading the profiles, adding one and writing the file back. Whichever writes
// second has to have read what the first one stored, or the profile the first
// player just created is gone, and the only thing that arranges that is the
// lock on the file.
//
// The other process is this test binary started again, so what it does is what
// twixtui does. The lock is taken here directly rather than through a mutation,
// because a mutation holds it only for as long as it takes and the other
// process would then be waiting for nothing in particular. Its being kept
// waiting is what tells a real lock from one that grants everything at once;
// the release afterwards, and the two profiles at the end, are what tell a real
// lock from one that grants nothing.
func TestAnotherProcessWaitsForThisOnesWrite(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if _, err := s.Create("Ada"); err != nil {
		t.Fatal(err)
	}

	release, err := lockFile(s.lockPath, true)
	if err != nil {
		t.Fatalf("taking the store's lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProfileHelperCreatesAProfile$")
	cmd.Env = append(os.Environ(), crossProcessDirEnv+"="+dir, crossProcessNameEnv+"=Bea")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var complaints strings.Builder
	cmd.Stderr = &complaints
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// What the other process complained about may only be read once the copying
	// into it has finished, which is what Wait waits for. Reading it while the
	// other process is still running would race with the copy.
	stop := func() string {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		return complaints.String()
	}
	defer stop()

	started, stored := make(chan struct{}), make(chan struct{})
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			switch strings.TrimSpace(lines.Text()) {
			case helperStartedLine:
				close(started)
			case helperStoredLine:
				close(stored)
			}
		}
	}()

	// The handshake is what makes the wait below mean something: the other
	// process is running and about to open the store, so from then on what it
	// is doing is waiting for the lock.
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatalf("the other process never started: %s", stop())
	}
	select {
	case <-stored:
		t.Fatal("the other process wrote the store while this one held the lock")
	case <-time.After(time.Second):
	}

	released = true
	release()
	select {
	case <-stored:
	case <-time.After(30 * time.Second):
		t.Fatalf("the other process never wrote the store after the lock was released: %s", stop())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the other process failed: %v\n%s", err, complaints.String())
	}

	fresh := openStore(t, dir)
	names := make([]string, 0, 2)
	for _, p := range fresh.List() {
		names = append(names, p.Name)
	}
	if len(names) != 2 {
		t.Fatalf("the store holds %v, want both processes' profiles", names)
	}
	for _, want := range []string{"Ada", "Bea"} {
		found := false
		for _, name := range names {
			found = found || name == want
		}
		if !found {
			t.Errorf("%s is missing from %v", want, names)
		}
	}
}

// TestProfileHelperCreatesAProfile is not a test of its own: it is the other
// process TestAnotherProcessWaitsForThisOnesWrite needs, and it does nothing
// unless that test starts it. It goes through Open and Create so that the
// waiting it does is the waiting twixtui does.
func TestProfileHelperCreatesAProfile(t *testing.T) {
	dir := os.Getenv(crossProcessDirEnv)
	if dir == "" {
		t.Skip("not the process this helper is for")
	}
	fmt.Println(helperStartedLine)
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	if _, err := s.Create(os.Getenv(crossProcessNameEnv)); err != nil {
		t.Fatalf("creating a profile: %v", err)
	}
	fmt.Println(helperStoredLine)
}
