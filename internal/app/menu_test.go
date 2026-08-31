package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/theme"
)

func mnMenu(t *testing.T, d Deps, width, height int) *Menu {
	t.Helper()
	if _, ok := d.Profiles.Get("Balint"); !ok {
		if _, err := d.Profiles.Create("Balint"); err != nil {
			t.Fatal(err)
		}
	}
	m := NewMenu(d, "Balint")
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// mnChooser returns the list that currently has focus, which is the menu itself
// when no form is open.
func mnChooser(t *testing.T, m *Menu) *chooser {
	t.Helper()
	if m.form == nil {
		return m.list
	}
	c, ok := m.form.(*chooser)
	if !ok {
		t.Fatalf("the open form is %T, not a list of choices", m.form)
	}
	return c
}

// mnPick moves the selection onto the option whose label starts with prefix and
// chooses it, returning whatever command that produced. The selection is moved
// with real keypresses, so this also proves the entry is reachable.
func mnPick(t *testing.T, m *Menu, prefix string) tea.Cmd {
	t.Helper()
	c := mnChooser(t, m)
	want := -1
	for i, o := range c.opts {
		if strings.HasPrefix(o.label, prefix) {
			want = i
			break
		}
	}
	if want < 0 {
		labels := make([]string, 0, len(c.opts))
		for _, o := range c.opts {
			labels = append(labels, o.label)
		}
		t.Fatalf("no option starting with %q in %q; have %v", prefix, c.title, labels)
	}
	for range len(c.opts) {
		if c.sel == want {
			break
		}
		shellSend(t, m, "down")
	}
	if c.sel != want {
		t.Fatalf("could not move the selection to %q with the down key", prefix)
	}
	return shellSend(t, m, "enter")
}

// mnStartedConfig unwraps the command a finished form produced and returns the
// configuration the game screen was really built with. Reading it back from the
// screen rather than reassembling it here is what makes this an assertion about
// the keypresses instead of about a copy.
func mnStartedConfig(t *testing.T, cmd tea.Cmd) GameConfig {
	t.Helper()
	if cmd == nil {
		t.Fatal("finishing the form produced no command")
	}
	msg := cmd()
	done, ok := msg.(DoneMsg)
	if !ok {
		t.Fatalf("finishing the form produced %T, want a DoneMsg", msg)
	}
	if done.Err != nil {
		t.Fatalf("starting the game failed: %v", done.Err)
	}
	gs, ok := done.Next.(*gameScreen)
	if !ok {
		t.Fatalf("the menu handed over to %T, want the game screen", done.Next)
	}
	return gs.cfg
}

// mnSaveGame writes an unfinished game to the store.
func mnSaveGame(t *testing.T, d Deps, kind gamestore.Kind, player, opponent string) gamestore.Saved {
	t.Helper()
	rs := game.Std
	rs.Size = 12
	g := game.MustNew(rs)
	rec, err := g.Record()
	if err != nil {
		t.Fatal(err)
	}
	sv := gamestore.Saved{
		ID:       gamestore.NewID(),
		Kind:     kind,
		Created:  time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		Player:   player,
		Side:     "vertical",
		Opponent: opponent,
		Record:   rec.Encode(),
	}
	if err := d.Games.Put(sv); err != nil {
		t.Fatal(err)
	}
	return sv
}

// TestMenuEveryEntryShowsItsExplanation walks the whole list with the arrow key
// and requires the highlighted entry's own sentence to be on screen.
func TestMenuEveryEntryShowsItsExplanation(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 120, 40)
	entries := m.list.opts
	if got := len(entries); got != 9 {
		t.Errorf("%d entries, want the nine the menu is specified to offer", got)
	}

	for i := range entries {
		if m.list.sel != i {
			shellSend(t, m, "down")
		}
		if m.list.sel != i {
			t.Fatalf("down moved the selection to %d, want %d", m.list.sel, i)
		}
		frame := m.View().Content
		if !strings.Contains(frame, entries[i].label) {
			t.Errorf("entry %q is not on screen:\n%s", entries[i].label, frame)
		}
		if !strings.Contains(frame, entries[i].help) {
			t.Errorf("entry %q does not show its explanation %q:\n%s", entries[i].label, entries[i].help, frame)
		}
	}
}

