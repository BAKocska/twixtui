//go:build windows

package winfs

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/winfs/winfstest"
)

// held is how long a lock somebody else holds is watched for before it is
// accepted that the second acquisition is waiting. Nothing but time separates
// "waiting" from "never locked anything", and a lock that works keeps the
// second acquisition out for as long as it is held, so a generous window costs
// only time and never a false failure. Every one of these tests then releases
// the lock and insists the waiting acquisition completes, which is what stops
// them passing for a Lock that hands out nothing at all.
const held = time.Second

// granted is how long an acquisition that ought to succeed is given. It is long
// because a loaded machine can be slow, and it is bounded because a lock that
// never comes free has to fail the test rather than hang it.
const granted = 30 * time.Second

type acquisition struct {
	release func()
	err     error
}

// acquire takes the lock in another goroutine and reports the outcome down the
// returned channel.
func acquire(path string, exclusive bool) <-chan acquisition {
	out := make(chan acquisition, 1)
	go func() {
		release, err := Lock(path, exclusive)
		out <- acquisition{release: release, err: err}
	}()
	return out
}

func awaitAcquisition(t *testing.T, c <-chan acquisition) acquisition {
	t.Helper()
	select {
	case got := <-c:
		if got.err != nil {
			t.Fatalf("taking the lock: %v", got.err)
		}
		return got
	case <-time.After(granted):
		t.Fatal("the lock was never granted after it was released")
		return acquisition{}
	}
}

// TestASecondExclusiveLockWaitsForTheFirstInsideOneProcess covers what a mutex
// cannot: two stores opened inside one twixtui, each taking the lock through a
// handle of its own. Windows holds byte-range locks per handle, so the second
// acquisition has to wait even though there is only one process.
func TestASecondExclusiveLockWaitsForTheFirstInsideOneProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json.lock")
	first, err := Lock(path, true)
	if err != nil {
		t.Fatalf("taking the first lock: %v", err)
	}
	second := acquire(path, true)
	select {
	case got := <-second:
		if got.err == nil {
			got.release()
		}
		first()
		t.Fatalf("a second exclusive lock was granted while the first was held (error %v)", got.err)
	case <-time.After(held):
	}
	first()
	awaitAcquisition(t, second).release()
}

// TestSharedLocksOverlapAndAnExclusiveOneWaitsForThem is the other half of the
// shape the stores rely on: readers do not wait for each other, or one listing
// would stall behind every other listing, and a writer waits for all of them.
func TestSharedLocksOverlapAndAnExclusiveOneWaitsForThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json.lock")
	first, err := Lock(path, false)
	if err != nil {
		t.Fatalf("taking the first shared lock: %v", err)
	}
	second := awaitAcquisition(t, acquire(path, false))

	exclusive := acquire(path, true)
	select {
	case got := <-exclusive:
		if got.err == nil {
			got.release()
		}
		first()
		second.release()
		t.Fatalf("an exclusive lock was granted while two shared ones were held (error %v)", got.err)
	case <-time.After(held):
	}
	first()
	select {
	case got := <-exclusive:
		if got.err == nil {
			got.release()
		}
		second.release()
		t.Fatal("an exclusive lock was granted while one of the two shared ones was still held")
	case <-time.After(held):
	}
	second.release()
	awaitAcquisition(t, exclusive).release()
}

const (
	lockHelperEnv     = "TWIXTUI_WINFS_LOCK_HELPER"
	helperStartedLine = "winfs-helper: started"
	helperLockedLine  = "winfs-helper: locked"
)

