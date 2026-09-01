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

	"github.com/BAKocska/twixtui/docs"

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
	// reopenOnReveal rebuilds a panel when a screen opened from it finishes, so
	// that a row two levels down returns to its own list rather than to the front.
	reopenOnReveal func(*Menu)
	deps           Deps
	player         string

	nav navKeys
	// listUp and listDown are the letters the board moves by. Every list on
	// this screen answers them as well as the arrows, because the board is
	// driven h/j/k/l and its panel teaches that: a hand coming off the board
	// should not have to find the arrow keys to work the menu. navKeys narrows
	// the same bindings to their arrow forms (see arrowKeys) for the screens
	// that carry a text field, where the letters are characters being typed.
	// Nothing on this screen carries one; the profile picker does, and
	// deliberately keeps the letters as search characters.
	listUp, listDown []string
	list             *chooser
	// form is the question on top of the list, nil when the list has focus.
	form menuForm
	// moveHint and quitHint are the status-line fragments naming the keys, built
	// once because the keys they name cannot change while the screen is up.
	// frontQuitHint is the front screen's own, naming the plain quit letter the
	// board teaches, which only the front list is free to answer.
	moveHint, quitHint, frontQuitHint string
	// quitLetters are the letter forms of the keymap's quit binding. Only the
	// front list answers them: a form one level down is a question being asked,
	// and escape already leaves it, so a letter that ended the whole program
	// from there would be a trap rather than an exit.
	quitLetters []string

	// pending is the answers collected so far for a game that has not started,
	// and steps the questions still to ask.
	pending gameSetup
	steps   []stepFn
	stepIdx int

	// defaults are the stored choices a new game's form starts from, and what
	// the settings panel edits.
	defaults gameDefaults
	// settingsSel keeps the settings list's position while a sub-question is
	// open, so answering one lands back on the row it was asked from.
	settingsSel int

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
	m.defaults = loadDefaults(d.ConfigDir)
	m.listUp, m.listDown = letterKeys(km, ui.ActMoveUp), letterKeys(km, ui.ActMoveDown)
	m.moveHint = m.movementHint()
	m.quitHint = keyLabel(globalQuitKeys(km)...) + " quit"
	m.quitLetters = letterKeys(km, ui.ActQuit)
	if len(m.quitLetters) > 0 {
		m.frontQuitHint = keyLabel(m.quitLetters...) + " quit"
	} else {
		m.frontQuitHint = m.quitHint
	}
	m.list = &chooser{
		title: "twixtui — " + player,
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

// menuEntries is the front screen: what a player does with a game, one entry
// per act, each explaining itself in the line shown while it is highlighted.
//
// The grouping is by how often an act happens, not by what mechanism serves
// it. Playing is the common thing, so it is first, and it is one entry rather
// than one per opponent kind: who is on the other side is the first question
// of a new game, and it is asked the way every other question is, one at a
// time with escape walking backwards. Continuing and watching come next, being
// what a returning player does with games that already exist. Learning — the
// tutorial, the written rules, the introduction again — is one entry, visited
// rarely and usually early. The leaderboard stays on the front screen because
// glancing at the standings is a habit, not a configuration. Everything a
// player sets once and forgets — colours, the rules and board a new game
// starts from, hints, who is playing — is gathered under Settings, one level
// down, where it no longer stands between the player and a game. Quit is last,
// where a reader's eye expects the exit.
func menuEntries() []menuOption {
	return []menuOption{
		{
			label: "Play",
			help:  "A new game: against the computer, at this keyboard, or over the network.",
			value: (*Menu).startPlay,
		},
		{
			label: "Continue a saved game",
			help:  "Pick up an unfinished game exactly where it was left.",
			value: (*Menu).openSaved,
		},
		{
			label: "Watch a finished game",
			help:  "Step through a finished or imported game move by move.",
			value: (*Menu).openWatch,
		},
		{
			label: "Learn to play",
			help:  "The tutorial, the written rules, and the introduction again.",
			value: (*Menu).openLearn,
		},
		{
			label: "Leaderboard",
			help:  "Ratings and results of every game recorded on this machine.",
			value: (*Menu).openLeaderboard,
		},
		{
			label: "Settings",
			help:  "Colours, what a new game starts from, hints, and who is playing.",
			value: (*Menu).openSettings,
		},
		{
			label: "Quit",
			help:  "Leave twixtui.",
			value: (*Menu).quit,
		},
	}
}

// Init implements tea.Model. On the first run — of this profile, on this
// machine — the introduction opens on top of the menu, so that finishing or
// skipping it lands the player here, where everything else is. The check
// belongs in Init and not in revealed(): Init runs once, when the menu is
// built, so a player coming back from a game cannot be handed the tour a
// second time.
func (m *Menu) Init() tea.Cmd {
	if OnboardingSeen(m.deps, m.player) {
		return nil
	}
	sc, err := NewOnboarding(m.deps, m.player)
	if err != nil {
		// A menu that would not open because its introduction failed to build
		// is worse than a menu whose introduction has to be found under Learn.
		return nil
	}
	return Open(sc)
}

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
		if matchesKey(t.String(), m.quitLetters) {
			// The plain quit letter works on the front list, where no letter
			// is a character being typed and none of the questions a form asks
			// is in danger of being abandoned by it.
			return m, Quit()
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
	content := m.frontContent(st, w, max(0, h-1))
	if m.form != nil {
		form = m.form
		content = form.lines(m, st, w, max(0, h-1))
	}
	status := paint(st, &st.Status, hintLine(w, form.hints(m)...))
	return tea.NewView(textFrame(st, w, h, content, status))
}

// The front screen's geometry. The menu takes its column first and the cover
// artwork is offered only what is left, which is the same order of preference
// ui.Arrange applies to a board and its panel: the working part is sized
// first, and the decoration fits in the remainder or is absent. menuPaneWidth
// is enough for every entry label and for its two reserved help rows to wrap
// legibly; at 80 columns the remainder is too narrow for any artwork, so the
// list simply keeps the whole terminal.
const (
	menuPaneWidth = 58
	coverGap      = 2
)

// frontContent lays out the front screen: the menu list, with the cover
// artwork beside it when the terminal leaves room for both. The artwork never
// costs the menu anything — not an entry, not a column of its pane — because
// it is only ever given what the menu did not take.
func (m *Menu) frontContent(st *ui.Styles, width, height int) []string {
	menuW := min(width, menuPaneWidth)
	art := coverColumn(width-menuW-coverGap, height, st.Plain)
	if len(art) == 0 {
		return m.list.lines(m, st, width, height)
	}
	rows := m.list.lines(m, st, menuW, height)
	out := make([]string, 0, max(len(rows), len(art)))
	for i := range max(len(rows), len(art)) {
		var left, right string
		if i < len(rows) {
			left = rows[i]
		}
		if i < len(art) {
			right = art[i]
		}
		if right == "" {
			out = append(out, left)
			continue
		}
		out = append(out, padTo(left, menuW+coverGap)+right)
	}
	return out
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

// startPlay begins the new-game form at its real first question: who the game
// is against. The three kinds used to be three entries on the front screen,
// which spent most of it saying "Play" three ways; the question reads better
// asked once, in the same one-at-a-time form as everything else, and escape
// from it walks backwards exactly as it does everywhere in the form.
func (m *Menu) startPlay() tea.Cmd {
	m.pending = gameSetup{}
	return m.startSteps(stepWho)
}

// stepWho asks who is on the other side, and chains the questions that follow
// from the answer. It stays as question zero of whatever chain it picks, so
// escape from the next question comes back here rather than to the front list.
func stepWho(m *Menu) tea.Cmd {
	sel := 0
	switch m.pending.kind {
	case gamestore.Hotseat:
		sel = 1
	case gamestore.Remote, gamestore.Correspondence:
		sel = 2
	}
	m.form = &chooser{
		title: "Who do you want to play?",
		opts: []menuOption{
			{
				label: "the computer",
				help:  "Three engine tiers, beginner to pro. The default game is enter all the way.",
				value: gamestore.VersusBot,
			},
			{
				label: "someone at this keyboard",
				help:  "Two players taking turns on one machine, on one board.",
				value: gamestore.Hotseat,
			},
			{
				label: "someone over the network",
				help:  "A direct connection, a relay, or a correspondence game played by exchanging codes.",
				value: gamestore.Remote,
			},
		},
		sel:    sel,
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			k, _ := m.form.(*chooser).opts[i].value.(gamestore.Kind)
			// Correspondence is the network family's third method, so coming
			// back through this question with one pending keeps its answers
			// the way any unchanged answer is kept.
			changed := m.pending.kind != k &&
				!(k == gamestore.Remote && m.pending.kind == gamestore.Correspondence)
			if changed {
				m.pending = m.newGameSetup(k)
			}
			steps := []stepFn{stepWho}
			switch k {
			case gamestore.VersusBot:
				steps = append(steps, stepTier, stepSide, stepRules, stepSize)
			case gamestore.Hotseat:
				steps = append(steps, stepOpponent, stepSide, stepRules, stepSize)
			default:
				steps = append(steps, stepNetMethod)
			}
			m.steps = steps
			return m.runStep(1)
		},
	}
	return nil
}

// newGameSetup is the state of a game's answers before any question is asked.
//
// It starts at the stored defaults, which with nothing stored are the defaults
// the command line uses, so that the answer already highlighted in each
// chooser is the configured one: pressing enter through the questions must
// give the same game as `twixtui play bot` on a fresh machine, and the game
// the settings panel describes on any other. Every chooser then selects
// whatever the setup already holds, which is also what makes walking
// backwards through the form keep the answers given so far.
func (m *Menu) newGameSetup(k gamestore.Kind) gameSetup {
	return gameSetup{kind: k, rules: m.defaults.ruleset(), tier: bot.Intermediate}
}

func (m *Menu) openTutorial() tea.Cmd {
	// The tutorial owns its own lesson list, so the menu hands over without a
	// lesson rather than offering a second chooser for the same thing.
	sc, err := NewTutorialScreen(m.deps, "")
	if err != nil {
		return Fail(err)
	}
	return Open(sc)
}

// openLearn groups the three ways the product teaches: doing, reading, and
// the guided tour. They are one entry on the front screen because they are
// visited rarely and mostly early, and each of the three explains itself here.
func (m *Menu) openLearn() tea.Cmd {
	m.form = &chooser{
		title: "Learn to play",
		opts: []menuOption{
			{
				label: "The tutorial",
				help:  "The rules and the moves, taught on a real board you play on.",
				value: (*Menu).openTutorial,
			},
			{
				label: "The rules",
				help:  "The written rules, to read here: the same text `twixtui rules show` prints.",
				value: (*Menu).openRules,
			},
			{
				label: "The introduction",
				help:  "The tour twixtui gives on first run, played again.",
				value: (*Menu).openIntro,
			},
		},
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			run, ok := m.form.(*chooser).opts[i].value.(func(*Menu) tea.Cmd)
			if !ok {
				return nil
			}
			return run(m)
		},
	}
	return nil
}

