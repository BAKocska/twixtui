package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/BAKocska/twixtui/internal/bot"
	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/netplay"
	"github.com/BAKocska/twixtui/internal/theme"
	"github.com/BAKocska/twixtui/internal/ui"
)

// Menu is the main menu: everything a player can reach, each entry with a
// one-line explanation shown while it is highlighted, plus the small forms that
// collect the choices a game needs before it can start.
//
// The forms live inside this model rather than being screens of their own
// because a screen cannot push another screen: the shell's DoneMsg replaces or
// pops, so a wizard built out of screens could not walk backwards.
type Menu struct {
	deps   Deps
	player string

	nav  navKeys
	list *chooser
	// form is the question on top of the list, nil when the list has focus.
	form menuForm
	// moveHint and quitHint are the status-line fragments naming the keys, built
	// once because the keys they name cannot change while the screen is up.
	moveHint, quitHint string

	// pending is the answers collected so far for a game that has not started,
	// and steps the questions still to ask.
	pending gameSetup
	steps   []stepFn
	stepIdx int

	// message is the last thing that went wrong or was decided, shown under
	// the list until the player moves on.
	message string

	width, height int

	// cancelWait gives up on a network connection the player is waiting for.
	cancelWait context.CancelFunc
}

// gameSetup is the set of answers a new game is assembled from.
type gameSetup struct {
	kind  gamestore.Kind
	rules game.Ruleset

	side       game.Player
	randomSide bool

	tier bot.Tier
	// opponent is the second profile in a game at this keyboard.
	opponent string

	// role, relay and target describe a network game: which end this is, the
	// relay to pair through when one is used, and the address or pairing code
	// to reach the opponent with.
	role   netplay.Role
	relay  string
	target string
}

// NewMenu returns the main menu for a player.
func NewMenu(d Deps, player string) *Menu {
	km := shellKeymap(d)
	m := &Menu{deps: d, player: player, nav: newNavKeys(km)}
	m.moveHint = keyLabel(m.nav.up...) + "/" + keyLabel(m.nav.down...)
	m.quitHint = keyLabel(globalQuitKeys(km)...) + " quit"
	m.list = &chooser{
		title: "twixt — " + player,
		opts:  menuEntries(),
		// There is nothing above the main list to back out to, and quitting is
		// an entry of its own so that it cannot happen by pressing escape one
		// time too many.
		cancel: func(*Menu) tea.Cmd { return nil },
		pick: func(m *Menu, i int) tea.Cmd {
			run, ok := m.list.opts[i].value.(func(*Menu) tea.Cmd)
			if !ok {
				return nil
			}
			m.message = ""
			return run(m)
		},
	}
	return m
}

// menuEntries is the whole of what the interface can do. Each entry explains
// itself in one line, which is what the player sees while it is highlighted.
func menuEntries() []menuOption {
	return []menuOption{
		{
			label: "Play the computer",
			help:  "Three engine tiers, beginner to pro. Press ? on your turn for a hint.",
			value: (*Menu).startVersusBot,
		},
		{
			label: "Play someone at this keyboard",
			help:  "Two players taking turns on one machine, on one board.",
			value: (*Menu).startHotseat,
		},
		{
			label: "Play someone over the network",
			help:  "A direct connection, or a relay when neither of you can accept one.",
			value: (*Menu).startNetwork,
		},
		{
			label: "Continue a saved game",
			help:  "Pick up an unfinished game exactly where it was left.",
			value: (*Menu).openSaved,
		},
		{
			label: "Learn to play",
			help:  "The rules and the moves, taught on a real board you play on.",
			value: (*Menu).openTutorial,
		},
		{
			label: "Leaderboard",
			help:  "Ratings and results of every game recorded on this machine.",
			value: (*Menu).openLeaderboard,
		},
		{
			label: "Colours",
			help:  "Choose the colour scheme the board and the panels are drawn in.",
			value: (*Menu).openThemes,
		},
		{
			label: "Switch profile",
			help:  "Play as somebody else on this machine.",
			value: (*Menu).switchProfile,
		},
		{
			label: "Quit",
			help:  "Leave twixtui.",
			value: (*Menu).quit,
		},
	}
}

// Init implements tea.Model.
func (m *Menu) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Menu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = t.Width, t.Height
		if f, ok := m.form.(*pickerForm); ok {
			f.p.Update(t)
		}
		return m, nil

	case ThemeChangedMsg:
		m.deps.Theme = t.Theme
		if t.Styles != nil {
			m.deps.Styles = t.Styles
		}
		if f, ok := m.form.(*pickerForm); ok {
			// The embedded picker draws itself, so it needs the new styles too.
			f.p.Update(t)
		}
		return m, nil

	case menuSessionMsg:
		return m, m.connected(t)

	case menuOpponentMsg:
		return m, m.opponentChosen(t)

	case tea.KeyPressMsg:
		if m.form != nil {
			return m, m.form.key(m, t)
		}
		return m, m.list.key(m, t)
	}
	return m, nil
}