// TestMenuEveryEntryIsReachable chooses each entry in turn and requires
// something to actually happen: a form to open, or a command to be produced.
func TestMenuEveryEntryIsReachable(t *testing.T) {
	cases := []struct {
		prefix string
		check  func(t *testing.T, m *Menu, cmd tea.Cmd)
	}{
		{"Play the computer", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if got := mnChooser(t, m).title; got != "How strong an opponent?" {
				t.Errorf("opened %q", got)
			}
		}},
		{"Play someone at this keyboard", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if _, ok := m.form.(*pickerForm); !ok {
				t.Errorf("opened %T, want the profile picker for the second player", m.form)
			}
		}},
		{"Play someone over the network", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if got := mnChooser(t, m).title; got != "How do you want to connect?" {
				t.Errorf("opened %q", got)
			}
		}},
		{"Continue a saved game", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if got := mnChooser(t, m).title; got != "Continue a saved game" {
				t.Errorf("opened %q", got)
			}
		}},
		{"Learn to play", func(t *testing.T, _ *Menu, cmd tea.Cmd) {
			if cmd == nil {
				t.Fatal("no command")
			}
			done, ok := cmd().(DoneMsg)
			if !ok || done.Next == nil {
				t.Fatalf("produced %T without a next screen", cmd())
			}
		}},
		{"Leaderboard", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if _, ok := m.form.(*scrollForm); !ok {
				t.Errorf("opened %T, want a scrolling panel", m.form)
			}
		}},
		{"Colours", func(t *testing.T, m *Menu, _ tea.Cmd) {
			if got := mnChooser(t, m).title; got != "Colours" {
				t.Errorf("opened %q", got)
			}
		}},
		{"Switch profile", func(t *testing.T, _ *Menu, cmd tea.Cmd) {
			if cmd == nil {
				t.Fatal("no command")
			}
			done, _ := cmd().(DoneMsg)
			if _, ok := done.Next.(*Picker); !ok {
				t.Errorf("handed over to %T, want the profile picker", done.Next)
			}
		}},
		{"Quit", func(t *testing.T, _ *Menu, cmd tea.Cmd) {
			if cmd == nil {
				t.Fatal("no command")
			}
			done, _ := cmd().(DoneMsg)
			if !done.Quit {
				t.Errorf("produced %+v, want a quit", done)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			d := shellTestDeps(t)
			mnSaveGame(t, d, gamestore.VersusBot, "Balint", leaderboard.BotName("beginner"))
			m := mnMenu(t, d, 100, 30)
			cmd := mnPick(t, m, tc.prefix)
			tc.check(t, m, cmd)
		})
	}
}

// TestMenuBotGameCollectsEveryChoice drives the whole form with keys and checks
// the configuration the game was actually started with, including R7's side
// choice and R15's hint engine.
func TestMenuBotGameCollectsEveryChoice(t *testing.T) {
	for _, tc := range []struct {
		side game.Player
		name string
	}{
		{game.Vertical, "vertical"},
		{game.Horizontal, "horizontal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := shellTestDeps(t)
			m := mnMenu(t, d, 100, 30)

			mnPick(t, m, "Play the computer")
			mnPick(t, m, "intermediate")
			mnPick(t, m, tc.name)
			mnPick(t, m, "pp")
			cfg := mnStartedConfig(t, mnPick(t, m, "18x18"))

			if cfg.Kind != gamestore.VersusBot {
				t.Errorf("kind is %q", cfg.Kind)
			}
			if cfg.Rules.Size != 18 {
				t.Errorf("board size is %d, want the 18 that was chosen", cfg.Rules.Size)
			}
			if got := cfg.Rules.PresetName(); got != "pp" {
				t.Errorf("ruleset is %q, want the pp that was chosen", got)
			}
			mine, ok := cfg.Seats[tc.side]
			if !ok || !mine.Human() {
				t.Fatalf("the %s seat is %+v, want the local player", tc.side, mine)
			}
			if mine.Profile != "Balint" {
				t.Errorf("the local seat is %q", mine.Profile)
			}
			opponent := cfg.Seats[tc.side.Opponent()]
			if opponent.Bot == nil {
				t.Fatalf("the %s seat has no bot", tc.side.Opponent())
			}
			if got := opponent.Bot.Tier(); got != bot.Intermediate {
				t.Errorf("bot tier is %v, want the intermediate that was chosen", got)
			}
			if !cfg.Hints || cfg.HintFor == nil {
				t.Error("hints are off in a game against the computer")
			}
		})
	}
}