// openRules shows the rules document in the scrolling panel. The layout is
// built once, when the panel opens, to the width the panel has then, capped at
// 78 columns because a paragraph the width of a cinema terminal is harder to
// read than a column. The price of laying out once is that a resize while
// reading keeps the wrap the panel opened with — the same trade the standings
// make — and closing and reopening re-flows.
func (m *Menu) openRules() tea.Cmd {
	m.form = &scrollForm{title: "The rules", body: rulesLines(docs.Rules, min(max(m.width, 20), 78))}
	return nil
}

// rulesLines lays the rules document out for the panel. The source is
// markdown hard-wrapped for the repository page, so wrapping its lines one by
// one leaves a ragged half-line after every second one; instead prose lines
// are joined back into their paragraph and the paragraph is wrapped whole. A
// line that opens a structure of its own — a heading, a list item, a quote —
// starts a fresh paragraph with its continuation lines joined to it, and a
// fenced code block is notation, kept exactly as written.
func rulesLines(text string, width int) []string {
	var out []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, wrapText(strings.Join(para, " "), width)...)
			para = nil
		}
	}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			inFence = !inFence
			out = append(out, line)
		case inFence:
			out = append(out, line)
		case trimmed == "":
			flush()
			out = append(out, "")
		default:
			if opensBlock(trimmed) {
				flush()
			}
			para = append(para, trimmed)
		}
	}
	flush()
	return out
}

