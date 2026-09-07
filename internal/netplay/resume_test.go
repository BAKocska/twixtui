package netplay

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/BAKocska/twixtui/internal/game"
)

// recordedGame plays entries into a fresh game and returns it, which is what a
// stored record decodes to: a position with a history and no session behind it.
func recordedGame(t *testing.T, moves []Entry) *game.Game {
	t.Helper()
	g, err := replay(testRules(), moves)
	if err != nil {
		t.Fatalf("replaying %d entries: %v", len(moves), err)
	}
	return g
}

// TestSnapshotForKeepsTheActorOfEveryEntry is the property a resume from a
// record stands on. A resignation and the two draw messages may be made by the
// side that is not to move, so counting sides off the move order attributes
// them to the wrong player — and the far end refuses an entry replayed as its
// own side, so the game would not come back at all.
func TestSnapshotForKeepsTheActorOfEveryEntry(t *testing.T) {
	g := recordedGame(t, scriptedGame[:4])
	// Horizontal offers a draw while vertical is to move, and vertical then
	// plays on. The offer is horizontal's, in the middle of vertical's turn.
	if err := g.OfferDraw(game.Horizontal); err != nil {
		t.Fatalf("offering a draw out of turn: %v", err)
	}
	if err := applyEntry(g, game.Vertical, "D5"); err != nil {
		t.Fatalf("playing on after the offer: %v", err)
	}

	snap, err := SnapshotFor(g, Guest, game.Horizontal, "grace", "ada")
	if err != nil {
		t.Fatalf("SnapshotFor: %v", err)
	}
	if got := len(snap.Moves); got != 6 {
		t.Fatalf("the snapshot holds %d entries, want the record's 6: %+v", got, snap.Moves)
	}
	offer := snap.Moves[4]
	if offer.Side != game.Horizontal {
		t.Errorf("the draw offer is attributed to %s, but horizontal made it: %+v", offer.Side, offer)
	}
	if !strings.Contains(offer.Move, "draw") {
		t.Errorf("entry 5 is %q, want the draw offer", offer.Move)
	}
	if last := snap.Moves[5]; last.Side != game.Vertical || last.Move != "D5" {
		t.Errorf("entry 6 is %+v, want vertical's D5", last)
	}
	if snap.Role != Guest || snap.Side != game.Horizontal || snap.Name != "grace" || snap.Opponent != "ada" {
		t.Errorf("the snapshot describes %+v, want the guest playing horizontal as grace against ada", snap)
	}

	// The transcript has to rebuild the position it was read from, since that
	// is the whole of what the two ends compare. The offer no longer stands in
	// either of them: vertical played on, and a move by the side an offer was
	// made to withdraws it. That is the engine's rule, and the point here is
	// that the transcript reproduces it rather than a state of its own.
	rebuilt, err := replay(snap.Rules, snap.Moves)
	if err != nil {
		t.Fatalf("replaying the snapshot: %v", err)
	}
	if got, want := PositionHash(rebuilt), PositionHash(g); got != want {
		t.Errorf("the snapshot replays to %s, the record is %s", shortHash(got), shortHash(want))
	}
	if got := rebuilt.DrawOfferedBy(); got != game.NoPlayer {
		t.Errorf("the replayed game still has an offer standing for %s, and vertical's D5 withdrew it", got)
	}

	// A standing offer is the case where the actor is all that distinguishes
	// two positions: horizontal's offer and vertical's are different
	// positions, and the bare notation the engine also accepts would attribute
	// this one to whoever was to move.
	standing := recordedGame(t, scriptedGame[:4])
	if err := standing.OfferDraw(game.Horizontal); err != nil {
		t.Fatalf("offering a draw out of turn: %v", err)
	}
	held, err := SnapshotFor(standing, Host, game.Vertical, "ada", "grace")
	if err != nil {
		t.Fatalf("SnapshotFor with an offer standing: %v", err)
	}
	stood, err := replay(held.Rules, held.Moves)
	if err != nil {
		t.Fatalf("replaying a standing offer: %v", err)
	}
	if got := stood.DrawOfferedBy(); got != game.Horizontal {
		t.Errorf("the offer came back standing for %s, want horizontal", got)
	}
	if got, want := PositionHash(stood), PositionHash(standing); got != want {
		t.Errorf("a standing offer replays to %s, the record is %s", shortHash(got), shortHash(want))
	}
}