// View implements tea.Model. The shell owns the alternate screen, so only the
// content is set.
func (m *Menu) View() tea.View {
	// A form that lays itself out for the whole terminal draws the frame
	// itself; the embedded profile picker is the one that does.
	if f, ok := m.form.(fullFormer); ok {
		return tea.NewView(f.frame(m))
	}
	st := shellStyles(m.deps)
	w, h := m.width, m.height

	var form menuForm = m.list
	if m.form != nil {
		form = m.form
	}
	content := form.lines(m, st, w, max(0, h-1))
	status := paint(st, &st.Status, hintLine(w, form.hints(m)...))
	return tea.NewView(textFrame(st, w, h, content, status))
}

// opponentChosen takes the second player's name back from the embedded picker.
func (m *Menu) opponentChosen(msg menuOpponentMsg) tea.Cmd {
	f, ok := m.form.(*pickerForm)
	if !ok {
		return nil
	}
	if msg.name == "" {
		return backOneStep(m)
	}
	if strings.EqualFold(msg.name, m.player) {
		// Both seats would be the same profile, which the leaderboard cannot
		// record and which is not a game anyway. Stay on the picker and say so
		// there, since that is where the player is looking.
		f.p.problem = "That is you. Pick the other player."
		return nil
	}
	m.pending.opponent = msg.name
	return m.answered()
}

// entry actions.

func (m *Menu) startVersusBot() tea.Cmd {
	m.pending = newGameSetup(gamestore.VersusBot)
	return m.startSteps(stepTier, stepSide, stepRules, stepSize)
}

func (m *Menu) startHotseat() tea.Cmd {
	m.pending = newGameSetup(gamestore.Hotseat)
	return m.startSteps(stepOpponent, stepSide, stepRules, stepSize)
}

func (m *Menu) startNetwork() tea.Cmd {
	m.pending = newGameSetup(gamestore.Remote)
	return m.startSteps(stepNetMethod)
}

// newGameSetup is the state of a game's answers before any question is asked.
//
// It starts at the defaults the command line uses, so that the answer already
// highlighted in each chooser is the documented one: pressing enter through the
// questions must give the same game as `twixtui play bot`. Every chooser then
// selects whatever the setup already holds, which is also what makes walking
// backwards through the form keep the answers given so far.
func newGameSetup(k gamestore.Kind) gameSetup {
	return gameSetup{kind: k, rules: game.Std, tier: bot.Intermediate}
}

func (m *Menu) openTutorial() tea.Cmd {
	// The tutorial owns its own lesson list, so the menu hands over without a
	// lesson rather than offering a second chooser for the same thing.
	sc, err := NewTutorialScreen(m.deps, "")
	if err != nil {
		return Fail(err)
	}
	return Replace(sc)
}

func (m *Menu) switchProfile() tea.Cmd {
	return Replace(NewPicker(m.deps, "Who is playing?"))
}

func (m *Menu) quit() tea.Cmd { return Quit() }

// openSaved lists the games still waiting for a move.
func (m *Menu) openSaved() tea.Cmd {
	saved := m.deps.Games.Unfinished()
	if len(saved) == 0 {
		m.message = "No unfinished games on this machine."
		return nil
	}
	now := m.deps.Clock()
	opts := make([]menuOption, 0, len(saved))
	for _, sv := range saved {
		o := menuOption{label: savedRow(now, sv), value: sv, help: savedHelp(sv)}
		if sv.Kind == gamestore.Remote {
			// A live network game cannot be picked up without reconnecting,
			// and the game screen refuses a remote seat with no session.
			o.disabled = true
		}
		opts = append(opts, o)
	}
	m.form = &chooser{
		title:  "Continue a saved game",
		opts:   opts,
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			sv, ok := m.form.(*chooser).opts[i].value.(gamestore.Saved)
			if !ok {
				return nil
			}
			cfg, err := m.resumeConfig(sv)
			if err != nil {
				m.message = err.Error()
				return nil
			}
			return m.start(cfg)
		},
	}
	return nil
}

// savedRow is the line a player picks a game by, so it has to carry whatever
// tells two of them apart. Players and side alone do not: a second game against
// the same opponent is the same line twice, which is what this list used to
// show. How far in it is and when it was last touched are what differ.
//
// Counting the moves means decoding and replaying the stored record. Measured
// on an Apple M5 Pro that is 15µs for a 60-move game on a 24x24 board and 32µs
// for a 160-move one, so a hundred saved games cost a few milliseconds on top of
// the hundred file reads the listing already does. That is why it is done here
// for every row rather than only for the highlighted one. A record that will not
// load is listed without those two fields rather than hidden: choosing it will
// explain why.
func savedRow(now time.Time, sv gamestore.Saved) string {
	parts := make([]string, 0, 5)
	parts = append(parts, sv.Player+" vs "+leaderboard.DisplayName(sv.Opponent))
	if sv.Side != "" {
		parts = append(parts, sv.Side)
	}
	// Ordered by how much each field distinguishes one save from another, since
	// a narrow panel cuts the end off: two games of the same pairing differ
	// first by when they were last touched, then by how far in they are, and
	// only sometimes by the board they are on.
	parts = append(parts, playedAgo(now, sv.Updated))
	if g, err := sv.Game(); err == nil {
		parts = append(parts, plural(g.Ply(), "move"), fmt.Sprintf("%dx%d", g.Size(), g.Size()))
	}
	return strings.Join(parts, " · ")
}

