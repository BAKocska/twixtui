package cli

import (
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// Correspondence play was reachable from the command line and dead on arrival:
// the identifier netplay minted was refused by the store the moment a game was
// created, so neither --new nor --join ever wrote anything. These tests are the
// command-line half of the fix; internal/app holds the two-player round trip.
//
// Nothing here opens the interface. The bare command and a --join by the side
// that moves first both do, and a full-screen program in a test is a hang, so
// the cases below are the ones that print and return.

// inviteIn pulls the invitation out of what --new printed. A player copies it
// off their terminal, so a test that reached into the store for it would not be
// testing the thing the player uses.
func inviteIn(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Fields(out) {
		if strings.HasPrefix(line, "TWXI-") {
			return line
		}
	}
	t.Fatalf("no invitation in the output:\n%s", out)
	return ""
}

// idIn pulls the game identifier out of what --new or --join printed.
func idIn(t *testing.T, out string) string {
	t.Helper()
	for _, prefix := range []string{"Started correspondence game ", "Joined "} {
		_, rest, ok := strings.Cut(out, prefix)
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, " ")
		return strings.TrimSuffix(id, ".")
	}
	t.Fatalf("no game identifier in the output:\n%s", out)
	return ""
}

// TestCorrespondenceNewPrintsAnInvitationAndStoresTheGame is the first of the
// three breaks: netplay mints an upper-case identifier and the store, which
// names a file after it, only takes lower case, so creating a game failed before
// anything was written.
func TestCorrespondenceNewPrintsAnInvitationAndStoresTheGame(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "--profile", "ada", "play", "correspondence", "--new", "--side", "vertical")
	if err != nil {
		t.Fatalf("starting a correspondence game: %v\n%s", err, out)
	}

	invite := inviteIn(t, out)
	decoded, err := netplay.DecodeInvite(invite)
	if err != nil {
		t.Fatalf("the printed invitation does not decode: %v", err)
	}
	if decoded.HostSide != game.Vertical {
		t.Errorf("the invitation puts the host on %s, want vertical", decoded.HostSide)
	}
	if decoded.HostName != "ada" {
		t.Errorf("the invitation names the host %q, want ada", decoded.HostName)
	}

	id := idIn(t, out)
	if id != strings.ToLower(decoded.ID) {
		t.Errorf("the game was announced as %q, want the invitation's %q lower-cased", id, decoded.ID)
	}
	if err := gamestore.ValidateID(id); err != nil {
		t.Errorf("the identifier shown to the player cannot name a stored game: %v", err)
	}

	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(id)
	if err != nil {
		t.Fatalf("the game was announced but not stored: %v", err)
	}
	if saved.Kind != gamestore.Correspondence {
		t.Errorf("the game was stored as a %s game", saved.Kind)
	}
	if saved.Side != game.Vertical.String() {
		t.Errorf("the game was stored with the host on %s", saved.Side)
	}
	if !strings.Contains(out, "You move first") {
		t.Errorf("the host plays vertical but was not told they move first:\n%s", out)
	}
}

// TestCorrespondenceJoinAcceptsTheInvitationOnce covers the acceptance and the
// mistake that follows it: pasting the same invitation twice must not leave two
// games, or two games at different positions under one identifier.
func TestCorrespondenceJoinAcceptsTheInvitationOnce(t *testing.T) {
	host, guest := t.TempDir(), t.TempDir()
	out, err := run(t, host, "--profile", "ada", "play", "correspondence", "--new", "--side", "vertical")
	if err != nil {
		t.Fatalf("starting the game: %v\n%s", err, out)
	}
	invite := inviteIn(t, out)

	joined, err := run(t, guest, "--profile", "linus", "play", "correspondence", "--join", invite)
	if err != nil {
		t.Fatalf("joining: %v\n%s", err, joined)
	}
	if !strings.Contains(joined, "against ada") {
		t.Errorf("the guest was not told who they are playing:\n%s", joined)
	}
	if !strings.Contains(joined, "You play horizontal") {
		t.Errorf("the guest was not given the side the host left:\n%s", joined)
	}
	if got, want := idIn(t, joined), idIn(t, out); got != want {
		t.Fatalf("the guest joined game %q, the host started %q", got, want)
	}

	again, err := run(t, guest, "--profile", "linus", "play", "correspondence", "--join", invite)
	if err == nil {
		t.Fatalf("the same invitation was accepted twice:\n%s", again)
	}
	if !strings.Contains(err.Error(), "already have game") {
		t.Errorf("the refusal reads %q, which does not say the game is already here", err)
	}

	store, err := gamestore.Open(guest)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.List(); len(got) != 1 {
		t.Fatalf("the guest has %d stored games after joining twice, want 1", len(got))
	}
}