// TestSnapshotForRefusesASideItCannotPlay: a stored side that is neither of
// the two is refused here, where the game has not been offered to anybody yet,
// rather than at a handshake the opponent is waiting on.
func TestSnapshotForRefusesASideItCannotPlay(t *testing.T) {
	g := recordedGame(t, scriptedGame[:2])
	if _, err := SnapshotFor(g, Host, game.NoPlayer, "ada", "grace"); err == nil {
		t.Fatal("a snapshot was built with no side to play")
	}
}

// TestSnapshotForCarriesASwap covers the one entry that changes hands as well
// as the board. The engine writes it as a bare "swap" with no side in it, so
// the side can only come from the record, and a game resumed after one has to
// come back with the swap in the same place — anything else is a different
// game, and the two ends would find out by refusing each other.
func TestSnapshotForCarriesASwap(t *testing.T) {
	swapped := []Entry{
		{Side: game.Vertical, Move: "B1"},
		{Side: game.Horizontal, Move: "swap"},
	}
	g := recordedGame(t, swapped)

	snap, err := SnapshotFor(g, Host, game.Vertical, "ada", "grace")
	if err != nil {
		t.Fatalf("SnapshotFor after a swap: %v", err)
	}
	if len(snap.Moves) != len(swapped) {
		t.Fatalf("the snapshot holds %+v, want %+v", snap.Moves, swapped)
	}
	for i, want := range swapped {
		if snap.Moves[i] != want {
			t.Errorf("entry %d is %+v, want %+v", i+1, snap.Moves[i], want)
		}
	}
	if got, want := transcriptDigest(snap.Moves), transcriptDigest(swapped); got != want {
		t.Errorf("the transcript digests to %s, want %s", shortHash(got), shortHash(want))
	}
}

// TestResumeFromRecordsReplaysTheMissingEntry is the resume the product
// actually performs: neither end has a session left, both rebuild their
// snapshot from the record they saved, and the end that is behind is caught up
// by the protocol. TestDisconnectThenResync proves the same for snapshots
// carried over from a live session; this proves it for the stored record,
// which is all a reconnection a day later has.
func TestResumeFromRecordsReplaysTheMissingEntry(t *testing.T) {
	// The host committed the fifth entry; the guest never saw it.
	hostSnap, err := SnapshotFor(recordedGame(t, scriptedGame[:5]), Host, game.Vertical, "ada", "grace")
	if err != nil {
		t.Fatalf("the host's snapshot: %v", err)
	}
	guestSnap, err := SnapshotFor(recordedGame(t, scriptedGame[:4]), Guest, game.Horizontal, "grace", "ada")
	if err != nil {
		t.Fatalf("the guest's snapshot: %v", err)
	}

	hc, gc := net.Pipe()
	t.Cleanup(func() { _ = hc.Close(); _ = gc.Close() })
	hopts := hostOpts()
	hopts.Resume = &hostSnap
	gopts := guestOpts()
	gopts.Rules = testRules()
	gopts.Side = game.Horizontal
	gopts.Resume = &guestSnap
	host, guest, hostErr, guestErr := connectOver(t, hc, gc, hopts, gopts)
	if hostErr != nil || guestErr != nil {
		t.Fatalf("resuming from records failed: host %v, guest %v", hostErr, guestErr)
	}
	t.Cleanup(func() { _ = host.Close(); _ = guest.Close() })

	wantEvent(t, host, EventConnected)
	wantEvent(t, guest, EventConnected)
	replayed := wantEvent(t, guest, EventMove)
	if replayed.Move != "D5" || !strings.Contains(replayed.Text, "replayed") {
		t.Fatalf("the guest was caught up with %+v, want D5 replayed", replayed)
	}
	assertAgree(t, host, guest, 5)

	// The seats are the stored ones, not fresh ones: the host still plays the
	// side its record says, and each end still knows the other's name.
	if host.Side() != game.Vertical || guest.Side() != game.Horizontal {
		t.Errorf("the resumed sides are host %s and guest %s", host.Side(), guest.Side())
	}
	if host.OpponentName() != "grace" || guest.OpponentName() != "ada" {
		t.Errorf("the resumed opponents are %q and %q", host.OpponentName(), guest.OpponentName())
	}

	// And play carries on from where it stopped rather than from the start.
	playScriptFrom(t, host, guest, scriptedGame[5:], 5)
}