func savedHelp(sv gamestore.Saved) string {
	if sv.Kind == gamestore.Remote {
		return "A network game needs the connection back: host or join again from the network menu."
	}
	return fmt.Sprintf("A %s game, last played %s. It resumes exactly where it was left.",
		sv.Kind, sv.Updated.Local().Format("2 January 2006 at 15:04"))
}

// openLeaderboard shows the standings in a panel that scrolls.
func (m *Menu) openLeaderboard() tea.Cmd {
	m.form = &scrollForm{title: "Leaderboard", body: standingsLines(m.deps)}
	return nil
}

// standingsLines renders the standings: people ranked against one another, and
// under them the bots they played, unranked. A bot's rating is a constant in the
// program rather than something it won, so a single column holding both invites
// a comparison neither number supports — that is what made a player who had lost
// their only game read as the best on the machine.
//
// The rate column is labelled "score" and not "wins" because it counts half of
// every draw, which is the same quantity the rating is derived from.
func standingsLines(d Deps) []string {
	board := d.Board.Standings()
	if len(board.Players) == 0 && len(board.Bots) == 0 {
		return []string{"No games recorded yet. Play one and it will appear here."}
	}

	nameW := len("player")
	for _, s := range board.Players {
		nameW = max(nameW, len([]rune(leaderboard.DisplayName(s.Name))))
	}
	for _, s := range board.Bots {
		nameW = max(nameW, len([]rune(leaderboard.DisplayName(s.Name))))
	}
	// A position needs somebody to hold it against. With one player it would
	// say only that they are the only one, over a score that is quite possibly
	// zero, so the column waits for a second player before it appears.
	rankW := 0
	if len(board.Players) > 1 {
		rankW = len("#") + 3
	}
	// padTo cannot pad to nothing, so the position is built here: with no rank
	// column there is no prefix at all, not a one-character stub.
	pos := func(s string) string {
		if rankW == 0 {
			return ""
		}
		return padTo(s, rankW)
	}
	row := func(rank, name string, s leaderboard.Standing) string {
		return fmt.Sprintf("%s%s %6d %6d %5.0f%%",
			pos(rank), padTo(name, nameW), s.Rating, s.Played, s.WinRate*100)
	}

	out := make([]string, 0, len(board.Players)+len(board.Bots)+4)
	out = append(out, fmt.Sprintf("%s%s %6s %6s %6s",
		pos("#"), padTo("player", nameW), "rating", "games", "score"))
	for i, s := range board.Players {
		out = append(out, row(strconv.Itoa(i+1), leaderboard.DisplayName(s.Name), s))
	}
	if len(board.Bots) > 0 {
		out = append(out, "", "Bots are not ranked: a tier's rating is fixed, not earned.", "")
		for _, s := range board.Bots {
			out = append(out, row("", leaderboard.DisplayName(s.Name), s))
		}
	}
	return out
}

// openThemes offers the colour schemes.
func (m *Menu) openThemes() tea.Cmd {
	all := theme.All()
	opts := make([]menuOption, 0, len(all))
	sel := 0
	for i, t := range all {
		if t.Name == m.deps.Theme.Name {
			sel = i
		}
		label := t.Name
		if t.Name == m.deps.Theme.Name {
			label += " (in use)"
		}
		opts = append(opts, menuOption{label: label, help: t.Summary, value: t})
	}
	m.form = &chooser{
		title:  "Colours",
		opts:   opts,
		sel:    sel,
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			t, ok := m.form.(*chooser).opts[i].value.(theme.Theme)
			if !ok {
				return nil
			}
			if _, err := theme.Select(m.deps.ConfigDir, t.Name); err != nil {
				// The choice still applies to this run; only remembering it
				// for next time failed, and saying so is better than pretending
				// the setting was stored.
				m.message = "Using " + t.Name + " now, but it could not be saved: " + err.Error()
			} else {
				m.message = "Colours: " + t.Name + "."
			}
			styles := ui.StylesFor(t)
			m.form = nil
			return func() tea.Msg { return ThemeChangedMsg{Theme: t, Styles: &styles} }
		},
	}
	return nil
}

// the new-game form.

// stepFn asks one question of the new-game form. It installs a form and
// returns any command that has to run alongside it.
type stepFn func(m *Menu) tea.Cmd

// startSteps begins a new-game form at its first question.
func (m *Menu) startSteps(steps ...stepFn) tea.Cmd {
	m.steps = steps
	return m.runStep(0)
}