// opensBlock reports whether a markdown line begins a block of its own rather
// than continuing the paragraph above it.
func opensBlock(line string) bool {
	switch line[0] {
	case '#', '-', '*', '>', '|':
		return true
	}
	// An ordered list item: digits, a dot, a space.
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

// openIntro replays the introduction on top of the menu, exactly as a first
// run shows it, so what a player deliberately asks to see again is the thing
// itself and not a summary of it.
func (m *Menu) openIntro() tea.Cmd {
	sc, err := NewOnboarding(m.deps, m.player)
	if err != nil {
		return Fail(err)
	}
	return Open(sc)
}

// openWatch lists the games that are over, to be stepped through move by
// move. Imported games belong here whatever their stored state says: they
// were brought in to be looked at, and the continue list refuses them for
// exactly that reason.
func (m *Menu) openWatch() tea.Cmd {
	var done []gamestore.Saved
	for _, sv := range m.deps.Games.List() {
		if sv.Finished || sv.Kind == gamestore.Imported {
			done = append(done, sv)
		}
	}
	if len(done) == 0 {
		m.message = "No finished games on this machine. Finish one and it will be here to watch."
		return nil
	}
	now := m.deps.Clock()
	opts := make([]menuOption, 0, len(done))
	for _, sv := range done {
		opts = append(opts, menuOption{label: savedRow(now, sv), value: sv, help: watchHelp(sv)})
	}
	m.form = &chooser{
		title:  "Watch a finished game",
		opts:   opts,
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			sv, ok := m.form.(*chooser).opts[i].value.(gamestore.Saved)
			if !ok {
				return nil
			}
			sc, err := NewReplayScreen(m.deps, sv)
			if err != nil {
				m.message = err.Error()
				return nil
			}
			return Open(sc)
		},
	}
	return nil
}

// watchHelp explains a finished game in the row's own terms: what kind of game
// it was and how it ended. A record that will not load is still listed — the
// row above already is — and choosing it is what explains why.
func watchHelp(sv gamestore.Saved) string {
	g, err := sv.Game()
	if err != nil {
		return "A stored game whose record would not load; choosing it says why."
	}
	kind := "A " + string(sv.Kind) + " game"
	if sv.Kind == gamestore.Imported {
		kind = "An imported game"
	}
	return kind + ", " + describeOutcome(g.Result()) + ". Step through it move by move."
}

// openSettings gathers the choices a player makes once and then forgets:
// colours, what a new game's form starts from, whether hints are offered, and
// who is playing. The list is rebuilt on every visit so that each row can name
// the value now in force, which is what makes the panel readable as a summary
// before anything is opened.
func (m *Menu) openSettings() tea.Cmd {
	rs := m.defaults.ruleset()
	hints := "offered"
	if !m.defaults.hintsOffered() {
		hints = "not offered"
	}
	m.form = &chooser{
		title: "Settings",
		opts: []menuOption{
			{
				label: "Colours — " + m.deps.Theme.Name,
				help:  "The colour scheme the board and the panels are drawn in.",
				value: (*Menu).openThemes,
			},
			{
				label: "Rules — " + rs.PresetName(),
				help:  "What a new game's rules question starts at. Any game can still answer differently.",
				value: (*Menu).openDefaultRules,
			},
			{
				label: fmt.Sprintf("Board — %dx%d", rs.Size, rs.Size),
				help:  "What a new game's board question starts at.",
				value: (*Menu).openDefaultSize,
			},
			{
				label: "Hints — " + hints,
				help:  "Whether a game against the computer offers advice on your turn.",
				value: (*Menu).openHints,
			},
			{
				label: "Profile — " + m.player,
				help:  "Play as somebody else on this machine.",
				value: (*Menu).switchProfile,
			},
		},
		sel:    m.settingsSel,
		cancel: closeForm,
		pick: func(m *Menu, i int) tea.Cmd {
			run, ok := m.form.(*chooser).opts[i].value.(func(*Menu) tea.Cmd)
			if !ok {
				return nil
			}
			m.settingsSel = i
			return run(m)
		},
	}
	return nil
}