// TestMenuRandomSidePicksBothSides requires random to be a real coin toss:
// every draw is one of the two axes, and over many draws both appear.
func TestMenuRandomSidePicksBothSides(t *testing.T) {
	d := shellTestDeps(t)
	seen := map[game.Player]int{}
	const draws = 200
	for range draws {
		m := mnMenu(t, d, 100, 30)
		mnPick(t, m, "Play the computer")
		mnPick(t, m, "beginner")
		mnPick(t, m, "random")

		side := m.pending.side
		if side != game.Vertical && side != game.Horizontal {
			t.Fatalf("random produced %v, which is not a side", side)
		}
		if !m.pending.randomSide {
			t.Fatal("the random choice was not recorded as random")
		}
		seen[side]++
	}
	if seen[game.Vertical] == 0 || seen[game.Horizontal] == 0 {
		t.Errorf("over %d draws the sides came out %v: random picks only one of them", draws, seen)
	}
}

// TestMenuRandomSideReachesTheGameConfig closes the loop between the coin toss
// and the seat map: whichever side comes up is the one the player is seated on.
func TestMenuRandomSideReachesTheGameConfig(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play the computer")
	mnPick(t, m, "beginner")
	mnPick(t, m, "random")
	drawn := m.pending.side
	mnPick(t, m, "std")
	cfg := mnStartedConfig(t, mnPick(t, m, "12x12"))

	if seat := cfg.Seats[drawn]; !seat.Human() || seat.Profile != "Balint" {
		t.Fatalf("the drawn side %s is seated as %+v", drawn, seat)
	}
	if cfg.Seats[drawn.Opponent()].Bot == nil {
		t.Fatalf("the other side of a random draw has no bot: %+v", cfg.Seats)
	}
}

// TestMenuSideChooserNamesTheAxes: R7 is only met if the player can tell what
// they are choosing, which means naming the borders each side joins.
func TestMenuSideChooserNamesTheAxes(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play the computer")
	mnPick(t, m, "beginner")

	c := mnChooser(t, m)
	if len(c.opts) != 3 {
		t.Fatalf("%d side options, want vertical, horizontal and random", len(c.opts))
	}
	want := map[string]string{
		"vertical":   "top and bottom",
		"horizontal": "left and right",
		"random":     "pick one of the two",
	}
	for i, o := range c.opts {
		phrase, ok := want[o.label]
		if !ok {
			t.Errorf("unexpected side option %q", o.label)
			continue
		}
		if !strings.Contains(o.help, phrase) {
			t.Errorf("option %q does not say which axis it connects: %q", o.label, o.help)
		}
		c.sel = i
		if frame := m.View().Content; !strings.Contains(frame, phrase) {
			t.Errorf("highlighting %q does not show its explanation:\n%s", o.label, frame)
		}
	}
}