// runStep shows question i, or starts the game when the questions run out.
func (m *Menu) runStep(i int) tea.Cmd {
	if i < 0 {
		return closeForm(m)
	}
	if i >= len(m.steps) {
		m.form = nil
		cfg, err := m.buildConfig()
		if err != nil {
			m.message = err.Error()
			return nil
		}
		return m.start(cfg)
	}
	m.stepIdx = i
	return m.steps[i](m)
}

// answered records an answer and moves to the next question.
func (m *Menu) answered() tea.Cmd { return m.runStep(m.stepIdx + 1) }

// backOneStep returns to the previous question, or to the menu list from the
// first one.
func backOneStep(m *Menu) tea.Cmd {
	if m.stepIdx == 0 {
		return closeForm(m)
	}
	return m.runStep(m.stepIdx - 1)
}

// closeForm gives focus back to the menu list.
func closeForm(m *Menu) tea.Cmd {
	m.form = nil
	return nil
}

func stepTier(m *Menu) tea.Cmd {
	names := bot.TierNames()
	opts := make([]menuOption, 0, len(names))
	sel := 0
	for _, n := range names {
		t, err := bot.ParseTier(n)
		if err != nil {
			continue
		}
		// The selection is found by value rather than by position: a tier the
		// list skips would otherwise shift every index after it.
		if t == m.pending.tier {
			sel = len(opts)
		}
		opts = append(opts, menuOption{label: n, help: bot.TierSummary(n), value: t})
	}
	m.form = &chooser{
		title:  "How strong an opponent?",
		opts:   opts,
		sel:    sel,
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			t, _ := m.form.(*chooser).opts[i].value.(bot.Tier)
			m.pending.tier = t
			return m.answered()
		},
	}
	return nil
}

// stepSide is R7: the player picks their colour, by the axis it connects,
// before the first move. Random is offered as well, and resolved here so that
// the rest of the form and the game itself see a concrete side.
func stepSide(m *Menu) tea.Cmd {
	m.form = &chooser{
		title: "Which side do you play?",
		opts: []menuOption{
			{
				label: "vertical",
				help:  "You join the top and bottom borders. Vertical always moves first.",
				value: game.Vertical,
			},
			{
				label: "horizontal",
				help:  "You join the left and right borders, and move second.",
				value: game.Horizontal,
			},
			{
				label: "random",
				help:  "Let twixtui pick one of the two for you.",
				value: game.NoPlayer,
			},
		},
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			side, _ := m.form.(*chooser).opts[i].value.(game.Player)
			m.pending.randomSide = side == game.NoPlayer
			if m.pending.randomSide {
				side = randomSide()
			}
			m.pending.side = side
			if m.pending.randomSide {
				m.message = "You play " + side.String() + "."
			}
			return m.answered()
		},
	}
	return nil
}

// randomSide picks a side. Vertical moves first, so this is a real coin toss
// over the first move and not only over a colour.
func randomSide() game.Player {
	if rand.IntN(2) == 0 {
		return game.Vertical
	}
	return game.Horizontal
}

func stepRules(m *Menu) tea.Cmd {
	names := game.PresetNames()
	opts := make([]menuOption, 0, len(names))
	sel := 0
	current := m.pending.rules.PresetName()
	for i, n := range names {
		if n == current {
			sel = i
		}
		opts = append(opts, menuOption{label: n, help: game.PresetSummary(n), value: n})
	}
	m.form = &chooser{
		title:  "Which rules?",
		opts:   opts,
		sel:    sel,
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			name, _ := m.form.(*chooser).opts[i].value.(string)
			rs, err := game.Preset(name)
			if err != nil {
				m.message = err.Error()
				return nil
			}
			m.pending.rules = rs
			return m.answered()
		},
	}
	return nil
}

// boardSizes are the sizes offered, and standardSize is the one a new game
// starts on. The standard commercial board is 24; the smaller ones are here
// because a whole game on one fits in a small pane.
var boardSizes = []int{12, 18, 24, 30}

const standardSize = 24

func stepSize(m *Menu) tea.Cmd {
	opts := make([]menuOption, 0, len(boardSizes))
	sel := 0
	for i, n := range boardSizes {
		if n == m.pending.rules.Size || (m.pending.rules.Size == 0 && n == standardSize) {
			sel = i
		}
		opts = append(opts, menuOption{
			label: fmt.Sprintf("%dx%d", n, n),
			help:  boardSizeHelp(n),
			value: n,
		})
	}
	m.form = &chooser{
		title:  "How big a board?",
		opts:   opts,
		sel:    sel,
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			n, _ := m.form.(*chooser).opts[i].value.(int)
			m.pending.rules.Size = n
			return m.answered()
		},
	}
	return nil
}

func boardSizeHelp(n int) string {
	switch n {
	case 24:
		return "The standard commercial board."
	case 30:
		return "Bigger than standard: longer games, more room to manoeuvre."
	case 18:
		return "Shorter than standard, and it fits a smaller terminal."
	default:
		return "A quick game, and the easiest board to learn on."
	}
}