// TestTheTwoEndsBindTheirCodesToTheSameIdentifier is the point of having two
// forms of the identifier at all. The store keeps the lower-cased one and the
// invitation carries netplay's own; if the two ends derived different ones from
// the same invitation, every code either sent would be refused as belonging to
// another game, and each end would still look perfectly consistent with itself.
func TestTheTwoEndsBindTheirCodesToTheSameIdentifier(t *testing.T) {
	host, guest := t.TempDir(), t.TempDir()
	out, err := run(t, host, "--profile", "ada", "play", "correspondence", "--new", "--side", "vertical")
	if err != nil {
		t.Fatalf("starting the game: %v\n%s", err, out)
	}
	invite := inviteIn(t, out)
	id := idIn(t, out)

	joined, err := run(t, guest, "--profile", "linus", "play", "correspondence", "--join", invite)
	if err != nil {
		t.Fatalf("joining: %v\n%s", err, joined)
	}

	// The host makes a move on their own copy and encodes it under the
	// identifier they were shown.
	hostGame := storedGame(t, host, id)
	if err := hostGame.PlacePeg(game.Point{Col: 1, Row: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := hostGame.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	code, err := netplay.EncodeLastMove(hostGame, id)
	if err != nil {
		t.Fatalf("encoding the host's move under %q: %v", id, err)
	}

	// The guest applies it under the identifier they derived for themselves.
	guestGame := storedGame(t, guest, idIn(t, joined))
	move, err := netplay.ApplyMove(guestGame, idIn(t, joined), code)
	if err != nil {
		t.Fatalf("the guest refused the host's code: %v", err)
	}
	if move != "B1" {
		t.Errorf("the guest played %q, want B1", move)
	}
	if got, want := netplay.PositionHash(guestGame), netplay.PositionHash(hostGame); got != want {
		t.Error("the two ends hold different positions after the first code")
	}
}

func storedGame(t *testing.T, dir, id string) *game.Game {
	t.Helper()
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(id)
	if err != nil {
		t.Fatalf("reading game %s from %s: %v", id, dir, err)
	}
	g, err := saved.Game()
	if err != nil {
		t.Fatalf("loading game %s: %v", id, err)
	}
	return g
}

// TestSeveralOpenGamesAreListedByTheNameTheyAreOpenedBy covers the case the bare
// command cannot decide: the listing has to carry the identifier the player then
// types, and a way to type it.
func TestSeveralOpenGamesAreListedByTheNameTheyAreOpenedBy(t *testing.T) {
	dir := t.TempDir()
	var ids []string
	for range 2 {
		out, err := run(t, dir, "--profile", "ada", "play", "correspondence", "--new", "--side", "vertical")
		if err != nil {
			t.Fatalf("starting a game: %v\n%s", err, out)
		}
		ids = append(ids, idIn(t, out))
	}

	listed, err := run(t, dir, "--profile", "ada", "play", "correspondence")
	if err != nil {
		t.Fatalf("listing: %v\n%s", err, listed)
	}
	for _, id := range ids {
		if !strings.Contains(listed, id) {
			t.Errorf("game %s is missing from the listing:\n%s", id, listed)
		}
	}
	if !strings.Contains(listed, "--game") {
		t.Errorf("the listing does not say how to open one of them:\n%s", listed)
	}
	if !strings.Contains(listed, "your move") {
		t.Errorf("the listing does not say which games are waiting for the player:\n%s", listed)
	}
	// The advice has to be true. game replay opens a replay, which is not a way
	// to play a correspondence turn, and the listing used to recommend it.
	if strings.Contains(listed, "game replay") {
		t.Errorf("the listing recommends the replay screen, which cannot play a turn:\n%s", listed)
	}
}

// TestOpeningANamedGameRefusesWhatItCannotPlay checks the flag that makes the
// listing above useful: an identifier that is not a correspondence game is
// refused by name rather than opened as one.
func TestOpeningANamedGameRefusesWhatItCannotPlay(t *testing.T) {
	dir := t.TempDir()
	store, err := gamestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := game.New(game.Std)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(gamestore.Saved{
		ID:       "hotseat1",
		Kind:     gamestore.Hotseat,
		Player:   "ada",
		Side:     game.Vertical.String(),
		Opponent: "linus",
		Record:   rec.Encode(),
	}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, dir, "--profile", "ada", "play", "correspondence", "--game", "hotseat1")
	if err == nil {
		t.Fatalf("a hotseat game was opened as a correspondence one:\n%s", out)
	}
	if !strings.Contains(err.Error(), "hotseat") {
		t.Errorf("the refusal reads %q, which does not say what kind of game it is", err)
	}

	if _, err := run(t, dir, "--profile", "ada", "play", "correspondence", "--game", "nosuchgame"); err == nil {
		t.Error("an identifier naming nothing was accepted")
	}
}

// TestNoCorrespondenceGameSaysHowToStartOne is the empty case, which is the
// first thing a new player meets.
func TestNoCorrespondenceGameSaysHowToStartOne(t *testing.T) {
	dir := t.TempDir()
	out, err := run(t, dir, "--profile", "ada", "play", "correspondence")
	if err == nil {
		t.Fatalf("opening nothing succeeded:\n%s", out)
	}
	for _, want := range []string{"--new", "--join"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %s", err, want)
		}
	}
}

// TestAGameWaitingOnTheOpponentIsNotWaitingOnYou pins the rule the bare command
// selects by. It is asserted on the rule itself because acting on it opens the
// interface, which a test cannot drive here.
func TestAGameWaitingOnTheOpponentIsNotWaitingOnYou(t *testing.T) {
	rs := game.Std
	rs.Size = 6
	g, err := game.New(rs)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	fresh := gamestore.Saved{
		ID:       "fresh",
		Kind:     gamestore.Correspondence,
		Player:   "ada",
		Side:     game.Vertical.String(),
		Opponent: "linus",
		Record:   rec.Encode(),
	}
	if !toMove(fresh) {
		t.Error("a fresh game with the player on vertical is not waiting for them")
	}

	theirs := fresh
	theirs.Side = game.Horizontal.String()
	if toMove(theirs) {
		t.Error("a fresh game with the player on horizontal is waiting for them")
	}

	if err := g.PlacePeg(game.Point{Col: 1, Row: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitTurn(); err != nil {
		t.Fatal(err)
	}
	played, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	moved := fresh
	moved.Record = played.Encode()
	if toMove(moved) {
		t.Error("a game the player has just moved in is still said to be waiting for them")
	}

	damaged := fresh
	damaged.Record = "not a record"
	if toMove(damaged) {
		t.Error("a game that will not load is offered as one to play")
	}
}