// reopenSettings is the way back from a settings question, so that answering
// one lands on the settings list — rebuilt, and therefore naming the value
// just chosen — rather than on the front screen.
func reopenSettings(m *Menu) tea.Cmd {
	return m.openSettings()
}

// storeDefaults writes the defaults and reports what was decided. The choice
// holds for this run even when the disk refuses it — the same judgement the
// colour chooser applies — because saying the setting failed is better than
// pretending it was stored, and better again than refusing the choice.
func (m *Menu) storeDefaults(what string) {
	if err := m.defaults.save(m.deps.ConfigDir); err != nil {
		m.message = what + " for now, but the choice could not be saved: " + err.Error()
		return
	}
	m.message = what + "."
}

// openDefaultRules asks which ruleset a new game's form should start at.
func (m *Menu) openDefaultRules() tea.Cmd {
	names := game.PresetNames()
	current := m.defaults.ruleset().PresetName()
	opts := make([]menuOption, 0, len(names))
	sel := 0
	for i, n := range names {
		if n == current {
			sel = i
		}
		opts = append(opts, menuOption{label: n, help: game.PresetSummary(n), value: n})
	}
	m.form = &chooser{
		title:  "Which rules should a new game start with?",
		opts:   opts,
		sel:    sel,
		cancel: reopenSettings,
		pick: func(m *Menu, i int) tea.Cmd {
			name, _ := m.form.(*chooser).opts[i].value.(string)
			m.defaults.Rules = name
			m.storeDefaults("New games start with " + name + " rules")
			return m.openSettings()
		},
	}
	return nil
}

// openDefaultSize asks how big a new game's board should start.
func (m *Menu) openDefaultSize() tea.Cmd {
	current := m.defaults.ruleset().Size
	opts := make([]menuOption, 0, len(boardSizes))
	sel := 0
	for i, n := range boardSizes {
		if n == current {
			sel = i
		}
		opts = append(opts, menuOption{
			label: fmt.Sprintf("%dx%d", n, n),
			help:  boardSizeHelp(n),
			value: n,
		})
	}
	m.form = &chooser{
		title:  "How big should a new game's board start?",
		opts:   opts,
		sel:    sel,
		cancel: reopenSettings,
		pick: func(m *Menu, i int) tea.Cmd {
			n, _ := m.form.(*chooser).opts[i].value.(int)
			m.defaults.Size = n
			m.storeDefaults(fmt.Sprintf("New games start on %dx%d", n, n))
			return m.openSettings()
		},
	}
	return nil
}

// openHints asks whether a game against the computer offers advice. Only bot
// games are affected, because a hint is only meaningful when there is an
// engine to ask (R15), and that is worth saying where the choice is made.
func (m *Menu) openHints() tea.Cmd {
	sel := 0
	if !m.defaults.hintsOffered() {
		sel = 1
	}
	m.form = &chooser{
		title: "Should games against the computer offer hints?",
		opts: []menuOption{
			{
				label: "offered",
				help:  "On your turn, ? asks the engine you are playing what it would do.",
				value: true,
			},
			{
				label: "not offered",
				help:  "No advice. Games against people never had any: there is no engine to ask.",
				value: false,
			},
		},
		sel:    sel,
		cancel: reopenSettings,
		pick: func(m *Menu, i int) tea.Cmd {
			on, _ := m.form.(*chooser).opts[i].value.(bool)
			if on {
				// Offered is the default, so it is stored as nothing at all:
				// a file that says only what differs from the documentation
				// cannot disagree with it.
				m.defaults.Hints = nil
				m.storeDefaults("Hints are offered against the computer")
			} else {
				off := false
				m.defaults.Hints = &off
				m.storeDefaults("Hints are off")
			}
			return m.openSettings()
		},
	}
	return nil
}

// switchProfile opens the profile picker.
//
// It is opened on top of the menu rather than replacing it, and both ways out of
// it come back to the settings list it was reached from. Replacing the menu left
// the picker with no way back at all: its only exits were choosing a profile or
// ending the program, so escape did nothing and nothing on screen named a way
// out, while every other settings row escaped back to the list.
func (m *Menu) switchProfile() tea.Cmd {
	m.reopenOnReveal = func(m *Menu) { _ = m.openSettings() }
	picker := NewPicker(m.deps, "Who is playing?").Cancelled(func() tea.Cmd {
		return Done(DoneMsg{})
	})
	return Open(picker)
}

func (m *Menu) quit() tea.Cmd { return Quit() }