// stepOpponent asks who is on the other side of a game at this keyboard,
// reusing the profile picker so that the second player is found the same
// fuzzy, browsable way the first one was.
func stepOpponent(m *Menu) tea.Cmd {
	p := NewPicker(m.deps, "Who is playing the other side?").
		Chosen(func(name string) tea.Cmd {
			return func() tea.Msg { return menuOpponentMsg{name: name} }
		}).
		Cancelled(func() tea.Cmd {
			return func() tea.Msg { return menuOpponentMsg{} }
		})
	if m.width > 0 && m.height > 0 {
		p.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
	m.form = &pickerForm{p: p}
	return nil
}

// menuOpponentMsg carries the second player's name back out of the embedded
// picker. An empty name means the player backed out.
type menuOpponentMsg struct{ name string }

// the network form.

func stepNetMethod(m *Menu) tea.Cmd {
	type method struct {
		role  netplay.Role
		relay bool
	}
	m.form = &chooser{
		title: "How do you want to connect?",
		opts: []menuOption{
			{
				label: "wait for them to connect to me",
				help:  "You set the rules and the side. Works on a local network, a tailnet or a forwarded port.",
				value: method{role: netplay.Host},
			},
			{
				label: "wait for them through a relay",
				help:  "For when neither of you can accept a connection. One of you runs twixtui serve.",
				value: method{role: netplay.Host, relay: true},
			},
			{
				label: "connect to their address",
				help:  "They are waiting; you take the side and the rules they chose.",
				value: method{role: netplay.Guest},
			},
			{
				label: "join their pairing code through a relay",
				help:  "They printed a code. You need the same relay address they used.",
				value: method{role: netplay.Guest, relay: true},
			},
		},
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			mode, _ := m.form.(*chooser).opts[i].value.(method)
			m.pending.role = mode.role
			m.pending.relay = ""
			m.pending.target = ""

			// The method chooser stays as question zero so that escape from the
			// next question comes back here rather than to the menu list.
			steps := []stepFn{stepNetMethod}
			if mode.relay {
				steps = append(steps, stepRelayAddr)
			}
			if mode.role == netplay.Host {
				steps = append(steps, stepSide, stepRules, stepSize)
			} else if !mode.relay {
				steps = append(steps, stepJoinAddr)
			}
			if mode.relay && mode.role == netplay.Guest {
				steps = append(steps, stepPairingCode)
			}
			steps = append(steps, stepConnect)
			m.steps = steps
			return m.runStep(1)
		},
	}
	return nil
}

func stepRelayAddr(m *Menu) tea.Cmd {
	m.form = &textForm{
		title:  "Relay address",
		label:  "host:port of the relay you both use",
		note:   "One of you runs \"twixtui serve\" on a machine you can both reach.",
		value:  m.pending.relay,
		cancel: backOneStep,
		submit: func(m *Menu, v string) tea.Cmd {
			if strings.TrimSpace(v) == "" {
				m.message = "The relay needs an address."
				return nil
			}
			m.pending.relay = strings.TrimSpace(v)
			return m.answered()
		},
	}
	return nil
}

func stepJoinAddr(m *Menu) tea.Cmd {
	m.form = &textForm{
		title:  "Their address",
		label:  "host, or host:port",
		note:   fmt.Sprintf("Port %s is used when you do not give one.", netplay.DefaultPort),
		value:  m.pending.target,
		cancel: backOneStep,
		submit: func(m *Menu, v string) tea.Cmd {
			if strings.TrimSpace(v) == "" {
				m.message = "Type the address they printed."
				return nil
			}
			m.pending.target = netplay.NormalizeAddr(strings.TrimSpace(v))
			return m.answered()
		},
	}
	return nil
}

func stepPairingCode(m *Menu) tea.Cmd {
	m.form = &textForm{
		title:  "Their pairing code",
		label:  "the code they printed",
		note:   "Codes ignore case.",
		value:  m.pending.target,
		cancel: backOneStep,
		submit: func(m *Menu, v string) tea.Cmd {
			if strings.TrimSpace(v) == "" {
				m.message = "Type the code they gave you."
				return nil
			}
			m.pending.target = strings.TrimSpace(v)
			return m.answered()
		},
	}
	return nil
}