// TestMenuHotseatTakesASecondProfile uses the embedded picker, which is the
// same fuzzy browsable chooser the first player used.
func TestMenuHotseatTakesASecondProfile(t *testing.T) {
	d := shellTestDeps(t)
	if _, err := d.Profiles.Create("Bernadett"); err != nil {
		t.Fatal(err)
	}
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play someone at this keyboard")

	f, ok := m.form.(*pickerForm)
	if !ok {
		t.Fatalf("the form is %T, want the profile picker", m.form)
	}
	if !strings.Contains(m.View().Content, "Bernadett") {
		t.Errorf("the other player's profile is not listed:\n%s", m.View().Content)
	}

	// Choosing yourself is refused where the player is looking.
	f.p.edit.setValue("Balint")
	f.p.retype()
	cmd := shellSend(t, m, "enter")
	if cmd == nil {
		t.Fatal("choosing produced no command")
	}
	m.Update(cmd())
	if _, still := m.form.(*pickerForm); !still {
		t.Fatalf("choosing yourself left the picker; the form is now %T", m.form)
	}
	if !strings.Contains(f.p.problem, "That is you") {
		t.Errorf("choosing yourself said %q", f.p.problem)
	}

	f.p.edit.setValue("Bernadett")
	f.p.retype()
	cmd = shellSend(t, m, "enter")
	m.Update(cmd())
	if m.pending.opponent != "Bernadett" {
		t.Fatalf("the second player is %q", m.pending.opponent)
	}

	mnPick(t, m, "vertical")
	mnPick(t, m, "std")
	cfg := mnStartedConfig(t, mnPick(t, m, "12x12"))
	if cfg.Kind != gamestore.Hotseat {
		t.Errorf("kind is %q", cfg.Kind)
	}
	if got := cfg.Seats[game.Vertical].Profile; got != "Balint" {
		t.Errorf("vertical is %q, want Balint", got)
	}
	if got := cfg.Seats[game.Horizontal].Profile; got != "Bernadett" {
		t.Errorf("horizontal is %q, want Bernadett", got)
	}
}

// TestMenuFormsWalkBackwards: escape returns to the previous question instead of
// throwing every answer away, and from the first one back to the menu.
func TestMenuFormsWalkBackwards(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)

	mnPick(t, m, "Play the computer")
	mnPick(t, m, "pro")
	if got := mnChooser(t, m).title; got != "Which side do you play?" {
		t.Fatalf("after the tier the form is %q", got)
	}
	shellSend(t, m, "esc")
	if got := mnChooser(t, m).title; got != "How strong an opponent?" {
		t.Fatalf("escape went to %q, want back to the tier question", got)
	}
	if got := m.pending.tier; got != bot.Pro {
		t.Errorf("the answer was lost on the way back: tier is %v", got)
	}
	if got := mnChooser(t, m); got.opts[got.sel].label != "pro" {
		t.Errorf("the tier question reopened on %q, want the answer given before", got.opts[got.sel].label)
	}
	shellSend(t, m, "esc")
	if m.form != nil {
		t.Fatalf("escape from the first question left %T open", m.form)
	}
}

// TestMenuSaysWhenThereIsNothingToContinue: an empty list is worse than a
// sentence, so the entry answers instead of opening one.
func TestMenuSaysWhenThereIsNothingToContinue(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Continue a saved game")

	if m.form != nil {
		t.Fatalf("an empty list opened as %T", m.form)
	}
	if !strings.Contains(m.message, "No unfinished games") {
		t.Errorf("nothing to continue said %q", m.message)
	}
	if !strings.Contains(m.View().Content, "No unfinished games") {
		t.Errorf("the answer is not on screen:\n%s", m.View().Content)
	}
}

