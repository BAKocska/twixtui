package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedClock returns a clock that advances a second per call, so ordering by
// last-used time is deterministic instead of depending on how fast the machine
// runs the test.
func fixedClock() func() time.Time {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
}

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	s.now = fixedClock()
	return s
}

func TestDefaultDirHonoursEnvironmentOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/twixtui-test-config")
	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if dir != "/tmp/twixtui-test-config" {
		t.Fatalf("DefaultDir = %q, want the override used verbatim", dir)
	}

	t.Setenv(EnvConfigDir, "")
	dir, err = DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir without override: %v", err)
	}
	if filepath.Base(dir) != "twixtui" {
		t.Fatalf("DefaultDir = %q, want a twixtui subdirectory of the user config dir", dir)
	}
}

func TestOpenCreatesDirectoryAndRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	s := openStore(t, dir)

	want := []string{"Balint", "Zsófia", "Jane Smith"}
	for _, name := range want {
		if _, err := s.Create(name); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.List()
	if len(got) != len(want) {
		t.Fatalf("after reopen got %d profiles, want %d", len(got), len(want))
	}
	// The clock advances per creation, so the last created is the most recently
	// used and therefore first.
	if got[0].Name != "Jane Smith" {
		t.Fatalf("List()[0] = %q, want the most recently used profile", got[0].Name)
	}
	for _, name := range want {
		p, ok := reopened.Get(name)
		if !ok {
			t.Fatalf("Get(%q) after reopen: not found", name)
		}
		if p.Created.IsZero() || p.LastUsed.IsZero() {
			t.Fatalf("Get(%q) = %+v, want both timestamps set", name, p)
		}
		if !p.Created.Equal(p.LastUsed) {
			t.Fatalf("Get(%q): created %v and last used %v differ on a fresh profile", name, p.Created, p.LastUsed)
		}
	}
}