// stepConnect starts the connection and shows what the opponent needs to be
// told. The dial runs in a command: it blocks for as long as the other player
// takes, which must not be inside Update.
func stepConnect(m *Menu) tea.Cmd {
	if m.pending.role == netplay.Guest {
		return m.connectAs("Connecting", []string{"Reaching " + m.describeTarget() + "."},
			func(ctx context.Context) (netplay.Session, error) {
				opts := netplay.GuestOptions{Name: m.player}
				if m.pending.relay != "" {
					// The address goes to the relay code unfilled: it supplies
					// DefaultRelayPort itself. Filling it in here with
					// NormalizeAddr sent a player who typed a bare host name to
					// the direct-play port instead, and the only symptom was a
					// connection that would not open.
					return netplay.JoinViaRelay(ctx, m.pending.relay, m.pending.target, opts)
				}
				return netplay.Dial(ctx, m.pending.target, opts)
			})
	}

	rules := m.pending.rules
	if err := rules.Validate(); err != nil {
		m.message = err.Error()
		return nil
	}
	opts := netplay.HostOptions{Name: m.player, Rules: rules, Side: m.pending.side}

	if m.pending.relay != "" {
		code := netplay.PairingCode()
		relay := m.pending.relay
		info := []string{
			"Pairing code: " + code,
			"They run: twixtui play join --relay " + m.pending.relay + " " + code,
		}
		return m.connectAs("Waiting for your opponent", info,
			func(ctx context.Context) (netplay.Session, error) {
				return netplay.HostViaRelay(ctx, relay, code, opts)
			})
	}

	listener, err := netplay.Bind("")
	if err != nil {
		m.message = err.Error()
		return nil
	}
	info := []string{
		"Listening on " + listener.Addr(),
		"They run: twixtui play join <your address>",
	}
	return m.connectAs("Waiting for your opponent", info,
		func(ctx context.Context) (netplay.Session, error) {
			s, err := listener.Wait(ctx, opts)
			if err != nil {
				listener.Close()
			}
			return s, err
		})
}

func (m *Menu) describeTarget() string {
	if m.pending.relay != "" {
		return "code " + m.pending.target + " on " + m.pending.relay
	}
	return m.pending.target
}

// connectAs installs the waiting form and returns the command that connects.
func (m *Menu) connectAs(title string, info []string, dial func(context.Context) (netplay.Session, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelWait = cancel
	m.form = &waitForm{title: title, info: info}
	return func() tea.Msg {
		s, err := dial(ctx)
		cancel()
		return menuSessionMsg{session: s, err: err}
	}
}

// menuSessionMsg is the outcome of a connection attempt.
type menuSessionMsg struct {
	session netplay.Session
	err     error
}

// connected acts on a finished connection attempt.
func (m *Menu) connected(msg menuSessionMsg) tea.Cmd {
	m.cancelWait = nil
	if _, waiting := m.form.(*waitForm); !waiting {
		// The player gave up and moved on. A session that arrived anyway is
		// closed rather than left holding a socket.
		if msg.session != nil {
			msg.session.Close()
		}
		return nil
	}
	m.form = nil
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			m.message = "Gave up waiting."
		} else {
			m.message = msg.err.Error()
		}
		return nil
	}
	side := msg.session.Side()
	cfg := GameConfig{
		Kind:  gamestore.Remote,
		Rules: msg.session.Rules(),
		Seats: map[game.Player]Seat{
			side:            {Profile: m.player, Label: m.player},
			side.Opponent(): {Remote: true, Label: msg.session.OpponentName()},
		},
		Session: msg.session,
	}
	return m.start(cfg)
}

// building the game.

// botLabel names a bot seat. It goes through the recorded name so that the
// panel beside the board, the saved-game list and the standings cannot drift
// apart into three spellings of the same opponent.
func botLabel(t bot.Tier) string {
	return leaderboard.DisplayName(leaderboard.BotName(t.String()))
}

// buildConfig turns the collected answers into a game.
func (m *Menu) buildConfig() (GameConfig, error) {
	p := m.pending
	if err := p.rules.Validate(); err != nil {
		return GameConfig{}, err
	}
	if p.side != game.Vertical && p.side != game.Horizontal {
		return GameConfig{}, errors.New("no side chosen")
	}
	cfg := GameConfig{Kind: p.kind, Rules: p.rules, Seats: make(map[game.Player]Seat, 2)}
	cfg.Seats[p.side] = Seat{Profile: m.player, Label: m.player}

	switch p.kind {
	case gamestore.VersusBot:
		opponent := bot.New(p.tier, m.deps.Clock().UnixNano())
		// The seat is labelled with the same name the leaderboard will show,
		// built from the name the result is recorded under, so the opponent is
		// called one thing from the board to the standings.
		cfg.Seats[p.side.Opponent()] = Seat{Bot: opponent, Label: botLabel(p.tier)}
		// R15: a hint is only meaningful when there is an engine to ask, and
		// the engine already in the game is the one to ask.
		cfg.Hints = true
		cfg.HintFor = opponent
	case gamestore.Hotseat:
		if p.opponent == "" {
			return GameConfig{}, errors.New("the other side has no player")
		}
		if strings.EqualFold(p.opponent, m.player) {
			return GameConfig{}, errors.New("both sides are the same profile: pick a different one")
		}
		cfg.Seats[p.side.Opponent()] = Seat{Profile: p.opponent, Label: p.opponent}
	default:
		return GameConfig{}, fmt.Errorf("cannot start a %s game from here", p.kind)
	}
	return cfg, nil
}