// TestALockIsHeldAgainstAnotherProcess is the case the lock exists for: two
// twixtui processes sharing one configuration directory. The other process is
// this test binary started again, so the lock it takes is taken by this
// package's own code rather than by something a test wrote to imitate it.
func TestALockIsHeldAgainstAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json.lock")
	release, err := Lock(path, true)
	if err != nil {
		t.Fatalf("taking the lock: %v", err)
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
	cmd := exec.Command(exe, "-test.run=^TestLockHelperTakesTheLock$")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+path)
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

	started, locked := make(chan struct{}), make(chan struct{})
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			switch strings.TrimSpace(lines.Text()) {
			case helperStartedLine:
				close(started)
			case helperLockedLine:
				close(locked)
			}
		}
	}()

	// The handshake is what makes the wait below mean anything: the other
	// process is running and has reached the lock, so from here on what it is
	// doing is waiting for it.
	select {
	case <-started:
	case <-time.After(granted):
		t.Fatalf("the other process never reached the lock: %s", stop())
	}
	select {
	case <-locked:
		t.Fatal("the other process took the lock while this one held it")
	case <-time.After(held):
	}

	released = true
	release()
	select {
	case <-locked:
	case <-time.After(granted):
		t.Fatalf("the other process never got the lock after this one released it: %s", stop())
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("the other process failed: %v\n%s", err, complaints.String())
	}
}

// TestLockHelperTakesTheLock is not a test of its own: it is the other process
// TestALockIsHeldAgainstAnotherProcess needs, and it does nothing unless that
// test starts it. It is a test rather than a program of its own so that the
// lock it takes is taken by this package's code.
func TestLockHelperTakesTheLock(t *testing.T) {
	path := os.Getenv(lockHelperEnv)
	if path == "" {
		t.Skip("not the process this helper is for")
	}
	fmt.Println(helperStartedLine)
	release, err := Lock(path, true)
	if err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	fmt.Println(helperLockedLine)
	release()
}

// TestASharedLockGivesUpWhereNothingMayBeWritten covers a configuration
// directory this user may read and not write — one on read-only media, or
// somebody else's that this user was let into. Reading what is in it is an
// ordinary thing to want, and a lock file cannot be created there, so a shared
// lock goes without one.
func TestASharedLockGivesUpWhereNothingMayBeWritten(t *testing.T) {
	path := filepath.Join(denyWrites(t), "profiles.json.lock")
	release, err := Lock(path, false)
	if err != nil {
		t.Fatalf("a shared lock where nothing may be written: %v", err)
	}
	release()
	if _, err := os.Stat(path); err == nil {
		t.Error("a shared lock created a lock file where nothing may be written")
	}

	// The control: in a directory that does allow it, the same call does take a
	// lock file. Without this, a Lock that quietly locked nothing anywhere
	// would pass the assertion above.
	writable := filepath.Join(t.TempDir(), "profiles.json.lock")
	release, err = Lock(writable, false)
	if err != nil {
		t.Fatalf("a shared lock in a writable directory: %v", err)
	}
	release()
	if _, err := os.Stat(writable); err != nil {
		t.Errorf("a shared lock in a writable directory left no lock file: %v", err)
	}
}

// TestAnExclusiveLockFailsWhereNothingMayBeWritten is the other side of that
// softening: giving up the lock for a read must not have made a write possible
// without one.
func TestAnExclusiveLockFailsWhereNothingMayBeWritten(t *testing.T) {
	release, err := Lock(filepath.Join(denyWrites(t), "profiles.json.lock"), true)
	if err == nil {
		release()
		t.Error("an exclusive lock was taken where nothing may be written")
	}
}

// denyWrites is a directory this process may read and not write. The refusal is
// asserted rather than assumed: a process holding a privilege that overrides
// access control writes there whatever the directory says, and these tests
// would then cover nothing.
func denyWrites(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	restore, err := winfstest.DenyDirectoryWrites(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Error(err)
		}
	})
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o600); err == nil {
		os.Remove(probe)
		t.Skipf("this process creates files in %s whatever its access control says", dir)
	}
	return dir
}

func TestHeldLockCannotBeDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.lock")
	unlock, err := Lock(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.Remove(path); err == nil {
		t.Fatal("deleting a held synchronization file succeeded")
	}
}