// revealed is called when a screen that was covering the menu has finished.
//
// Every panel the menu opens is built from what the profile, game and
// leaderboard stores said at the moment it was opened, and the screen that just
// closed is exactly what changes them: a game gets played, finished, rated. The
// saved-game list was the visible case — a game resigned in the screen above was
// still offered for resumption by the list underneath, at the position it held
// before the resignation — but the leaderboard panel and the profile list are
// stale in the same way. Dropping the panel puts the player back on the menu,
// where reopening it reads the stores again.
func (m *Menu) revealed() {
	m.form = nil
	m.message = ""
	// A screen opened from a settings row returns to that row rather than to the
	// front, because a player who went two levels down to change one thing has
	// not asked to be put back at the top. The panel is rebuilt rather than
	// restored, which is the point of dropping it: choosing a profile is exactly
	// what changes the row that names the current one.
	if reopen := m.reopenOnReveal; reopen != nil {
		m.reopenOnReveal = nil
		reopen(m)
	}
}

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
		switch sv.Kind {
		case gamestore.Remote:
			// A live network game cannot be picked up without reconnecting,
			// and the game screen refuses a remote seat with no session.
			o.disabled = true
		case gamestore.Imported:
			// Somebody else's game, read in to be looked at. Its players are
			// not this machine's players, so playing on in it would mean taking
			// a seat that belongs to one of them and then claiming the result.
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
	switch sv.Kind {
	case gamestore.Remote:
		return "A network game needs the connection back: host or join again from the network menu."
	case gamestore.Correspondence:
		return "A correspondence game: it opens on the board with the code exchange, whoever is to move."
	case gamestore.Imported:
		return "A game imported from elsewhere. It can be replayed and looked at, but not played on: the seats belong to the two players named in it."
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

// openThemes offers the colour schemes, each shown rather than only named: a
// player used to have to adopt a scheme, start a game and look at the board
// before finding out what it did.
func (m *Menu) openThemes() tea.Cmd {
	sample := newThemeSample()
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
		cancel: reopenSettings,
		preview: func(_ *Menu, st *ui.Styles, o menuOption, width, height int) []string {
			t, ok := o.value.(theme.Theme)
			if !ok {
				return nil
			}
			return sample.lines(st, t, width, height)
		},
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
			// The new scheme is adopted here as well as through the message,
			// because the settings list rebuilt on the next line names the
			// scheme in force and must not name the old one for a frame.
			m.deps.Theme = t
			m.deps.Styles = &styles
			m.openSettings()
			return func() tea.Msg { return ThemeChangedMsg{Theme: t, Styles: &styles} }
		},
	}
	return nil
}

// themeSample is the position the colour previews are drawn from. It is built
// once per visit to the chooser rather than per frame, since building it means
// playing moves through the engine.
//
// The preview is drawn by the real board renderer from a real position, which
// is what stops it drifting from what adopting the scheme produces: a
// hand-drawn strip of swatches would be a second, unverified picture of the
// same colours, and could go on looking right after the board stopped agreeing
// with it.
type themeSample struct {
	g  *game.Game
	bv ui.BoardView
}

// newThemeSample plays the sample position: two pegs and a link for each
// player, an empty hole under the cursor, a highlighted hole and a last move.
// Between them those use every colour a scheme names for the board, which is
// the point — a preview that left one out would be a preview of something else.
//
// The board is the smallest legal size so that the whole of it fits under the
// list without scrolling. A sample that cannot be built returns nil and the
// chooser simply shows no preview: a menu that would not open because a
// decoration failed is worse than a menu without the decoration.
func newThemeSample() *themeSample {
	rs := game.Std
	rs.Size = game.MinSize
	g, err := game.New(rs)
	if err != nil {
		return nil
	}
	// Vertical moves first, so the moves alternate V, H, V, H. The second peg
	// of each pair is a knight's move from the first, which is what makes the
	// link appear, and the two links do not cross.
	for _, p := range []game.Point{
		{Col: 2, Row: 1}, {Col: 1, Row: 3}, {Col: 3, Row: 3}, {Col: 3, Row: 4},
	} {
		if _, err := g.PlayPeg(p); err != nil {
			return nil
		}
	}
	return &themeSample{
		g: g,
		bv: ui.BoardView{
			Scale:        ui.Compact,
			ShowCursor:   true,
			Cursor:       game.Point{Col: 1, Row: 1},
			ShowLastMove: true,
			LastMove:     game.Point{Col: 3, Row: 4},
			Highlights:   []game.Point{{Col: 4, Row: 2}},
		},
	}
}

