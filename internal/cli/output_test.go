package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// syncBuffer collects a command's output while the command is still running, so
// a test can read what a blocked command has already said. bytes.Buffer alone
// would be read from one goroutine while the command writes from another.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runBlocking executes a command that is expected to sit and wait, and returns
// as soon as it has printed anything at all — that being the property under
// test: a command that blocks must say what it is blocked on before it blocks.
// It then cancels the command, as ctrl+c would, and returns everything printed
// together with the error the command ended with.
//
// The wait is on "any output" rather than on the wording, so that a command
// which prints nothing fails here rather than at an assertion on its text: the
// defect being guarded is the silence, not the phrasing.
func runBlocking(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := NewRootCommand()
	buf := &syncBuffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"--config", dir}, args...))

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for buf.String() == "" {
		select {
		case err := <-done:
			t.Fatalf("%v ended without printing anything: %v", args, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v printed nothing while it waited", args)
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(10 * time.Second):
		t.Fatalf("%v did not stop when it was cancelled; it printed %q", args, buf.String())
	}
	return "", nil
}

// startTestRelay runs a relay on a free port for the duration of the test and
// returns its address. Nobody hosts a game on it, which is the situation that
// leaves a joiner waiting.
func startTestRelay(t *testing.T) string {
	t.Helper()
	l, err := netplay.BindRelay("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = netplay.ServeOn(ctx, l)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return l.Addr().String()
}

// TestPlayJoinSaysWhatItIsWaitingFor covers the case a mistyped pairing code
// lands in: the relay is up, the room is empty, and the joiner waits. It has to
// be distinguishable from a hang, which means saying so before it blocks and
// naming the room it is waiting in.
func TestPlayJoinSaysWhatItIsWaitingFor(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}
	addr := startTestRelay(t)
	code := netplay.PairingCode()

	out, _ := runBlocking(t, dir, "play", "join", "--relay", addr, code)

	for _, want := range []string{code, addr, "ctrl+c"} {
		if !strings.Contains(out, want) {
			t.Errorf("the joiner never mentioned %q while it waited:\n%s", want, out)
		}
	}
}

// TestPlayJoinCancelledExplainsItself covers the other half: cancelling the wait
// closes the socket under a blocked read, and the error that comes back names an
// ephemeral port rather than saying what happened.
func TestPlayJoinCancelledExplainsItself(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}
	addr := startTestRelay(t)

	_, err := runBlocking(t, dir, "play", "join", "--relay", addr, netplay.PairingCode())
	if err == nil {
		t.Fatal("a cancelled join reported success")
	}
	if got := err.Error(); got != "gave up waiting for the host" {
		t.Errorf("a cancelled join ended with %q, which is not a sentence a player can act on", got)
	}
}

// TestServeAnnouncesOnlyAfterItBinds covers what the relay's log asserts. An
// operator reads that line to know the relay is up, so it must not appear for a
// relay that never bound; and it must still appear for one that did, or the test
// would pass on a command that says nothing at all.
func TestServeAnnouncesOnlyAfterItBinds(t *testing.T) {
	const banner = "relay listening on"

	taken := startTestRelay(t)
	out, err := run(t, t.TempDir(), "serve", "--addr", taken)
	if err == nil {
		t.Fatalf("serve claimed the address %s, which is already in use:\n%s", taken, out)
	}
	if strings.Contains(out, banner) {
		t.Errorf("serve announced a relay it failed to bind:\n%s", out)
	}

	// Port 0 asks the operating system for a free port, so the successful case
	// cannot collide with anything and the address printed is one only a
	// successful bind could know.
	out, _ = runBlocking(t, t.TempDir(), "serve", "--addr", "127.0.0.1:0")
	if !strings.Contains(out, banner) {
		t.Errorf("a relay that bound said nothing:\n%s", out)
	}
	if strings.Contains(out, "127.0.0.1:0") {
		t.Errorf("serve announced the address it asked for rather than the one it got:\n%s", out)
	}
}

// TestMain fixes the local zone for the whole binary, somewhere that is not
// UTC, so that a UTC machine cannot make a UTC-versus-local mistake invisible
// to the rendering tests below.
//
// It is done here rather than inside the one test that needs it because
// time.Local is a package-level variable that every time.Now in the process
// reads. Assigning it while tests are running is a data race against anything
// still winding down, and there is such a thing in this package: the race
// detector catches the write landing against a relay goroutine left over from
// an earlier test. Before m.Run, nothing else is running, and it is the only
// point at which the assignment is safe.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("Test/+05:30", 5*3600+1800)
	os.Exit(m.Run())
}