func TestGetAndCreateIgnoreCaseAndSpacing(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.Create("Jane  Smith"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, query := range []string{"Jane  Smith", "jane smith", "JANE SMITH"} {
		if _, ok := s.Get(query); !ok {
			t.Fatalf("Get(%q): not found, want the existing profile", query)
		}
	}
	for _, dup := range []string{"jane smith", "JANE  SMITH", "Jane Smith"} {
		if _, err := s.Create(dup); !errors.Is(err, ErrExists) {
			t.Fatalf("Create(%q) = %v, want ErrExists", dup, err)
		}
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("List has %d profiles, want 1 after rejected duplicates", got)
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{"Balint", nil},
		{"a", nil},
		{"Zsófia", nil},
		{"Jane Smith", nil},
		{"李雷", nil},
		{"O'Brien-Smith Jr.", nil},
		{strings.Repeat("a", MaxNameRunes), nil},
		{"", ErrNameEmpty},
		{strings.Repeat("a", MaxNameRunes+1), ErrNameTooLong},
		{" Balint", ErrNamePadded},
		{"Balint ", ErrNamePadded},
		{"\u00a0Balint", ErrNamePadded},
		{"Bal\tint", ErrNameControl},
		{"Bal\x00int", ErrNameControl},
		{"Bal\nint", ErrNameControl},
		{"Bal\u200eint", ErrNameInvisible},
		{"Bal\u202eint", ErrNameInvisible},
		{string([]byte{0x42, 0xff, 0xfe}), ErrNameNotUTF8},
	}
	for _, c := range cases {
		err := ValidateName(c.name)
		if c.want == nil {
			if err != nil {
				t.Errorf("ValidateName(%q) = %v, want accepted", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("ValidateName(%q) = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestCreateRejectsInvalidNameWithSpecificError(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, err := s.Create("  "); !errors.Is(err, ErrNamePadded) {
		t.Fatalf("Create(\"  \") = %v, want ErrNamePadded", err)
	}
	if _, err := s.Create(strings.Repeat("x", MaxNameRunes+1)); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("Create(overlong) = %v, want ErrNameTooLong", err)
	}
	if got := len(s.List()); got != 0 {
		t.Fatalf("List has %d profiles, want 0 after rejected names", got)
	}
}

func TestTouchReordersList(t *testing.T) {
	s := openStore(t, t.TempDir())
	for _, name := range []string{"Ann", "Bob", "Cid"} {
		if _, err := s.Create(name); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	if got := s.List()[0].Name; got != "Cid" {
		t.Fatalf("List()[0] = %q, want Cid", got)
	}
	if err := s.Touch("ann"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	list := s.List()
	if list[0].Name != "Ann" {
		t.Fatalf("List()[0] = %q, want Ann after touching it", list[0].Name)
	}
	if list[0].Created.Equal(list[0].LastUsed) {
		t.Fatalf("Touch did not move last-used away from created: %+v", list[0])
	}
	if err := s.Touch("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(unknown) = %v, want ErrNotFound", err)
	}
}

func TestRename(t *testing.T) {
	s := openStore(t, t.TempDir())
	created, err := s.Create("Balint")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create("Bernadett"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Rename("balint", "Bálint"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	p, ok := s.Get("Bálint")
	if !ok {
		t.Fatal("Get after rename: not found")
	}
	if !p.Created.Equal(created.Created) {
		t.Fatalf("Rename changed the creation time: %v became %v", created.Created, p.Created)
	}
	if _, ok := s.Get("Balint"); ok {
		t.Fatal("old name still resolves after rename")
	}

	// Changing only the capitalisation is a rename of the same profile, not a
	// collision with itself.
	if err := s.Rename("Bálint", "BÁLINT"); err != nil {
		t.Fatalf("Rename to a different case: %v", err)
	}
	if err := s.Rename("BÁLINT", "bernadett"); !errors.Is(err, ErrExists) {
		t.Fatalf("Rename onto another profile = %v, want ErrExists", err)
	}
	if err := s.Rename("nobody", "Somebody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rename(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.Rename("BÁLINT", " bad"); !errors.Is(err, ErrNamePadded) {
		t.Fatalf("Rename to an invalid name = %v, want ErrNamePadded", err)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	for _, name := range []string{"Ann", "Bob"} {
		if _, err := s.Create(name); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	if err := s.Delete("ANN"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get("Ann"); ok {
		t.Fatal("profile still present after Delete")
	}
	if err := s.Delete("Ann"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(deleted) = %v, want ErrNotFound", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.List(); len(got) != 1 || got[0].Name != "Bob" {
		t.Fatalf("after reopen List = %+v, want only Bob", got)
	}
}

// TestRepeatedOpenWriteCyclesKeepEveryEntry is the durability check: every cycle
// opens the store fresh, appends one profile and closes, which is what a run of
// twixtui does. Nothing may be lost or corrupted along the way.
func TestRepeatedOpenWriteCyclesKeepEveryEntry(t *testing.T) {
	dir := t.TempDir()
	const cycles = 60
	for i := range cycles {
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("cycle %d: Open: %v", i, err)
		}
		if got := len(s.List()); got != i {
			t.Fatalf("cycle %d: store has %d profiles, want %d", i, got, i)
		}
		if _, err := s.Create(fmt.Sprintf("player%02d", i)); err != nil {
			t.Fatalf("cycle %d: Create: %v", i, err)
		}
	}

	final, err := Open(dir)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	if got := len(final.List()); got != cycles {
		t.Fatalf("final store has %d profiles, want %d", got, cycles)
	}
	for i := range cycles {
		name := fmt.Sprintf("player%02d", i)
		if _, ok := final.Get(name); !ok {
			t.Fatalf("%q missing after %d cycles", name, cycles)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("reading store file: %v", err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("store file does not parse: %v", err)
	}
	if doc.Version != storeVersion {
		t.Fatalf("store file version = %d, want %d", doc.Version, storeVersion)
	}
	if len(doc.Profiles) != cycles {
		t.Fatalf("store file holds %d profiles, want %d", len(doc.Profiles), cycles)
	}

	// The atomic write must not leave temporary files behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temporary file %q left behind", e.Name())
		}
	}
}

// TestConcurrentCreateAcrossStores exercises the advisory lock: each goroutine
// has its own Store over the same directory, which is how two twixtui processes
// see it, so the in-process mutex alone would not save the entries.
func TestConcurrentCreateAcrossStores(t *testing.T) {
	dir := t.TempDir()
	const (
		writers = 8
		each    = 5
	)
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s, err := Open(dir)
			if err != nil {
				errs <- err
				return
			}
			for i := range each {
				if _, err := s.Create(fmt.Sprintf("w%dp%d", w, i)); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Create: %v", err)
	}

	final, err := Open(dir)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	if got := len(final.List()); got != writers*each {
		t.Fatalf("store has %d profiles, want %d", got, writers*each)
	}
	for w := range writers {
		for i := range each {
			name := fmt.Sprintf("w%dp%d", w, i)
			if _, ok := final.Get(name); !ok {
				t.Fatalf("%q lost to a concurrent writer", name)
			}
		}
	}
}

func TestReadsSeeAnotherStoresWrites(t *testing.T) {
	dir := t.TempDir()
	reader := openStore(t, dir)
	writer := openStore(t, dir)

	if got := len(reader.List()); got != 0 {
		t.Fatalf("fresh store has %d profiles, want 0", got)
	}
	if _, err := writer.Create("Balint"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := reader.Get("Balint"); !ok {
		t.Fatal("reader did not pick up the other store's write")
	}
}

func TestCorruptFileIsReportedNotDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seeding corrupt file: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open on a corrupt file succeeded, want an error rather than a silent reset")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt file was removed: %v", err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	data, err := json.Marshal(document{Version: storeVersion + 1, Profiles: []Profile{{Name: "Future"}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o600); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	_, err = Open(dir)
	if err == nil {
		t.Fatal("Open on a newer schema succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "newer twixtui") {
		t.Fatalf("error = %v, want it to name the version mismatch", err)
	}
}

func TestWriteSweepsStaleTempsButNotFreshOnes(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	stale := filepath.Join(dir, fileName+".tmp-crashed")
	fresh := filepath.Join(dir, fileName+".tmp-inflight")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("residue"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", p, err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("ageing %s: %v", stale, err)
	}

	if _, err := s.Create("Balint"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary file survived a write: %v", err)
	}
	// A temporary file that could still belong to a writer in flight must be
	// left alone.
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent temporary file was removed: %v", err)
	}
}