// lines draws the sample in scheme t, in at most height rows.
//
// active is the style set the rest of the screen is drawn in, and it decides
// whether any colour is emitted at all: with colour off — --no-color, NO_COLOR,
// output that is not a terminal, or the monochrome scheme chosen — this screen
// emits none either, because a preview that answered --no-color with escape
// sequences would be a worse fault than the one it fixes. Every scheme then
// draws the identical board, which is the truth about them rather than a
// failure of the preview: they differ in colour alone, and the note says so.
func (ts *themeSample) lines(active *ui.Styles, t theme.Theme, width, height int) []string {
	if ts == nil || active == nil || width < 1 || height < 1 {
		return nil
	}
	st := ui.StylesFor(t)
	var note []string
	if active.Plain {
		st = ui.PlainStyles()
		note = appendWrapped(nil, active, &active.Label,
			"Colour is off, so every scheme draws this same board; your choice applies wherever colour is on.", width)
	}
	// The sample is drawn whole or not at all. Cropping it takes rows off the
	// bottom, which is where the links are, and the links are most of what a
	// scheme is judged on; a note reading "this same board" beside no board at
	// all is worse still. A panel with no room for both shows the list alone,
	// which is the same order of preference the panel itself applies.
	w, h := ts.bv.Scale.BlockSize(ts.g.Size())
	if width < w || height < h+len(note) {
		return nil
	}
	return clampLines(append(ts.bv.Render(ts.g, &st, width, h), note...), height)
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
			// Every preset carries the standard size, but how big the board
			// is belongs to the next question — and to the stored default the
			// setup opened with — so the preset's own size must not overwrite
			// the answer already held.
			if m.pending.rules.Size != 0 {
				rs.Size = m.pending.rules.Size
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

// stepNetMethod asks in which of the three ways the game crosses the machine
// boundary: a direct connection, a relay, or no connection at all — codes
// exchanged by hand, at whatever pace the two of you keep. The live methods
// split by which end this is, so the chooser offers six rows for the three.
func stepNetMethod(m *Menu) tea.Cmd {
	type method struct {
		role  netplay.Role
		relay bool
		// corr marks the correspondence rows; host mints an invitation and
		// guest accepts one.
		corr bool
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
			{
				label: "start a correspondence game",
				help:  "No connection: you exchange short codes at your own pace, and you get an invitation to send.",
				value: method{role: netplay.Host, corr: true},
			},
			{
				label: "accept a correspondence invitation",
				help:  "They sent you an invitation code; paste it and the game lives on this machine too.",
				value: method{role: netplay.Guest, corr: true},
			},
		},
		cancel: backOneStep,
		pick: func(m *Menu, i int) tea.Cmd {
			mode, _ := m.form.(*chooser).opts[i].value.(method)
			m.pending.role = mode.role
			m.pending.relay = ""
			m.pending.target = ""

			// The method chooser stays as question one, after stepWho, so that
			// escape from the next question comes back here rather than to the
			// front list.
			steps := []stepFn{stepWho, stepNetMethod}
			switch {
			case mode.corr && mode.role == netplay.Host:
				m.pending.kind = gamestore.Correspondence
				steps = append(steps, stepSide, stepRules, stepSize, stepCorrInvite)
			case mode.corr:
				m.pending.kind = gamestore.Correspondence
				steps = append(steps, stepCorrJoin)
			default:
				m.pending.kind = gamestore.Remote
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
			}
			m.steps = steps
			return m.runStep(2)
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
		note:   "Case, dashes and spaces are ignored, so paste it however it arrived.",
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

// the correspondence form.
//
// A correspondence game is created whole, on this machine, the moment its
// invitation exists — the command line's play correspondence does the same —
// so the two steps below end by putting a game in the store and opening its
// board, not by connecting anything.

// corrUnknownOpponent stands in for a player whose name this end cannot
// learn: an invitation is open, and nothing that comes back carries a name.
// It must read exactly as the command line's unknownOpponent
// (internal/cli/correspondence.go) spells it, because the two paths create
// the same kind of game and the listings must not spell the missing name two
// ways.
const corrUnknownOpponent = "an unnamed opponent"

// corrStoreID turns the identifier an invite carries into the one this side
// stores the game under and binds its codes to: lower-cased, because on a
// case-insensitive filesystem two identifiers differing only in case would be
// one file. The rule is the command line's (internal/cli/correspondenceID),
// applied here for the same invites, and both ends derive the digest their
// codes carry from this stored form.
func corrStoreID(inviteID string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(inviteID))
	if err := gamestore.ValidateID(id); err != nil {
		return "", fmt.Errorf("that invitation names a game this build cannot store: %w", err)
	}
	return id, nil
}

// stepCorrInvite is the step answered by the machine rather than the player:
// it mints the invitation, saves the game, and shows what to send. The game
// goes on disk before the invitation is shown, because an invitation is only
// worth sending if the game it names survives this terminal closing.
func stepCorrInvite(m *Menu) tea.Cmd {
	fail := func(err error) tea.Cmd {
		m.form = nil
		m.message = err.Error()
		return nil
	}
	invite, err := netplay.NewInvite(m.pending.rules, m.pending.side, m.player)
	if err != nil {
		return fail(err)
	}
	code, err := netplay.EncodeInvite(invite)
	if err != nil {
		return fail(err)
	}
	id, err := corrStoreID(invite.ID)
	if err != nil {
		return fail(err)
	}
	g, err := game.New(m.pending.rules)
	if err != nil {
		return fail(err)
	}
	rec, err := g.Record()
	if err != nil {
		return fail(err)
	}
	sv := gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Correspondence,
		Player:   m.player,
		Side:     m.pending.side.String(),
		Opponent: corrUnknownOpponent,
		Record:   rec.Encode(),
	}
	if err := m.deps.Games.Put(sv); err != nil {
		return fail(err)
	}
	m.form = &inviteForm{saved: sv, code: code}
	return nil
}

// stepCorrJoin takes the invitation the opponent sent and accepts it.
func stepCorrJoin(m *Menu) tea.Cmd {
	m.form = &textForm{
		title:  "Their invitation",
		label:  "the code they sent you",
		note:   "Case, dashes, spaces and line breaks are ignored, so paste it however it arrived.",
		value:  m.pending.target,
		cancel: backOneStep,
		submit: func(m *Menu, v string) tea.Cmd {
			v = strings.TrimSpace(v)
			if v == "" {
				m.message = "Paste the invitation they sent you."
				return nil
			}
			// Kept so that walking back to this question keeps the paste.
			m.pending.target = v
			return m.acceptInvite(v)
		},
	}
	return nil
}

// acceptInvite stores the invited game and opens its board. A code that does
// not decode leaves the player on the form, where the complaint is beside the
// field it is about.
func (m *Menu) acceptInvite(code string) tea.Cmd {
	invite, err := netplay.DecodeInvite(code)
	if err != nil {
		m.message = err.Error()
		return nil
	}
	id, err := corrStoreID(invite.ID)
	if err != nil {
		m.message = err.Error()
		return nil
	}
	if _, err := m.deps.Games.Get(id); err == nil {
		m.message = "You already have game " + id + "; it is under Continue a saved game."
		return nil
	}
	g, err := game.New(invite.Rules)
	if err != nil {
		m.message = err.Error()
		return nil
	}
	rec, err := g.Record()
	if err != nil {
		m.message = err.Error()
		return nil
	}
	host := invite.HostName
	if host == "" {
		host = corrUnknownOpponent
	}
	sv := gamestore.Saved{
		ID:       id,
		Kind:     gamestore.Correspondence,
		Player:   m.player,
		Side:     invite.GuestSide().String(),
		Opponent: host,
		Record:   rec.Encode(),
	}
	if err := m.deps.Games.Put(sv); err != nil {
		m.message = err.Error()
		return nil
	}
	m.form = nil
	cfg, err := m.correspondenceConfig(sv)
	if err != nil {
		m.message = err.Error()
		return nil
	}
	return m.start(cfg)
}

// correspondenceConfig rebuilds a stored correspondence game for the game
// screen, whose exchange panel is where codes are produced and pasted. It is
// the same configuration the command line's play correspondence builds, which
// is what keeps a game openable from either door.
func (m *Menu) correspondenceConfig(sv gamestore.Saved) (GameConfig, error) {
	g, err := sv.Game()
	if err != nil {
		return GameConfig{}, err
	}
	side, err := game.ParsePlayer(sv.Side)
	if err != nil {
		return GameConfig{}, fmt.Errorf("saved game %s records an unreadable side %q: %w", sv.ID, sv.Side, err)
	}
	player := sv.Player
	if player == "" {
		player = m.player
	}
	return GameConfig{
		Kind:  gamestore.Correspondence,
		Rules: g.Rules(),
		Seats: map[game.Player]Seat{
			side: {Profile: player, Label: player},
			// BareName rather than DisplayName: the label is fed back through
			// RemoteName on the next save, so it has to round-trip, not read
			// well. The screen saves the opponent under the recorded name and
			// the game is reopened from that save every turn, so the prefix
			// has to come off again here or it accumulates.
			side.Opponent(): {Remote: true, Label: leaderboard.BareName(sv.Opponent)},
		},
		Codes:   true,
		Resume:  &sv,
		StoreID: sv.ID,
	}, nil
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
		// the engine already in the game is the one to ask. Whether it is
		// asked at all is the player's stored choice.
		cfg.Hints = m.defaults.hintsOffered()
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
//
// The row handed in is a snapshot taken when the list was built, so the store is
// read again here: by now the game may have been finished, or played on in
// another window. The snapshot is used only if the store no longer has it.
func (m *Menu) resumeConfig(sv gamestore.Saved) (GameConfig, error) {
	if fresh, err := m.deps.Games.Get(sv.ID); err == nil {
		sv = fresh
	}
	g, err := sv.Game()
	if err != nil {
		return GameConfig{}, err
	}
	if sv.Finished || g.Result().Over() {
		return GameConfig{}, errors.New("that game is over: start a new one, or look at it with 'twixtui game show'")
	}
	if sv.Kind == gamestore.Correspondence {
		// A correspondence game resumes into the exchange, not into a seat
		// map built from the opponent's recorded name: that name is a remote
		// one, and the branch below for remote names refuses to resume — a
		// rule that is right for a live game and wrong for one that never had
		// a connection to lose.
		return m.correspondenceConfig(sv)
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
		cfg.Hints = m.defaults.hintsOffered()
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
	return Open(sc)
}

// forms.

// listMove is the movement every list on this screen shares: whatever navKeys
// answers, plus the board's letters.
//
// The letters are translated into the pair navKeys already treats as one step
// rather than being given their own arithmetic, so what a step means — where it
// wraps, what it does to an empty list — stays defined in exactly one place.
func (m *Menu) listMove(key string, sel, n int) (int, bool) {
	switch {
	case matchesKey(key, m.listUp):
		key = keyPrev
	case matchesKey(key, m.listDown):
		key = keyNext
	}
	return m.nav.move(key, sel, n)
}

// letterKeys returns the letter forms of a movement binding, which is the
// complement of what arrowKeys takes. Both read the shared keymap rather than
// naming keys of their own, so rebinding the board's movement moves the menu's
// with it and no hint can describe a key the screen does not answer.
func letterKeys(km ui.Keymap, a ui.Action) []string {
	b, ok := km.ByAction(ui.CtxBoard, a)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(b.Keys))
	for _, k := range b.Keys {
		if len([]rune(k)) == 1 {
			keys = append(keys, k)
		}
	}
	return keys
}

// movementHint names the keys a list moves by. The letters are named only when
// the bindings really have them, since a keymap without them would leave the
// arrows as the whole answer.
func (m *Menu) movementHint() string {
	hint := keyLabel(m.nav.up...) + "/" + keyLabel(m.nav.down...)
	up, down := keyLabel(m.listUp...), keyLabel(m.listDown...)
	if up != "" && down != "" {
		hint += " or " + up + "/" + down
	}
	return hint
}

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
	// preview shows the highlighted option instead of only naming it, for a
	// question whose answers can be shown at all. It is asked for at most
	// height rows and may return fewer or none. Nil on every other chooser.
	preview func(m *Menu, st *ui.Styles, o menuOption, width, height int) []string
}

func (c *chooser) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	key := press.String()
	if sel, moved := m.listMove(key, c.sel, len(c.opts)); moved {
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
	var preview func(width, height int) []string
	if c.sel >= 0 && c.sel < len(c.opts) {
		help = c.opts[c.sel].help
		if c.preview != nil {
			sel := c.opts[c.sel]
			preview = func(width, height int) []string {
				return c.preview(m, st, sel, width, height)
			}
		}
	}
	return listPanel(st, c.title, rows, c.sel, preview, m.message, help, width, height)
}

func (c *chooser) hints(m *Menu) []string {
	if c == m.list {
		// The front list has nothing to back out to, so its line does not
		// offer esc, and it names the plain quit letter — the way out the
		// board teaches — rather than only the control form.
		return []string{
			m.moveHint + " move",
			keyLabel(m.nav.confirm...) + " choose",
			m.frontQuitHint,
			keyPrev + "/" + keyNext + " move",
		}
	}
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

// inviteForm shows a freshly minted correspondence invitation. Enter opens
// the board; escape goes to the front list rather than back one question,
// because the game already exists and walking the form forward again would
// mint a second one — the message it leaves says where the first one lives.
type inviteForm struct {
	saved gamestore.Saved
	code  string
}

func (f *inviteForm) key(m *Menu, press tea.KeyPressMsg) tea.Cmd {
	key := press.String()
	switch {
	case m.nav.isCancel(key):
		m.form = nil
		m.message = "Game " + f.saved.ID + " is saved; it is under Continue a saved game."
		return nil
	case m.nav.isConfirm(key):
		m.form = nil
		cfg, err := m.correspondenceConfig(f.saved)
		if err != nil {
			m.message = err.Error()
			return nil
		}
		return m.start(cfg)
	}
	return nil
}

func (f *inviteForm) lines(m *Menu, st *ui.Styles, width, height int) []string {
	out := make([]string, 0, height)
	out = append(out, paint(st, &st.PanelTitle, "Send your opponent this invitation"))
	out = append(out, "")
	// The code comes first and unshortened: it is the one thing on this panel
	// that must survive being copied, and a short terminal should lose the
	// explanation, not the code. wrapText cuts it into width-sized pieces
	// rather than clipping, and the pieces are enough — spaces and line breaks
	// are ignored when a code is pasted.
	for _, l := range wrapText(f.code, width) {
		out = append(out, paint(st, &st.PanelText, l))
	}
	out = append(out, "")
	out = appendWrapped(out, st, &st.PanelText,
		fmt.Sprintf("Game %s. You play %s.", f.saved.ID, f.saved.Side), width)
	out = appendWrapped(out, st, &st.PanelText,
		"They accept it with: twixtui play correspondence --join <the invitation>", width)
	out = append(out, "")
	out = appendWrapped(out, st, &st.Label,
		"Copy the invitation before you go on: it is shown only here.", width)
	return clampLines(out, height)
}

func (f *inviteForm) hints(m *Menu) []string {
	return []string{
		keyLabel(m.nav.confirm...) + " open the board",
		"esc back to the menu",
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
	if focus, moved := m.listMove(key, f.focus, len(f.body)); moved {
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
// list, an optional preview of the highlighted entry, and a fixed-height
// explanation of it underneath. The explanation's height is fixed so that the
// list does not shift up and down as the selection moves.
func listPanel(st *ui.Styles, title string, rows []string, sel int, preview func(width, height int) []string, message, help string, width, height int) []string {
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
	// The preview is asked for whatever is left once the list has all its rows,
	// the explanation its own, and one blank line separates the two blocks. It
	// is the block a short panel gives up, because an entry can still be chosen
	// by name with nothing shown, and it is asked for its size rather than
	// clipped afterwards so that what it draws is what fits.
	var shown []string
	previewH := 0
	if preview != nil {
		if room := height - len(out) - helpH - len(rows) - 1; room > 0 {
			if shown = preview(width, room); len(shown) > 0 {
				previewH = len(shown) + 1
			}
		}
	}
	listH := max(1, height-len(out)-helpH-previewH)
	out = append(out, window(rows, listH, sel)...)
	if previewH > 0 {
		out = append(out, "")
		out = append(out, shown...)
	}
	if helpH > 0 {
		for len(out) < height-helpH {
			out = append(out, "")
		}
		out = append(out, clampLines(appendWrapped(nil, st, style, text, width), helpH)...)
	}
	return clampLines(out, height)
}
