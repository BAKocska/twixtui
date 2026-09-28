package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
)

// maxBudget is the time the max tier may spend on a move. A move that arrives
// well inside it after play now was pressed was cut short rather than left to
// run out its clock.
const maxBudget = 10 * time.Second

var (
	progressDepth = regexp.MustCompile(`depth (\d+) done`)
	// playedEarly is the announcement of a move play now cut short, in both
	// its forms: from a completed iteration, or before one had finished.
	playedEarly = regexp.MustCompile(`played ([A-Z]{1,2}\d{1,2}) early, (?:from its completed depth (\d+)|before any depth was complete)`)
	// playNowLead is the front of the status line of a terminal with no panel
	// while the engine searches: the play-now key, then the search's time or,
	// once an iteration has finished and the two do not fit, its depth alone.
	playNowLead = regexp.MustCompile(`^esc play now · (?:\d+\.\ds|d\d+)\b`)
	// playedMove is the engine's move as its announcement names it, which at
	// the smallest width is all of the announcement that fits.
	playedMove = regexp.MustCompile(`played ([A-Z]{1,2}\d{1,2})\b`)
)

// TestPlayNowCutsAMaxSearchShort plays against the max engine on a full-size
// board, where its opening reply searches for seconds, and presses escape
// while it is still thinking. The compiled program has to play exactly one
// legal reply long before the engine's budget would have run out, hand the
// move back to the player, and then report that search's figures on request.
// A completed iteration shown on the thinking line has to be one the move was
// played from. The stored record is the judge of the move: it replays under
// the rules and counts its entries.
//
// How far the search gets before escape is the machine's business. A slow or
// busy runner may not finish depth one in the time the test watches for it,
// and play now before any iteration is valid behaviour with an announcement of
// its own, so either outcome passes; what each one has to be consistent with
// is what the screen showed.
func TestPlayNowCutsAMaxSearchShort(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	tm := sessionIn(t, cfg, 120, 40, "play", "bot", "--tier", "max", "--side", "vertical", "--seed", "1")

	// Positive control: the game is up and waiting for the player.
	frame := tm.MustWaitFor("vertical to move", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of starting the game:\n%s", frame)
	}
	frame = tm.WaitSettled(10 * time.Second)

	// The cursor starts on the centre hole: place there and commit.
	tm.SendKeys("Space")
	tm.WaitChanged(frame, 10*time.Second)
	tm.SendKeys("Enter")
	tm.MustWaitFor("is thinking", 20*time.Second)

	// The thinking line is watched for a completed iteration for a share of
	// the budget, and escape is pressed once one shows or the share is up.
	// Either way the search still has most of its budget left, so a move that
	// follows soon after can only be play now's.
	shown := 0
	watch := time.Now().Add(maxBudget * 2 / 5)
	for {
		screen := tm.Capture()
		if !strings.Contains(screen, "is thinking") || playedEarly.MatchString(screen) {
			t.Fatalf("the engine was no longer thinking when play now was about to be pressed:\n%s", screen)
		}
		if found := progressDepth.FindStringSubmatch(screen); found != nil {
			depth, err := strconv.Atoi(found[1])
			if err != nil || depth < 1 {
				t.Fatalf("the thinking line claims an impossible completed depth %q:\n%s", found[1], screen)
			}
			shown = depth
			break
		}
		if time.Now().After(watch) {
			break
		}
		time.Sleep(pollInterval)
	}

	pressed := time.Now()
	tm.SendKeys("Escape")
	var found []string
	for {
		screen := tm.Capture()
		if found = playedEarly.FindStringSubmatch(screen); found != nil {
			break
		}
		if time.Since(pressed) > maxBudget/2 {
			t.Fatalf("the engine had not played %s after play now was pressed:\n%s", maxBudget/2, screen)
		}
		time.Sleep(pollInterval)
	}
	move := found[1]
	// A depth the thinking line showed as done was finished work, so the move
	// has to come from it or a deeper one. With none shown, either form is
	// right: an iteration can finish in the moment between the last look at
	// the screen and the key reaching the program. The form played before any
	// iteration has no depth, which reads as nought here.
	from, _ := strconv.Atoi(found[2])
	switch {
	case shown > 0 && from < shown:
		t.Fatalf("the thinking line showed depth %d done, but the move was announced as %q", shown, found[0])
	case shown == 0:
		t.Logf("no completed depth showed within %s; the move was announced as %q", maxBudget*2/5, found[0])
	}
	// The frame the turn line first appears in can be one the terminal has
	// only part of, with the thinking line still on the rows the repaint has
	// not reached, so the engine is looked for once the screen has settled.
	tm.MustWaitFor("vertical to move", 10*time.Second)
	after := tm.WaitSettled(10 * time.Second)
	if strings.Contains(after, "is thinking") {
		t.Fatalf("the engine is still thinking after its move was played:\n%s", after)
	}
	tm.AssertFits()

	// The figures of that search. Escape and the next key sent quickly can
	// arrive as one alt-modified key, which is why the move was waited for
	// first.
	tm.SendKeys("i")
	tm.MustWaitFor("nodes", 10*time.Second)
	stats := tm.WaitSettled(10 * time.Second)
	var line string
	for _, l := range strings.Split(stats, "\n") {
		if strings.Contains(l, "nodes") {
			line = l
		}
	}
	if !strings.Contains(line, move) || !strings.Contains(line, "play now") {
		t.Errorf("the search's figures %q do not name the move %s and the play now that ended the search", line, move)
	}

	tm.SendKeys("q")
	if code, exited := tm.WaitExit(20 * time.Second); !exited || code != 0 {
		t.Fatalf("the program did not leave cleanly: exited=%v status=%d\n%s", exited, code, tm.Capture())
	}

	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved := store.List()
	if len(saved) != 1 {
		t.Fatalf("%d games stored, want 1", len(saved))
	}
	g, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the stored game does not replay: %v", err)
	}
	if g.Entries() != 2 || g.Turn() != game.Vertical {
		t.Fatalf("the stored game holds %d entries with %v to move, want the player's move and one reply:\n%s",
			g.Entries(), g.Turn(), g)
	}
	if reply := g.History()[1]; reply.Player != game.Horizontal || reply.Peg.String() != move {
		t.Errorf("the stored reply is %v at %s, want horizontal at %s", reply.Player, reply.Peg, move)
	}
}

