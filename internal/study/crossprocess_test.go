//go:build unix || windows

package study

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	helperDirEnv    = "TWIXTUI_STUDY_HELPER_DIR"
	helperModeEnv   = "TWIXTUI_STUDY_HELPER_MODE"
	helperWorkerEnv = "TWIXTUI_STUDY_HELPER_WORKER"
	helperReadyLine = "study-helper: ready"
	helperDoneLine  = "study-helper: done"
	// helperStalePrefix starts the line on which a racing process reports how
	// many of its saves were refused as stale and tried again.
	helperStalePrefix = "study-helper: stale "
	helperRaceMode    = "race"
	helperWaitMode    = "wait"
	// goAheadFile is how the other processes are told to go on. It lives in the
	// configuration directory, outside the study directory, so nothing that
	// reads or sweeps studies touches it.
	goAheadFile = "helper-go-ahead"
	// racingSets is how many marks each racing process saves. The two
	// processes start together and every save is a read, a compare and a
	// write with a flush in between, so their cycles overlap many times over.
	racingSets = 40
	// maxAttempts bounds how often one save may be refused as stale, so a
	// store that never stops refusing fails the test rather than hanging it.
	maxAttempts  = 10000
	helperGame   = "race-game"
	helperDigest = "race-record"
)

var (
	// savedElsewhere is what another window saves while the waiting process
	// is held at the study's lock.
	savedElsewhere = Mark{Entry: 1, Note: "saved by the other window"}
	// waitingMark is what the waiting process is trying to save.
	waitingMark = Mark{Entry: 2, Note: "typed while the other window saved", Bookmark: true}
)

// helperTarget is the finished game every process in these tests studies.
func helperTarget() Target {
	return Target{GameID: helperGame, RecordDigest: helperDigest, Entries: 2 * racingSets, Finished: true}
}

// raceMark is the i-th mark racing process worker saves. The two processes own
// separate entries, so every mark either of them saved must still be there at
// the end.
func raceMark(worker, i int) Mark {
	return Mark{Entry: worker*racingSets + i + 1, Note: fmt.Sprintf("worker %d, save %d", worker, i), Bookmark: i%2 == 0}
}

// Two processes save marks into one study as fast as they can, each retrying
// the way an editor has to when its save is refused as stale. A lock that did
// not cover the whole read, compare and write would let both pass the compare
// against the same revision, and one of their saves would vanish: the revision
// would fall short of the number of successful saves and a mark would be
// missing.
func TestTwoProcessesSavingOneStudyLoseNothing(t *testing.T) {
	dir := t.TempDir()
	helpers := []*studyHelper{
		startStudyHelper(t, dir, helperRaceMode, 0),
		startStudyHelper(t, dir, helperRaceMode, 1),
	}
	for _, h := range helpers {
		h.await(t, h.ready, "got ready to save")
	}
	if err := os.WriteFile(filepath.Join(dir, goAheadFile), nil, 0o600); err != nil {
		t.Fatalf("writing the go-ahead: %v", err)
	}
	stale := 0
	for worker, h := range helpers {
		h.await(t, h.done, "finished saving")
		if err := h.finish(t); err != nil {
			t.Fatalf("racing process %d failed: %v\n%s", worker, err, h.stop())
		}
		stale += h.stale
	}
	t.Logf("%d saves were refused as stale and tried again", stale)

	got, err := Open(dir).Load(helperTarget())
	if err != nil {
		t.Fatalf("loading the study both processes saved: %v", err)
	}
	if want := int64(2 * racingSets); got.Revision != want {
		t.Errorf("the study is at revision %d after %d successful saves", got.Revision, want)
	}
	if len(got.Marks) != 2*racingSets {
		t.Errorf("the study holds %d marks, want %d", len(got.Marks), 2*racingSets)
	}
	for worker := range 2 {
		for i := range racingSets {
			want := raceMark(worker, i)
			if m := got.At(want.Entry); m != want {
				t.Errorf("entry %d holds %+v, want %+v", want.Entry, m, want)
			}
		}
	}
}