// TestMenuResumesASavedGame checks both the listing and the configuration a
// resume produces: the seats come from the stored row, and the record is handed
// over for the game screen to replay.
func TestMenuResumesASavedGame(t *testing.T) {
	d := shellTestDeps(t)
	sv := mnSaveGame(t, d, gamestore.VersusBot, "Balint", leaderboard.BotName("pro"))
	m := mnMenu(t, d, 100, 30)

	mnPick(t, m, "Continue a saved game")
	frame := m.View().Content
	if !strings.Contains(frame, sv.Describe()) {
		t.Errorf("the saved game is not listed as it describes itself:\n%s", frame)
	}

	cfg := mnStartedConfig(t, shellSend(t, m, "enter"))
	if cfg.Resume == nil || cfg.Resume.ID != sv.ID {
		t.Fatalf("resume is %+v, want the stored game", cfg.Resume)
	}
	if cfg.StoreID != sv.ID {
		t.Errorf("store id is %q, want the game saved back over itself", cfg.StoreID)
	}
	if cfg.Kind != gamestore.VersusBot {
		t.Errorf("kind is %q", cfg.Kind)
	}
	if got := cfg.Seats[game.Vertical].Profile; got != "Balint" {
		t.Errorf("vertical is %q, want the stored player", got)
	}
	opponent := cfg.Seats[game.Horizontal]
	if opponent.Bot == nil || opponent.Bot.Tier() != bot.Pro {
		t.Fatalf("the opponent is %+v, want the stored pro bot", opponent)
	}
	if cfg.Rules.Size != 12 {
		t.Errorf("board size is %d, want the stored 12", cfg.Rules.Size)
	}
}

// TestMenuRefusesToResumeANetworkGameWithoutItsConnection: the game screen
// rejects a remote seat with no session, so the menu must not offer one.
func TestMenuRefusesToResumeANetworkGameWithoutItsConnection(t *testing.T) {
	d := shellTestDeps(t)
	mnSaveGame(t, d, gamestore.Remote, "Balint", leaderboard.RemoteName("Zsofia"))
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Continue a saved game")

	c := mnChooser(t, m)
	if len(c.opts) != 1 || !c.opts[0].disabled {
		t.Fatalf("the network game is offered as playable: %+v", c.opts)
	}
	if cmd := shellSend(t, m, "enter"); cmd != nil {
		t.Fatalf("choosing it produced %T", cmd())
	}
	if !strings.Contains(m.message, "connection") {
		t.Errorf("the refusal does not explain itself: %q", m.message)
	}
	if !strings.Contains(m.View().Content, "connection") {
		t.Errorf("the refusal is not on screen:\n%s", m.View().Content)
	}
}

// TestMenuLeaderboardLabelsTheRateAsScore: the rate counts half of every draw,
// so calling it a win rate would be wrong.
func TestMenuLeaderboardLabelsTheRateAsScore(t *testing.T) {
	d := shellTestDeps(t)
	if err := d.Board.Record(leaderboard.Result{
		Played:   time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
		Player:   "Balint",
		Opponent: leaderboard.BotName("pro"),
		Outcome:  leaderboard.DrawOutcome,
		Side:     "vertical",
		Moves:    40,
		Ruleset:  game.Std.Canonical(),
	}); err != nil {
		t.Fatal(err)
	}
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Leaderboard")

	frame := m.View().Content
	if !strings.Contains(frame, "score") {
		t.Errorf("the rate column is not labelled score:\n%s", frame)
	}
	if strings.Contains(frame, "wins") || strings.Contains(frame, "win rate") {
		t.Errorf("the rate column is labelled as wins, but it counts half of every draw:\n%s", frame)
	}
	if !strings.Contains(frame, "Balint") || !strings.Contains(frame, "bot pro") {
		t.Errorf("the participants are not listed:\n%s", frame)
	}
	if !strings.Contains(frame, "50%") {
		t.Errorf("a single draw is not shown as a 50%% score:\n%s", frame)
	}
	shellSend(t, m, "esc")
	if m.form != nil {
		t.Error("escape did not close the leaderboard")
	}
}