// TestPlayNowIsOfferedOnTheSmallestTerminal commits a move against the max
// engine at the minimum size, where there is no panel and the status line is
// the only place the play-now key can be seen, and presses nothing else. The
// commit's announcement stands until the next key, and it used to take the
// whole line: the key and the search stayed hidden for as long as the engine
// searched, which is exactly when a player waiting on it has no reason to press
// anything. TestPlayNowCutsAMaxSearchShort plays at 120x40, where the panel
// shows the search and names the key, so it could not see this. The key and
// the search have to lead the bottom row while the engine searches, escape has
// to cut the search short from there, and the move's own announcement has to
// take the row once the move is played. The stored record is the judge of the
// move.
func TestPlayNowIsOfferedOnTheSmallestTerminal(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	tm := sessionIn(t, cfg, 20, 8, "play", "bot", "--tier", "max", "--side", "vertical", "--seed", "9")

	// Positive control: the game is up and waiting for the player. At this
	// width the turn line is cut to its first words.
	frame := tm.MustWaitFor("vertical to", 20*time.Second)
	if !tm.Alive() {
		t.Fatalf("the program exited instead of starting the game:\n%s", frame)
	}
	frame = tm.WaitSettled(10 * time.Second)
	if status := lbStatusLine(frame); strings.Contains(status, "play now") {
		t.Fatalf("play now is offered before the engine has anything to play: %q\n%s", status, frame)
	}

	// The cursor starts on the centre hole: place there and commit, and press
	// nothing else until play now shows.
	tm.SendKeys("Space")
	tm.WaitChanged(frame, 10*time.Second)
	committed := time.Now()
	tm.SendKeys("Enter")
	for {
		screen := tm.Capture()
		if playNowLead.MatchString(lbStatusLine(screen)) {
			break
		}
		if playedMove.MatchString(screen) {
			t.Fatalf("the engine moved before the bottom row offered play now:\n%s", screen)
		}
		// The engine still has most of its budget left by then, so the
		// search it was on is still running.
		if time.Since(committed) > maxBudget*2/5 {
			t.Fatalf("%s after the commit, with no other key pressed, the bottom row does not lead with play now and the search:\n%s",
				maxBudget*2/5, screen)
		}
		time.Sleep(pollInterval)
	}
	tm.AssertFits()

	pressed := time.Now()
	tm.SendKeys("Escape")
	for {
		screen := tm.Capture()
		if playedMove.MatchString(lbStatusLine(screen)) {
			break
		}
		if time.Since(pressed) > maxBudget/2 {
			t.Fatalf("the engine had not played %s after play now was pressed:\n%s", maxBudget/2, screen)
		}
		time.Sleep(pollInterval)
	}
	// The move is read from a settled frame: the first frame it shows in can
	// be one the terminal has only part of, and play now must not still be
	// offered on the finished one. The move is also waited for before the next
	// key, since escape and a key sent straight after it can arrive as one
	// alt-modified key.
	after := tm.WaitSettled(10 * time.Second)
	status := lbStatusLine(after)
	found := playedMove.FindStringSubmatch(status)
	if found == nil || strings.Contains(status, "play now") {
		t.Fatalf("once the engine has moved, the bottom row %q is not its announcement:\n%s", status, after)
	}
	move := found[1]
	tm.AssertFits()

	tm.SendKeys("q")
	if code, exited := tm.WaitExit(20 * time.Second); !exited || code != 0 {
		t.Fatalf("the program did not leave cleanly: exited=%v status=%d\n%s", exited, code, tm.Capture())
	}

	store, err := gamestore.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved := store.List()
	if len(saved) != 1 {
		t.Fatalf("%d games stored, want 1", len(saved))
	}
	g, err := saved[0].Game()
	if err != nil {
		t.Fatalf("the stored game does not replay: %v", err)
	}
	if g.Entries() != 2 || g.Turn() != game.Vertical {
		t.Fatalf("the stored game holds %d entries with %v to move, want the player's move and one reply:\n%s",
			g.Entries(), g.Turn(), g)
	}
	if reply := g.History()[1]; reply.Player != game.Horizontal || reply.Peg.String() != move {
		t.Errorf("the stored reply is %v at %s, want horizontal at %s", reply.Player, reply.Peg, move)
	}
}