// TestLeaderboardHistoryIsInLocalTime covers the timestamps a player reads their
// own history by. The board stores UTC, which is what makes two machines' logs
// comparable, but the row is read by one person in one place and every other
// surface dates a game in their time.
func TestLeaderboardHistoryIsInLocalTime(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Alice"); err != nil {
		t.Fatal(err)
	}
	board, err := leaderboard.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Late enough in the day that the offset moves the date as well as the
	// clock, so a row rendered in UTC is wrong in a way nobody could miss.
	played := time.Date(2026, 9, 1, 23, 30, 0, 0, time.UTC)
	if err := board.Record(leaderboard.Result{
		Played: played, Player: "Alice", Opponent: leaderboard.BotName("beginner"),
		Outcome: leaderboard.Win, Side: "vertical", Moves: 7, Ruleset: game.Std.Canonical(),
	}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, dir, "leaderboard", "show", "--player", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	const layout = "2006-01-02 15:04"
	if want := played.Local().Format(layout); !strings.Contains(out, want) {
		t.Errorf("the history does not date the game %s in local time:\n%s", want, out)
	}
	if stored := played.UTC().Format(layout); strings.Contains(out, stored) {
		t.Errorf("the history shows the stored UTC time %s rather than the %s the player's clock reads:\n%s",
			stored, played.Local().Format(layout), out)
	}
}

// recordPath writes a game record to a file and returns its path.
func recordPath(t *testing.T, rs game.Ruleset, transcript string) string {
	t.Helper()
	g, err := game.ReplayTranscript(rs, transcript)
	if err != nil {
		t.Fatal(err)
	}
	record, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "game.twixt")
	if err := os.WriteFile(path, []byte(record.Encode()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// importedID reads the identifier out of what an import printed.
func importedID(t *testing.T, out string) string {
	t.Helper()
	fields := strings.Fields(out)
	if len(fields) < 3 {
		t.Fatalf("the import said %q, which does not name the game", out)
	}
	return strings.TrimSuffix(fields[2], ":")
}

// TestImportedGameDoesNotBorrowTheImporter covers what an imported record is
// allowed to claim. The format carries no names and no kind, so filing it under
// the profile that read it in states two things the file does not say: who
// played it, and that it was played here.
func TestImportedGameDoesNotBorrowTheImporter(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", "Carol"); err != nil {
		t.Fatal(err)
	}
	rs := game.Std
	rs.Size = 6
	out, err := run(t, dir, "game", "import", recordPath(t, rs, "B1; resign"))
	if err != nil {
		t.Fatal(err)
	}
	id := importedID(t, out)

	list, err := run(t, dir, "game", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "Carol") {
		t.Errorf("the listing says Carol played a game she only imported:\n%s", list)
	}
	if strings.Contains(list, string(gamestore.Hotseat)) {
		t.Errorf("the listing calls an imported record a game played at this keyboard:\n%s", list)
	}
	if !strings.Contains(list, string(gamestore.Imported)) {
		t.Errorf("the listing does not say where the game came from:\n%s", list)
	}

	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Kind != gamestore.Imported {
		t.Errorf("the record was stored as a %s game", sv.Kind)
	}
	if sv.Player == "Carol" || sv.Opponent == "Carol" {
		t.Errorf("the record was stored with the importing profile in a seat: %q vs %q", sv.Player, sv.Opponent)
	}
}

// TestImportingTheSameRecordTwiceIsRecognised covers the repeat. The digest
// identifies the game, so a second reading of the same file is the game already
// held rather than another one.
func TestImportingTheSameRecordTwiceIsRecognised(t *testing.T) {
	dir := t.TempDir()
	rs := game.Std
	rs.Size = 6
	path := recordPath(t, rs, "B1; resign")

	first, err := run(t, dir, "game", "import", path)
	if err != nil {
		t.Fatal(err)
	}
	id := importedID(t, first)

	second, err := run(t, dir, "game", "import", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, id) {
		t.Errorf("the second import did not recognise the game it already had (%s): %q", id, second)
	}

	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved := store.List(); len(saved) != 1 {
		t.Errorf("importing one record twice left %d saved games", len(saved))
	}
}
