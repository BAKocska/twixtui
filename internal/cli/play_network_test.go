package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// pnSaved writes an unfinished game straight into a configuration directory's
// store, which is what a connection dropping mid-game leaves behind.
func pnSaved(t *testing.T, dir string, sv gamestore.Saved) gamestore.Saved {
	t.Helper()
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs := game.Std
	rs.Size = 8
	g := game.MustNew(rs)
	for _, h := range []string{"B1", "A2"} {
		p, err := game.ParsePoint(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.PlayPeg(p); err != nil {
			t.Fatalf("playing %s: %v", h, err)
		}
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	if sv.ID == "" {
		sv.ID = gamestore.NewID()
	}
	if sv.Created.IsZero() {
		sv.Created = time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	}
	sv.Record = rec.Encode()
	if err := store.Put(sv); err != nil {
		t.Fatal(err)
	}
	return sv
}

// pnRemoteGame is the ordinary case: Alice's saved network game against Bea.
func pnRemoteGame(t *testing.T, dir string) gamestore.Saved {
	t.Helper()
	return pnSaved(t, dir, gamestore.Saved{
		Kind:     gamestore.Remote,
		Player:   "Alice",
		Side:     "vertical",
		Opponent: leaderboard.RemoteName("Bea"),
	})
}

// pnProfile makes a configuration directory with one profile in it.
func pnProfile(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := run(t, dir, "profile", "create", name); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPlayHostResumeNamesTheGameAndTheAddressItBound covers the whole preamble
// a player reads before the wait: which saved game is being continued, which
// of this machine's addresses is listening, and the command the opponent runs.
//
// The last of those used to be "play join <your address>:4270" whatever the
// host had bound; a host that bound one interface knows the address to hand
// out, and printing a placeholder made the player work out what it already
// knew.
func TestPlayHostResumeNamesTheGameAndTheAddressItBound(t *testing.T) {
	dir := pnProfile(t, "Alice")
	sv := pnRemoteGame(t, dir)

	out, _ := runBlocking(t, dir, "play", "host", "--bind", "127.0.0.1", "--port", "0", "--resume", sv.ID)

	for _, want := range []string{sv.ID, "Alice", "Bea", "vertical", "Listening on 127.0.0.1:", "play join 127.0.0.1:"} {
		if !strings.Contains(out, want) {
			t.Errorf("the host never mentioned %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "your address") {
		t.Errorf("a host that bound one interface was told to work its own address out:\n%s", out)
	}

	// Continuing a game does not create another one.
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := store.List()
	if len(saved) != 1 {
		t.Fatalf("%d saved games after asking to continue one, want 1: %+v", len(saved), saved)
	}
	if saved[0].ID != sv.ID || saved[0].Player != sv.Player || saved[0].Opponent != sv.Opponent || saved[0].Side != sv.Side {
		t.Errorf("the stored row is now %+v, want the identifier and seats unchanged", saved[0])
	}
}

// TestPlayHostWildcardStillSaysWhichPartItKnows: the default is unchanged, and
// with no interface named there is no whole address to give out — so the line
// says which part the host knows rather than printing a wildcard to copy.
func TestPlayHostWildcardStillSaysWhichPartItKnows(t *testing.T) {
	for _, bound := range []string{"[::]:4270", "0.0.0.0:4270", ":4270"} {
		got := netplay.JoinTarget(bound)
		if !strings.Contains(got, "your address") || !strings.HasSuffix(got, ":4270") {
			t.Errorf("a wildcard bind on %q would be announced as %q", bound, got)
		}
	}
}

// TestPlayResumeRefusesWhatItCannotContinue: each of these has no connection
// to get back, and the command has to say so rather than start a fresh game
// under the same command line — which is what would lose the saved one.
func TestPlayResumeRefusesWhatItCannotContinue(t *testing.T) {
	dir := pnProfile(t, "Alice")
	remote := pnRemoteGame(t, dir)
	bot := pnSaved(t, dir, gamestore.Saved{
		Kind: gamestore.VersusBot, Player: "Alice", Side: "vertical",
		Opponent: leaderboard.BotName("pro"),
	})
	finished := pnSaved(t, dir, gamestore.Saved{
		Kind: gamestore.Remote, Player: "Alice", Side: "vertical",
		Opponent: leaderboard.RemoteName("Bea"), Finished: true,
	})
	theirs := pnSaved(t, dir, gamestore.Saved{
		Kind: gamestore.Remote, Player: "Bea", Side: "vertical",
		Opponent: leaderboard.RemoteName("Cleo"),
	})

	for _, c := range []struct {
		name string
		args []string
		says string
	}{
		{"a game that is not there", []string{"--resume", "nosuchgame"}, "nosuchgame"},
		{"a game against the computer", []string{"--resume", bot.ID}, "not one played over a connection"},
		{"a game that is over", []string{"--resume", finished.ID}, "is over"},
		{"another profile's game", []string{"--resume", theirs.ID}, "Bea"},
		{"terms the saved game already has", []string{"--resume", remote.ID, "--side", "horizontal"}, "--side cannot be given with --resume"},
		{"a size the saved game already has", []string{"--resume", remote.ID, "--size", "12"}, "--size cannot be given with --resume"},
	} {
		args := append([]string{"play", "host", "--bind", "127.0.0.1", "--port", "0"}, c.args...)
		out, err := run(t, dir, args...)
		if err == nil {
			t.Errorf("%s was accepted:\n%s", c.name, out)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s was refused with %q, which does not mention %q", c.name, err, c.says)
		}
		if strings.Contains(out, "Listening on") {
			t.Errorf("%s bound a socket before refusing:\n%s", c.name, out)
		}
	}
}

// TestPlayJoinResumeRefusesWhatItCannotContinue: the same refusals apply to the
// end that connects, which is the end a player is most likely to try first.
func TestPlayJoinResumeRefusesWhatItCannotContinue(t *testing.T) {
	dir := pnProfile(t, "Alice")
	bot := pnSaved(t, dir, gamestore.Saved{
		Kind: gamestore.VersusBot, Player: "Alice", Side: "vertical",
		Opponent: leaderboard.BotName("pro"),
	})

	out, err := run(t, dir, "play", "join", "--resume", bot.ID, "127.0.0.1:4270")
	if err == nil {
		t.Fatalf("joining continued a game against the computer:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not one played over a connection") {
		t.Errorf("the refusal reads %q", err)
	}
	if strings.Contains(out, "Connecting to") {
		t.Errorf("the joiner dialled before refusing:\n%s", out)
	}
}

// TestPlayHostRefusesAnInterfaceThisMachineDoesNotHold: --bind names one of
// this machine's own addresses, so a name that would be resolved somewhere is
// refused where it was typed.
func TestPlayHostRefusesAnInterfaceThisMachineDoesNotHold(t *testing.T) {
	dir := pnProfile(t, "Alice")
	for _, bind := range []string{"localhost", "example.com", "203.0.113.0.1"} {
		out, err := run(t, dir, "play", "host", "--bind", bind, "--port", "0")
		if err == nil {
			t.Errorf("--bind %q was accepted:\n%s", bind, out)
			continue
		}
		if !strings.Contains(err.Error(), "interface") {
			t.Errorf("--bind %q was refused with %q", bind, err)
		}
	}
}

// TestPlayJoinRefusesAMalformedCodeBeforeItPromisesToWait covers the reading
// that hid a typo: the code was echoed back and the next line said the joiner
// was waiting for the host, so a code that could never pair looked accepted.
func TestPlayJoinRefusesAMalformedCodeBeforeItPromisesToWait(t *testing.T) {
	dir := pnProfile(t, "Alice")
	relay := startTestRelay(t)

	for _, code := range []string{"ABCD", "not-a-code", "!!!!"} {
		out, err := run(t, dir, "play", "join", "--relay", relay, code)
		if err == nil {
			t.Errorf("the code %q was accepted:\n%s", code, out)
			continue
		}
		for _, mustNot := range []string{"Pairing code:", "Waiting for the host"} {
			if strings.Contains(out, mustNot) {
				t.Errorf("the code %q produced %q before it was refused:\n%s", code, mustNot, out)
			}
		}
	}

	// A code that can pair still gets the banner, so the check above is a
	// check on the code and not on the banner having been removed.
	out, _ := runBlocking(t, dir, "play", "join", "--relay", relay, netplay.PairingCode())
	if !strings.Contains(out, "Pairing code:") || !strings.Contains(out, "Waiting for the host") {
		t.Errorf("a code that can pair lost its banner:\n%s", out)
	}
}

// TestPlaySideErrorNamesEveryValueItTakes: random is one of the three the flag
// documents and accepts, and the refusal used to list only the other two.
func TestPlaySideErrorNamesEveryValueItTakes(t *testing.T) {
	dir := pnProfile(t, "Alice")
	out, err := run(t, dir, "play", "host", "--bind", "127.0.0.1", "--port", "0", "--side", "sideways")
	if err == nil {
		t.Fatalf("--side sideways was accepted:\n%s", out)
	}
	text := err.Error()
	for _, want := range []string{"vertical", "horizontal", "random"} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal %q does not name %q, which the flag accepts", text, want)
		}
	}
}

// TestPlayHostHelpDoesNotPromiseARelayCannotSeeTheGame: the relay's own
// documentation says its operator reads the whole game in plain text, and the
// help used to say the opposite.
func TestPlayHostHelpDoesNotPromiseARelayCannotSeeTheGame(t *testing.T) {
	out, err := run(t, t.TempDir(), "play", "host", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, mustNot := range []string{"never sees the game", "only passes bytes"} {
		if strings.Contains(out, mustNot) {
			t.Errorf("the help still claims %q:\n%s", mustNot, out)
		}
	}
	for _, want := range []string{"plain text", "--bind", "--resume"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %q:\n%s", want, out)
		}
	}
}
