package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
)

// rmSave writes an unfinished network game with the given entries already
// played, which is what a connection dropping leaves behind.
func rmSave(t *testing.T, d Deps, player, opponent string, holes ...string) gamestore.Saved {
	t.Helper()
	rs := game.Std
	rs.Size = 8
	g := game.MustNew(rs)
	for _, h := range holes {
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
	sv := gamestore.Saved{
		ID:       gamestore.NewID(),
		Kind:     gamestore.Remote,
		Created:  time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		Player:   player,
		Side:     "vertical",
		Opponent: leaderboard.RemoteName(opponent),
		Record:   rec.Encode(),
	}
	if err := d.Games.Put(sv); err != nil {
		t.Fatal(err)
	}
	return sv
}

// TestPrepareRemoteResumeRefusesWhatCannotBeContinued: every one of these has
// no connection to get back, and the refusal has to arrive instead of a fresh
// game, which would lose the game the player asked for.
func TestPrepareRemoteResumeRefusesWhatCannotBeContinued(t *testing.T) {
	d := shellTestDeps(t)
	base := rmSave(t, d, "Balint", "Zsofia", "B1", "A2")

	for _, c := range []struct {
		name  string
		alter func(sv *gamestore.Saved)
		as    string
		says  string
	}{
		{"another kind of game", func(sv *gamestore.Saved) { sv.Kind = gamestore.VersusBot }, "Balint", "not one played over a connection"},
		{"a finished game", func(sv *gamestore.Saved) { sv.Finished = true }, "Balint", "is over"},
		{"another profile's game", func(sv *gamestore.Saved) {}, "Zsofia", "Balint"},
		{"an opponent on this machine", func(sv *gamestore.Saved) { sv.Opponent = "Zsofia" }, "Balint", "another machine"},
		{"an unreadable side", func(sv *gamestore.Saved) { sv.Side = "sideways" }, "Balint", "unreadable side"},
		{"no identifier", func(sv *gamestore.Saved) { sv.ID = "" }, "Balint", "no identifier"},
	} {
		sv := base
		c.alter(&sv)
		res, err := PrepareRemoteResume(sv, c.as)
		if err == nil {
			t.Errorf("%s was prepared for a connection as %+v", c.name, res.Snapshot)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s was refused with %q, which does not mention %q", c.name, err, c.says)
		}
	}
}

// TestPrepareRemoteResumeKeepsTheStoredGame is the identity a reconnection has
// to preserve: the same row, the same seats, the same transcript. A continued
// game that came back as a new one beside the saved one, or with the players
// moved around, would be the defect this path exists to fix.
func TestPrepareRemoteResumeKeepsTheStoredGame(t *testing.T) {
	d := shellTestDeps(t)
	sv := rmSave(t, d, "Balint", "Zsofia", "B1", "A2", "C3")

	res, err := PrepareRemoteResume(sv, "Balint")
	if err != nil {
		t.Fatalf("preparing the stored game: %v", err)
	}
	if res.Saved.ID != sv.ID {
		t.Errorf("prepared %q, want the stored %q", res.Saved.ID, sv.ID)
	}
	if res.Snapshot.Side != game.Vertical || res.Snapshot.Name != "Balint" {
		t.Errorf("the snapshot seats %q on %s, want Balint on vertical", res.Snapshot.Name, res.Snapshot.Side)
	}
	if res.Snapshot.Opponent != "Zsofia" {
		t.Errorf("the opponent is %q, want the bare stored name", res.Snapshot.Opponent)
	}
	if got := len(res.Snapshot.Moves); got != 3 {
		t.Fatalf("the transcript holds %d entries, want the record's 3: %+v", got, res.Snapshot.Moves)
	}
	if first := res.Snapshot.Moves[0]; first.Side != game.Vertical || first.Move != "B1" {
		t.Errorf("entry 1 is %+v, want vertical's B1", first)
	}
	if second := res.Snapshot.Moves[1]; second.Side != game.Horizontal {
		t.Errorf("entry 2 is attributed to %s, want horizontal", second.Side)
	}

	// The role is not stored and not guessed: each way of making the
	// connection again says which end this machine is.
	if got := res.Host().Resume.Role; got != netplay.Host {
		t.Errorf("hosting the reconnection resumes as %s", got)
	}
	if got := res.Guest().Resume.Role; got != netplay.Guest {
		t.Errorf("joining the reconnection resumes as %s", got)
	}
	if opts := res.Guest(); opts.Rules != res.Snapshot.Rules || opts.Side != game.Vertical {
		t.Errorf("joining does not insist on the saved terms: %+v", opts)
	}

	// The opponent may reconnect under a different name; the row keeps the
	// name the result will be recorded against, and the connection is what
	// the panel reads a current name from.
	session := newGSFakeSession(game.Vertical, res.Snapshot.Rules)
	cfg := res.Continue(session)
	if cfg.StoreID != sv.ID {
		t.Errorf("the continued game is bound to %q, want the stored %q", cfg.StoreID, sv.ID)
	}
	if cfg.Resume == nil || cfg.Resume.ID != sv.ID {
		t.Errorf("the continued game does not carry the stored row: %+v", cfg.Resume)
	}
	if cfg.Kind != gamestore.Remote || cfg.Session == nil {
		t.Errorf("the continued game is a %s with session=%v", cfg.Kind, cfg.Session != nil)
	}
	if seat := cfg.Seats[game.Vertical]; !seat.Human() || seat.Profile != "Balint" {
		t.Errorf("the local seat is %+v, want Balint at this keyboard", seat)
	}
	seat := cfg.Seats[game.Horizontal]
	if !seat.Remote {
		t.Fatalf("the opponent's seat is %+v, want a remote one", seat)
	}
	if seat.Label != "Zsofia" {
		t.Errorf("the opponent's seat is labelled %q, want the stored %q even though the connection says %q",
			seat.Label, "Zsofia", session.OpponentName())
	}
	if got := cfg.Opponent(game.Vertical); got != sv.Opponent {
		t.Errorf("the continued game would be recorded against %q, want the stored %q", got, sv.Opponent)
	}
}

// rmListen stands up the opponent's end of a stored game on loopback and
// returns the address to connect to. It resumes from the same record, which is
// what the other player's machine has.
func rmListen(t *testing.T, sv gamestore.Saved, side game.Player, name, opponent string) string {
	t.Helper()
	g, err := sv.Game()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := netplay.SnapshotFor(g, netplay.Host, side, name, opponent)
	if err != nil {
		t.Fatalf("the opponent's snapshot: %v", err)
	}
	l, err := netplay.Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding the opponent's end: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan netplay.Session, 1)
	go func() {
		s, err := l.Wait(ctx, netplay.HostOptions{
			Name: name, Rules: g.Rules(), Side: side, Resume: &snap,
			Tuning: netplay.Tuning{Keepalive: 200 * time.Millisecond, DeadAfter: 5 * time.Second, HandshakeTimeout: 5 * time.Second},
		})
		if err != nil {
			l.Close()
			done <- nil
			return
		}
		done <- s
	}()
	t.Cleanup(func() {
		cancel()
		if s := <-done; s != nil {
			s.Close()
		}
		l.Close()
	})
	return l.Addr()
}

// TestMenuContinuesANetworkGameThroughTheContinueList walks the keypresses a
// player actually makes: the saved network game is offered rather than greyed
// out, choosing it asks how to reconnect, and the connection that results
// opens the stored game — same identifier, same seats, no second row.
func TestMenuContinuesANetworkGameThroughTheContinueList(t *testing.T) {
	d := shellTestDeps(t)
	sv := rmSave(t, d, "Balint", "Zsofia", "B1", "A2")
	addr := rmListen(t, sv, game.Horizontal, "Zsofia", "Balint")

	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Continue a saved game")
	c := mnChooser(t, m)
	if len(c.opts) != 1 {
		t.Fatalf("%d rows in the continue list, want the saved network game", len(c.opts))
	}
	if c.opts[0].disabled {
		t.Fatal("the saved network game is greyed out, so there is no way to reconnect to it")
	}

	mnPick(t, m, "Balint vs")
	if got := mnChooser(t, m).title; got != "How do you want to reconnect?" {
		t.Fatalf("choosing the game asked %q", got)
	}
	if !strings.Contains(m.message, sv.ID) {
		t.Errorf("the reconnection form does not name the game being continued: %q", m.message)
	}

	mnPick(t, m, "connect to their address")
	mnTypeInto(t, m, addr)
	cmd := shellSend(t, m, "enter")
	wait, ok := m.form.(*waitForm)
	if !ok {
		t.Fatalf("after the address the form is %T, want the connection", m.form)
	}
	if joined := strings.Join(wait.info, "\n"); !strings.Contains(joined, sv.ID) {
		t.Errorf("the wait does not say which game is being continued: %q", joined)
	}
	if cmd == nil {
		t.Fatal("the address did not start a connection")
	}
	msg, ok := cmd().(menuSessionMsg)
	if !ok {
		t.Fatalf("connecting produced %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("reconnecting to the stored game failed: %v", msg.err)
	}
	t.Cleanup(func() { msg.session.Close() })

	_, next := m.Update(msg)
	cfg := mnStartedConfig(t, next)
	if cfg.StoreID != sv.ID {
		t.Errorf("the board opened as %q, want the stored game %q", cfg.StoreID, sv.ID)
	}
	if cfg.Kind != gamestore.Remote || cfg.Session == nil {
		t.Errorf("the board opened as a %s with session=%v", cfg.Kind, cfg.Session != nil)
	}
	if seat := cfg.Seats[game.Vertical]; !seat.Human() || seat.Profile != "Balint" {
		t.Errorf("the local seat is %+v, want Balint on the stored side", seat)
	}
	if seat := cfg.Seats[game.Horizontal]; !seat.Remote || seat.Label != "Zsofia" {
		t.Errorf("the opponent's seat is %+v, want the stored Zsofia", seat)
	}
	if got := cfg.Session.Side(); got != game.Vertical {
		t.Errorf("the connection plays %s, want the stored vertical", got)
	}

	// The stored game was continued, not copied: one row, still the same one.
	saved := d.Games.List()
	if len(saved) != 1 {
		t.Fatalf("%d saved games after reconnecting, want the one that was continued", len(saved))
	}
	if saved[0].ID != sv.ID || saved[0].Player != sv.Player || saved[0].Opponent != sv.Opponent || saved[0].Side != sv.Side {
		t.Errorf("the stored row is now %+v, want the seats and identifier unchanged", saved[0])
	}
}

// TestMenuRefusesToContinueAnotherProfilesNetworkGame: the row is offered — it
// is on this machine — and the refusal arrives on the list it was chosen from
// rather than after a wait for a connection that would play somebody else's
// game and record the result against them.
func TestMenuRefusesToContinueAnotherProfilesNetworkGame(t *testing.T) {
	d := shellTestDeps(t)
	rmSave(t, d, "Zsofia", "Reka", "B1")

	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Continue a saved game")
	if cmd := mnPick(t, m, "Zsofia vs"); cmd != nil {
		t.Fatalf("choosing another profile's game produced %T", cmd())
	}
	if _, still := m.form.(*chooser); !still {
		t.Fatalf("the refusal replaced the list with %T", m.form)
	}
	if !strings.Contains(m.message, "Zsofia") || !strings.Contains(m.message, "Balint") {
		t.Errorf("the refusal does not say whose game it is: %q", m.message)
	}
	if !strings.Contains(m.View().Content, "Zsofia") {
		t.Errorf("the refusal is not on screen:\n%s", m.View().Content)
	}
}

// TestMenuHostListensWhereItWasAsked covers --bind's counterpart in the
// interface: a host who does not want to be reachable from the rest of the
// network can say so, and what the opponent is told is then the whole address
// rather than a placeholder.
func TestMenuHostListensWhereItWasAsked(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play")
	mnPick(t, m, "someone over the network")
	mnPick(t, m, "wait for them to connect to me")
	mnPick(t, m, "vertical")
	mnPick(t, m, "std")
	mnPick(t, m, "12x12")

	form, ok := m.form.(*textForm)
	if !ok {
		t.Fatalf("after the terms the form is %T, want the address to listen on", m.form)
	}
	if !strings.Contains(form.note, "127.0.0.1") {
		t.Errorf("the field does not say how to listen on this machine only: %q", form.note)
	}

	// An address this machine does not hold is refused beside the field.
	mnTypeInto(t, m, "localhost")
	shellSend(t, m, "enter")
	if _, still := m.form.(*textForm); !still {
		t.Fatalf("a name that is not an interface was accepted, leaving %T", m.form)
	}
	if !strings.Contains(m.message, "interface") {
		t.Errorf("the refusal reads %q", m.message)
	}
	for range len("localhost") {
		shellSend(t, m, "backspace")
	}

	mnTypeInto(t, m, "127.0.0.1:0")
	cmd := shellSend(t, m, "enter")
	wait, ok := m.form.(*waitForm)
	if !ok {
		t.Fatalf("after the address the form is %T, want the wait: %q", m.form, m.message)
	}
	joined := strings.Join(wait.info, "\n")
	if !strings.Contains(joined, "127.0.0.1:") {
		t.Errorf("the host is not told the address it bound: %q", joined)
	}
	if strings.Contains(joined, "your address") {
		t.Errorf("a host that bound one interface was still told to work its own address out: %q", joined)
	}

	// Run the wait so that giving up closes the listening socket, as it does
	// for a player who presses escape.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	shellSend(t, m, "esc")
	msg, ok := (<-done).(menuSessionMsg)
	if !ok {
		t.Fatal("giving up did not produce the outcome of the wait")
	}
	if msg.session != nil {
		msg.session.Close()
		t.Fatal("somebody connected to a listener nobody was told about")
	}
}

// TestConnectionLabelGoesWithTheConnection covers the panel line naming the
// opponent this end is connected to. It survived the disconnection notice, so
// the panel said the connection was up directly under a notice saying it had
// dropped.
func TestConnectionLabelGoesWithTheConnection(t *testing.T) {
	host, guest := gsPipePair(t, gsRules(6), game.Vertical)
	go func() {
		for range guest.Events() {
		}
	}()

	d := gsTestDeps(t)
	h := newGSHarness(t, d, gsRemoteConfig(6, host, game.Vertical), 80, 24)
	h.waitFor("the connection to be reported", func() bool { return h.s.netNote != "" })
	if !strings.Contains(h.frame(), "connected") {
		t.Fatalf("the panel never said the connection was up:\n%s", h.frame())
	}

	if err := guest.Close(); err != nil {
		t.Fatalf("closing the opponent's end: %v", err)
	}
	h.waitFor("the connection to be reported gone", func() bool { return h.s.stopped })
	if h.s.netNote != "" {
		t.Errorf("the panel still says %q after the connection dropped", h.s.netNote)
	}
	if frame := h.frame(); strings.Contains(frame, "connected: ") {
		t.Errorf("the panel still claims a live connection:\n%s", frame)
	}
}