// TestResumeFromRecordsRefusesAnotherGame covers the mismatch a player will
// actually hit: the right saved game on one side and the wrong one on the
// other. There is no correct answer to choose, so both ends refuse rather than
// one adopting the other's record.
func TestResumeFromRecordsRefusesAnotherGame(t *testing.T) {
	hostSnap, err := SnapshotFor(recordedGame(t, scriptedGame[:4]), Host, game.Vertical, "ada", "grace")
	if err != nil {
		t.Fatal(err)
	}
	// A game of the same length whose second entry differs, which is what two
	// different games look like at the handshake.
	other := []Entry{
		{Side: game.Vertical, Move: "B1"},
		{Side: game.Horizontal, Move: "A3"},
		{Side: game.Vertical, Move: "C3"},
		{Side: game.Horizontal, Move: "F2"},
	}
	guestSnap, err := SnapshotFor(recordedGame(t, other), Guest, game.Horizontal, "grace", "ada")
	if err != nil {
		t.Fatal(err)
	}

	hc, gc := net.Pipe()
	t.Cleanup(func() { _ = hc.Close(); _ = gc.Close() })
	hopts := hostOpts()
	hopts.Resume = &hostSnap
	gopts := guestOpts()
	gopts.Resume = &guestSnap
	host, guest, hostErr, guestErr := connectOver(t, hc, gc, hopts, gopts)
	if host != nil {
		_ = host.Close()
	}
	if guest != nil {
		_ = guest.Close()
	}
	if hostErr == nil && guestErr == nil {
		t.Fatal("two different games were resumed as one")
	}
	if err := errors.Join(hostErr, guestErr); !errors.Is(err, ErrDiverged) {
		t.Fatalf("the refusal was %v, want a divergence", err)
	}
}

// TestResumeRefusesToPairWithANewGame covers the other half of the mismatch:
// one end continuing a saved game and the other starting a fresh one. Playing
// it as a new game would silently discard the saved one.
func TestResumeRefusesToPairWithANewGame(t *testing.T) {
	hostSnap, err := SnapshotFor(recordedGame(t, scriptedGame[:4]), Host, game.Vertical, "ada", "grace")
	if err != nil {
		t.Fatal(err)
	}
	hc, gc := net.Pipe()
	t.Cleanup(func() { _ = hc.Close(); _ = gc.Close() })
	hopts := hostOpts()
	hopts.Resume = &hostSnap
	host, guest, hostErr, guestErr := connectOver(t, hc, gc, hopts, guestOpts())
	if host != nil {
		_ = host.Close()
	}
	if guest != nil {
		_ = guest.Close()
	}
	if hostErr == nil && guestErr == nil {
		t.Fatal("a saved game paired with a new one")
	}
	if err := errors.Join(hostErr, guestErr); !errors.Is(err, ErrDiverged) {
		t.Fatalf("the refusal was %v, want a divergence", err)
	}
}

// TestResumeRefusesTheWrongSide: the side is the saved game's, so a host
// offering the other one is refused rather than swapping the players over.
func TestResumeRefusesTheWrongSide(t *testing.T) {
	// The guest saved itself on horizontal; this host takes horizontal too, so
	// it would hand the guest vertical.
	guestSnap, err := SnapshotFor(recordedGame(t, nil), Guest, game.Horizontal, "grace", "ada")
	if err != nil {
		t.Fatal(err)
	}
	hc, gc := net.Pipe()
	t.Cleanup(func() { _ = hc.Close(); _ = gc.Close() })
	hopts := hostOpts()
	hopts.Side = game.Horizontal
	gopts := guestOpts()
	gopts.Resume = &guestSnap
	host, guest, _, guestErr := connectOver(t, hc, gc, hopts, gopts)
	if host != nil {
		_ = host.Close()
	}
	if guest != nil {
		_ = guest.Close()
	}
	if guestErr == nil {
		t.Fatal("the guest accepted a side its saved game does not play")
	}
	if got := guestErr.Error(); !strings.Contains(got, "horizontal") {
		t.Errorf("the refusal does not name the side it expected: %v", guestErr)
	}
}