func TestMenuThemeChoiceAppliesAndIsRemembered(t *testing.T) {
	d := shellTestDeps(t)
	var other theme.Theme
	for _, th := range theme.All() {
		if th.Name != d.Theme.Name {
			other = th
			break
		}
	}
	if other.Name == "" {
		t.Skip("only one theme is built in")
	}
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Colours")
	cmd := mnPick(t, m, other.Name)
	if cmd == nil {
		t.Fatal("choosing a theme produced no command")
	}
	msg, ok := cmd().(ThemeChangedMsg)
	if !ok {
		t.Fatalf("choosing a theme produced %T, want a ThemeChangedMsg", cmd())
	}
	if msg.Theme.Name != other.Name || msg.Styles == nil {
		t.Fatalf("the message carries %q with styles %v", msg.Theme.Name, msg.Styles != nil)
	}
	stored, err := theme.Selected(d.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != other.Name {
		t.Errorf("the stored theme is %q, want %q", stored.Name, other.Name)
	}
	m.Update(msg)
	if m.deps.Theme.Name != other.Name {
		t.Errorf("the menu is still drawing with %q", m.deps.Theme.Name)
	}
}

// TestMenuNetworkFormAsksForWhatItNeeds walks the relay path far enough to see
// the address field and the wait, and gives up on the wait without connecting.
func TestMenuNetworkFormAsksForWhatItNeeds(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play someone over the network")
	mnPick(t, m, "join their pairing code")

	form, ok := m.form.(*textForm)
	if !ok {
		t.Fatalf("the form is %T, want the relay address field", m.form)
	}
	if form.title != "Relay address" {
		t.Fatalf("the field asks for %q", form.title)
	}
	// An empty answer is refused with a reason rather than accepted.
	shellSend(t, m, "enter")
	if _, still := m.form.(*textForm); !still {
		t.Fatal("an empty relay address was accepted")
	}
	if m.message == "" {
		t.Error("an empty relay address was refused without saying why")
	}

	mnTypeInto(t, m, "relay.example:4271")
	shellSend(t, m, "enter")
	if m.pending.relay != "relay.example:4271" {
		t.Fatalf("the relay is %q", m.pending.relay)
	}
	code, ok := m.form.(*textForm)
	if !ok || code.title != "Their pairing code" {
		t.Fatalf("after the relay the form is %T", m.form)
	}
	mnTypeInto(t, m, "ABCD")
	shellSend(t, m, "enter")
	if m.pending.target != "ABCD" {
		t.Fatalf("the pairing code is %q", m.pending.target)
	}

	if _, ok := m.form.(*waitForm); !ok {
		t.Fatalf("after the code the form is %T, want the wait", m.form)
	}
	if frame := m.View().Content; !strings.Contains(frame, "esc") {
		t.Errorf("the wait does not say how to give up:\n%s", frame)
	}
	shellSend(t, m, "esc")
	if m.form != nil {
		t.Fatalf("escape left %T open", m.form)
	}
	if !strings.Contains(m.message, "Gave up") {
		t.Errorf("giving up said %q", m.message)
	}
}

func mnTypeInto(t *testing.T, m *Menu, text string) {
	t.Helper()
	for _, r := range text {
		key := string(r)
		if r == ' ' {
			key = "space"
		}
		shellSend(t, m, key)
	}
}

// TestMenuHostFormAsksForTheTermsOfTheGame: the host sets the rules and the
// side, so those questions belong to the host path and not the guest one.
func TestMenuHostFormAsksForTheTermsOfTheGame(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play someone over the network")
	mnPick(t, m, "wait for them through a relay")

	mnTypeInto(t, m, "relay.example")
	shellSend(t, m, "enter")
	for _, want := range []string{"Which side do you play?", "Which rules?", "How big a board?"} {
		if got := mnChooser(t, m).title; got != want {
			t.Fatalf("the host is asked %q, want %q", got, want)
		}
		shellSend(t, m, "enter")
	}
	wait, ok := m.form.(*waitForm)
	if !ok {
		t.Fatalf("after the terms the form is %T, want the wait", m.form)
	}
	joined := strings.Join(wait.info, "\n")
	if !strings.Contains(joined, "Pairing code:") {
		t.Errorf("the host is not told the code to pass on: %q", joined)
	}
	if !strings.Contains(joined, "relay.example") {
		t.Errorf("the host is not told what the opponent must run: %q", joined)
	}
	shellSend(t, m, "esc")
}

// TestMenuGuestIsNotAskedForTermsItCannotSet.
func TestMenuGuestIsNotAskedForTermsItCannotSet(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 100, 30)
	mnPick(t, m, "Play someone over the network")
	mnPick(t, m, "connect to their address")

	form, ok := m.form.(*textForm)
	if !ok || form.title != "Their address" {
		t.Fatalf("the guest is asked for %T", m.form)
	}
	mnTypeInto(t, m, "127.0.0.1")
	shellSend(t, m, "enter")
	if _, ok := m.form.(*waitForm); !ok {
		t.Fatalf("after the address the form is %T, want the connection", m.form)
	}
	if want := "127.0.0.1:" + netplay.DefaultPort; m.pending.target != want {
		t.Errorf("the address is %q, want it normalised to %q", m.pending.target, want)
	}
	shellSend(t, m, "esc")
}