// resumeConfig rebuilds a stored game's setup. The seats come from the stored
// row, because the game screen takes only the position and the rules from the
// record.
func (m *Menu) resumeConfig(sv gamestore.Saved) (GameConfig, error) {
	g, err := sv.Game()
	if err != nil {
		return GameConfig{}, err
	}
	side, err := game.ParsePlayer(sv.Side)
	if err != nil {
		return GameConfig{}, err
	}
	player := sv.Player
	if player == "" {
		player = m.player
	}
	cfg := GameConfig{
		Kind:    sv.Kind,
		Rules:   g.Rules(),
		Seats:   make(map[game.Player]Seat, 2),
		Resume:  &sv,
		StoreID: sv.ID,
	}
	cfg.Seats[side] = Seat{Profile: player, Label: player}

	switch {
	case leaderboard.IsBot(sv.Opponent):
		tier, err := bot.ParseTier(leaderboard.BareName(sv.Opponent))
		if err != nil {
			return GameConfig{}, err
		}
		opponent := bot.New(tier, m.deps.Clock().UnixNano())
		cfg.Seats[side.Opponent()] = Seat{Bot: opponent, Label: botLabel(tier)}
		cfg.Hints = true
		cfg.HintFor = opponent
	case strings.HasPrefix(sv.Opponent, leaderboard.RemotePrefix):
		return GameConfig{}, errors.New("this game needs its connection back: host or join it again")
	default:
		if sv.Opponent == "" {
			return GameConfig{}, errors.New("the stored game does not say who the other player was")
		}
		cfg.Seats[side.Opponent()] = Seat{Profile: sv.Opponent, Label: sv.Opponent}
	}
	return cfg, nil
}

// start hands a finished configuration to the game screen.
func (m *Menu) start(cfg GameConfig) tea.Cmd {
	sc, err := NewGameScreen(m.deps, cfg)
	if err != nil {
		if cfg.Session != nil {
			cfg.Session.Close()
		}
		m.message = err.Error()
		return nil
	}
	return Replace(sc)
}

// forms.

// menuForm is a question drawn on top of the menu list.
type menuForm interface {
	// key handles a keypress; the command it returns goes straight up.
	key(m *Menu, press tea.KeyPressMsg) tea.Cmd
	// lines renders the form into at most height rows of width columns.
	lines(m *Menu, st *ui.Styles, width, height int) []string
	// hints are the status-line parts.
	hints(m *Menu) []string
}

// fullFormer is a form that lays itself out for the whole terminal, status
// line included, and so draws the frame instead of the menu.
type fullFormer interface {
	frame(m *Menu) string
}

// menuOption is one line of a chooser.
type menuOption struct {
	label string
	// help is the one-line explanation shown while this option is highlighted.
	help string
	// value is the answer this option stands for.
	value any
	// disabled marks an option that cannot be taken; help says why.
	disabled bool
}

// chooser is a list of options with an explanation of the highlighted one. It
// is the main menu itself as well as every question with a fixed set of
// answers, so there is one list widget rather than one per question.
type chooser struct {
	title  string
	opts   []menuOption
	sel    int
	pick   func(m *Menu, i int) tea.Cmd
	cancel func(m *Menu) tea.Cmd
}

func (c *chooser) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	key := press.String()
	if sel, moved := m.nav.move(key, c.sel, len(c.opts)); moved {
		c.sel = sel
		m.message = ""
		return nil
	}
	switch {
	case m.nav.isCancel(key):
		return c.cancel(m)
	case m.nav.isConfirm(key):
		if c.sel < 0 || c.sel >= len(c.opts) {
			return nil
		}
		if c.opts[c.sel].disabled {
			m.message = c.opts[c.sel].help
			return nil
		}
		return c.pick(m, c.sel)
	}
	return nil
}

func (c *chooser) lines(m *Menu, st *ui.Styles, width, height int) []string {
	rows := make([]string, 0, len(c.opts))
	for i, o := range c.opts {
		marker := "  "
		style := &st.PanelText
		label := o.label
		if o.disabled {
			style = &st.Label
			label += " — unavailable"
		}
		if i == c.sel {
			marker = paint(st, &st.Cursor, "> ")
		}
		rows = append(rows, marker+paint(st, style, label))
	}
	help := ""
	if c.sel >= 0 && c.sel < len(c.opts) {
		help = c.opts[c.sel].help
	}
	return listPanel(st, c.title, rows, c.sel, m.message, help, width, height)
}

func (c *chooser) hints(m *Menu) []string {
	return []string{
		m.moveHint + " move",
		keyLabel(m.nav.confirm...) + " choose",
		"esc back",
		m.quitHint,
		keyPrev + "/" + keyNext + " move",
	}
}

// textForm asks for one line of text.
type textForm struct {
	title string
	label string
	note  string
	value string

	edit   lineEdit
	primed bool

	submit func(m *Menu, v string) tea.Cmd
	cancel func(m *Menu) tea.Cmd
}

func (f *textForm) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	f.prime()
	key := press.String()
	switch {
	case m.nav.isCancel(key):
		return f.cancel(m)
	case m.nav.isConfirm(key):
		return f.submit(m, f.edit.value())
	}
	if f.edit.key(press) {
		m.message = ""
	}
	return nil
}