// The other process loads the study and then tries to save while this one
// holds the study's lock. Another window's save lands while it waits. Once it
// has the lock it must read the study again and find that save, so its own
// save is refused as stale instead of replacing the other window's note.
func TestSaveWaitsForTheLockAndSeesTheSaveBeforeIt(t *testing.T) {
	dir := t.TempDir()
	st := Open(dir)
	tg := helperTarget()

	h := startStudyHelper(t, dir, helperWaitMode, 0)
	// The other process now holds the study as it was: nothing saved yet.
	h.await(t, h.ready, "loaded the study")

	if err := os.MkdirAll(st.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := lockStudy(st.lockPath(tg.GameID))
	if err != nil {
		t.Fatalf("taking the study's lock: %v", err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	if err := os.WriteFile(filepath.Join(dir, goAheadFile), nil, 0o600); err != nil {
		t.Fatalf("writing the go-ahead: %v", err)
	}
	select {
	case <-h.done:
		t.Fatalf("the other process saved while this one held the study's lock: %s", h.stop())
	case <-time.After(time.Second):
	}

	theirs := Study{Version: Version, GameID: tg.GameID, RecordDigest: tg.RecordDigest, Revision: 1, Marks: []Mark{savedElsewhere}}
	data, err := encode(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(st.path(tg.GameID), data); err != nil {
		t.Fatalf("writing the other window's save: %v", err)
	}
	released = true
	release()

	h.await(t, h.done, "saved its note")
	// The other process asserted what it was started for: a stale refusal
	// that handed back the other window's save, then a save on top of it.
	if err := h.finish(t); err != nil {
		t.Fatalf("the other process did not see the save that landed while it waited: %v\n%s", err, h.stop())
	}
	got, err := st.Load(tg)
	if err != nil {
		t.Fatalf("loading the study: %v", err)
	}
	if got.Revision != 2 || got.At(savedElsewhere.Entry) != savedElsewhere || got.At(waitingMark.Entry) != waitingMark {
		t.Fatalf("the study ended as %+v, want revision 2 holding both %+v and %+v", got, savedElsewhere, waitingMark)
	}
}

// studyHelper is another twixtui process and the lines it has reached.
type studyHelper struct {
	cmd                  *exec.Cmd
	complaints           *strings.Builder
	ready, done, scanned chan struct{}
	// stale is what a racing process reported; it is written by the scanner
	// and may be read once scanned is closed.
	stale int
}

// startStudyHelper starts this test binary again as another process.
func startStudyHelper(t *testing.T, dir, mode string, worker int) *studyHelper {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestStudyHelperSaves$")
	cmd.Env = append(os.Environ(),
		helperDirEnv+"="+dir,
		helperModeEnv+"="+mode,
		helperWorkerEnv+"="+strconv.Itoa(worker))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	h := &studyHelper{
		cmd:        cmd,
		complaints: &strings.Builder{},
		ready:      make(chan struct{}),
		done:       make(chan struct{}),
		scanned:    make(chan struct{}),
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.stop() })

	go func() {
		defer close(h.scanned)
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			line := strings.TrimSpace(lines.Text())
			switch {
			case line == helperReadyLine:
				close(h.ready)
			case line == helperDoneLine:
				close(h.done)
			case strings.HasPrefix(line, helperStalePrefix):
				n, err := strconv.Atoi(strings.TrimPrefix(line, helperStalePrefix))
				if err != nil {
					fmt.Fprintf(h.complaints, "unreadable stale count: %s\n", line)
				}
				h.stale = n
			default:
				h.complaints.WriteString(lines.Text())
				h.complaints.WriteByte('\n')
			}
		}
		if err := lines.Err(); err != nil {
			fmt.Fprintf(h.complaints, "reading helper output: %v\n", err)
		}
	}()
	return h
}

// await waits for one of the other process's steps, or says which step it never
// reached and what it complained about.
func (h *studyHelper) await(t *testing.T, step <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-step:
	case <-h.scanned:
		select {
		case <-step:
			return
		default:
			t.Fatalf("the other process exited before it %s: %s", what, h.stop())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the other process never %s: %s", what, h.stop())
	}
}

// finish reads the other process's output to the end and reports how it
// exited. The output comes first because Wait closes the pipe, which would cut
// the scanner off partway through.
func (h *studyHelper) finish(t *testing.T) error {
	t.Helper()
	select {
	case <-h.scanned:
	case <-time.After(30 * time.Second):
		t.Fatalf("the other process never exited: %s", h.stop())
	}
	return h.cmd.Wait()
}

// stop joins the process and its output scanner before reading diagnostics.
func (h *studyHelper) stop() string {
	if h.cmd.ProcessState == nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
	<-h.scanned
	return h.complaints.String()
}

// TestStudyHelperSaves is not a test of its own: it is the other process the
// tests above need, and it does nothing unless one of them starts it. It goes
// through Open, Load and Set, so the waiting it does and the answers it gets
// are the ones twixtui gets.
func TestStudyHelperSaves(t *testing.T) {
	dir := os.Getenv(helperDirEnv)
	if dir == "" {
		t.Skip("not the process this helper is for")
	}
	st := Open(dir)
	tg := helperTarget()
	switch mode := os.Getenv(helperModeEnv); mode {
	case helperRaceMode:
		worker, err := strconv.Atoi(os.Getenv(helperWorkerEnv))
		if err != nil {
			t.Fatalf("reading the worker number: %v", err)
		}
		fmt.Println(helperReadyLine)
		waitForGoAhead(t, filepath.Join(dir, goAheadFile))
		fmt.Printf("%s%d\n", helperStalePrefix, saveRacing(t, st, tg, worker))
	case helperWaitMode:
		loaded, err := st.Load(tg)
		if err != nil {
			t.Fatalf("loading the study: %v", err)
		}
		fmt.Println(helperReadyLine)
		waitForGoAhead(t, filepath.Join(dir, goAheadFile))
		current, err := st.Set(tg, loaded.Revision, waitingMark)
		if !errors.Is(err, ErrStale) {
			t.Fatalf("saving over a save that landed while this one waited = %v, want ErrStale", err)
		}
		if current.Revision != 1 || current.At(savedElsewhere.Entry) != savedElsewhere {
			t.Fatalf("the stale refusal handed back %+v, want the study the other window saved", current)
		}
		if _, err := st.Set(tg, current.Revision, waitingMark); err != nil {
			t.Fatalf("saving again against the revision the refusal reported: %v", err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	fmt.Println(helperDoneLine)
}

// saveRacing saves one racing process's marks the way an editor has to: it
// loads the study, saves against the revision it loaded, and when the save is
// refused as stale it tries again against the revision the refusal reports.
// It returns how many saves were refused.
func saveRacing(t *testing.T, st *Store, tg Target, worker int) int {
	t.Helper()
	stale := 0
	for i := range racingSets {
		want := raceMark(worker, i)
		s, err := st.Load(tg)
		if err != nil {
			t.Fatalf("loading before save %d: %v", i, err)
		}
		for attempt := 0; ; attempt++ {
			if attempt == maxAttempts {
				t.Fatalf("save %d was refused as stale %d times in a row", i, attempt)
			}
			next, err := st.Set(tg, s.Revision, want)
			if errors.Is(err, ErrStale) {
				// Revisions only grow, so a refusal has to report a study
				// that has moved past the one this save was made against.
				if next.Revision <= s.Revision {
					t.Fatalf("a stale refusal of a save against revision %d reported revision %d", s.Revision, next.Revision)
				}
				stale++
				s = next
				continue
			}
			if err != nil {
				t.Fatalf("save %d: %v", i, err)
			}
			if next.Revision != s.Revision+1 || next.At(want.Entry) != want {
				t.Fatalf("save %d against revision %d returned %+v", i, s.Revision, next)
			}
			break
		}
	}
	return stale
}

// waitForGoAhead blocks until the process that started this one says to go
// on.
func waitForGoAhead(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the go-ahead at %s never arrived", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