func TestMenuFitsEverySize(t *testing.T) {
	forms := []string{
		"Play the computer",
		"Play someone at this keyboard",
		"Play someone over the network",
		"Continue a saved game",
		"Leaderboard",
		"Colours",
	}
	for _, size := range shellSizes {
		w, h := size[0], size[1]
		d := shellTestDeps(t)
		mnSaveGame(t, d, gamestore.VersusBot, "Balint", leaderboard.BotName("beginner"))
		if err := d.Board.Record(leaderboard.Result{
			Played: time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC), Player: "Balint",
			Opponent: leaderboard.BotName("pro"), Outcome: leaderboard.Win,
			Side: "vertical", Moves: 40, Ruleset: game.Std.Canonical(),
		}); err != nil {
			t.Fatal(err)
		}

		m := mnMenu(t, d, w, h)
		shellAssertFits(t, "menu", m.View().Content, w, h)
		// Every entry highlighted in turn, so the longest explanation is drawn.
		for range m.list.opts {
			shellSend(t, m, "down")
			shellAssertFits(t, "menu", m.View().Content, w, h)
		}

		for _, prefix := range forms {
			f := mnMenu(t, d, w, h)
			mnPick(t, f, prefix)
			shellAssertFits(t, "menu form "+prefix, f.View().Content, w, h)
		}

		// A text field, and a text field carrying a complaint.
		f := mnMenu(t, d, w, h)
		mnPick(t, f, "Play someone over the network")
		mnPick(t, f, "join their pairing code")
		shellAssertFits(t, "menu text field", f.View().Content, w, h)
		shellSend(t, f, "enter")
		shellAssertFits(t, "menu text field with a complaint", f.View().Content, w, h)

		// And the wait, whose lines are the longest text on the screen.
		mnTypeInto(t, f, "relay.example:4271")
		shellSend(t, f, "enter")
		mnTypeInto(t, f, "ABCD")
		shellSend(t, f, "enter")
		shellAssertFits(t, "menu waiting", f.View().Content, w, h)
		shellSend(t, f, "esc")
	}
}

func TestMenuSurvivesShrinkAndRegrow(t *testing.T) {
	d := shellTestDeps(t)
	m := mnMenu(t, d, 80, 24)
	shellSend(t, m, "down")
	shellSend(t, m, "down")
	shellSend(t, m, "down")
	want := m.list.opts[m.list.sel].label

	before := m.View().Content
	shellAssertFits(t, "menu", before, 80, 24)

	m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	shrunk := m.View().Content
	shellAssertFits(t, "menu shrunk", shrunk, 20, 8)
	if got := m.list.opts[m.list.sel].label; got != want {
		t.Errorf("the selection changed on shrink to %q", got)
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	after := m.View().Content
	if after != before {
		t.Errorf("the frame changed across shrink and regrow:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := m.list.opts[m.list.sel].label; got != want {
		t.Errorf("the selection is %q after regrow, want %q", got, want)
	}
}