// prime fills the field in from the answer given last time, so that walking
// back through the form does not lose what was typed.
func (f *textForm) prime() {
	if f.primed {
		return
	}
	f.primed = true
	if f.value != "" {
		f.edit.setValue(f.value)
	}
}

func (f *textForm) lines(m *Menu, st *ui.Styles, width, height int) []string {
	f.prime()
	out := make([]string, 0, height)
	out = append(out, paint(st, &st.PanelTitle, f.title))
	out = append(out, paint(st, &st.Label, f.label))
	out = append(out, f.edit.render(st, width))
	out = append(out, "")
	if m.message != "" {
		out = appendWrapped(out, st, &st.Message, m.message, width)
	} else if f.note != "" {
		out = appendWrapped(out, st, &st.PanelText, f.note, width)
	}
	return clampLines(out, height)
}

func (f *textForm) hints(m *Menu) []string {
	return []string{
		keyLabel(m.nav.confirm...) + " accept",
		"esc back",
		m.quitHint,
	}
}

// waitForm is shown while a network connection is being made. The connection
// itself runs in a command, so the screen stays responsive and escape really
// does give up.
type waitForm struct {
	title string
	info  []string
}

func (f *waitForm) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	if m.nav.isCancel(press.String()) {
		if m.cancelWait != nil {
			m.cancelWait()
			m.cancelWait = nil
		}
		m.form = nil
		m.message = "Gave up waiting."
	}
	return nil
}

func (f *waitForm) lines(m *Menu, st *ui.Styles, width, height int) []string {
	out := make([]string, 0, height)
	out = append(out, paint(st, &st.PanelTitle, f.title))
	out = append(out, "")
	for _, l := range f.info {
		out = appendWrapped(out, st, &st.PanelText, l, width)
	}
	out = append(out, "")
	out = appendWrapped(out, st, &st.Label, "Press esc to give up.", width)
	return clampLines(out, height)
}

func (f *waitForm) hints(m *Menu) []string {
	return []string{
		"esc give up",
		m.quitHint,
	}
}

// scrollForm shows a block of text that may not fit, such as the standings.
type scrollForm struct {
	title string
	body  []string
	// focus is the line the view is scrolled to keep visible.
	focus int
}

func (f *scrollForm) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	key := press.String()
	if m.nav.isCancel(key) || m.nav.isConfirm(key) {
		return closeForm(m)
	}
	if focus, moved := m.nav.move(key, f.focus, len(f.body)); moved {
		f.focus = focus
	}
	return nil
}

func (f *scrollForm) lines(m *Menu, st *ui.Styles, width, height int) []string {
	out := make([]string, 0, height)
	out = append(out, paint(st, &st.PanelTitle, f.title))
	if height >= 6 {
		out = append(out, "")
	}
	body := make([]string, 0, len(f.body))
	for _, l := range f.body {
		body = append(body, paint(st, &st.PanelText, l))
	}
	out = append(out, window(body, height-len(out), f.focus)...)
	return clampLines(out, height)
}

func (f *scrollForm) hints(m *Menu) []string {
	return []string{
		m.moveHint + " scroll",
		"esc back",
		m.quitHint,
	}
}

// pickerForm embeds the profile picker, which already lays itself out for the
// whole terminal, so it draws the frame rather than a panel inside one.
type pickerForm struct{ p *Picker }

func (f *pickerForm) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	_, cmd := f.p.Update(press)
	return cmd
}

func (f *pickerForm) lines(*Menu, *ui.Styles, int, int) []string { return nil }

func (f *pickerForm) hints(*Menu) []string { return nil }

func (f *pickerForm) frame(m *Menu) string { return f.p.View().Content }

// listPanel is the shared shape of every list on this screen: a title, the
// list, and a fixed-height explanation of the highlighted entry underneath.
// The explanation's height is fixed so that the list does not shift up and
// down as the selection moves.
func listPanel(st *ui.Styles, title string, rows []string, sel int, message, help string, width, height int) []string {
	if height <= 0 {
		return nil
	}
	text, style := help, &st.PanelText
	if message != "" {
		text, style = message, &st.Message
	}
	// The explanation's rows are reserved before the list is given any, and it
	// is pinned to the bottom of the panel, so neither block moves when the
	// selection does. One list row is always kept: an explanation the player
	// cannot act on is worth less than seeing the entry it describes.
	helpH := 0
	if text != "" {
		helpH = min(2, max(0, height-2))
	}
	out := make([]string, 0, height)
	out = append(out, paint(st, &st.PanelTitle, title))
	if height >= 6 {
		out = append(out, "")
	}
	listH := max(1, height-len(out)-helpH)
	out = append(out, window(rows, listH, sel)...)
	if helpH > 0 {
		for len(out) < height-helpH {
			out = append(out, "")
		}
		out = append(out, clampLines(appendWrapped(nil, st, style, text, width), helpH)...)
	}
	return clampLines(out, height)
}
