package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/ui"
)

// LeaderboardScreen is the standings, one step inside them a participant's own
// games, and one step inside those the game itself.
//
// It is a screen rather than a panel on the menu because it is a place the
// player moves about in and leaves in stages. Choosing a player has to come
// back to the standings, and watching a game has to come back to the list it
// was chosen from; the shell's stack gives the last of those — the replay is
// opened on top and returns here — and the two lists inside this screen keep
// their own position, so every step out lands on what the step in was taken
// from.
//
// Nothing here reads a store while drawing. The standings are read when the
// screen is made, a participant's games when that participant is chosen, and
// the saved game behind a row when that row is opened; a frame is built per
// keypress, and a listing that went to disk for every one of them would make
// moving the highlight cost a directory read.
type LeaderboardScreen struct {
	deps Deps
	nav  listKeys

	// people are the rated participants, best first, and bots the fixed-rating
	// opponents listed apart from them. Only a person can be chosen: a tier
	// plays at a rating the program sets and keeps no history of its own, which
	// is why the command line leaves the tiers out of its --player as well.
	people, bots []participant
	shownCounts  map[string]int
	// nameW is the name column, measured over both blocks so that the two line
	// up, and rankW the position column, which is zero while the board does not
	// rank.
	nameW, rankW int
	standSel     int

	// who is the participant whose games are being shown and games their
	// history as they played it, read through Board.History so that every row
	// is oriented to them rather than to whoever recorded it.
	who       participant
	games     []historyRow
	histSel   int
	inHistory bool
	// oppW, outcomeW and sideW are the history's own columns, measured when it
	// is built so that an unusual name or an outcome this build cannot score
	// cannot push the numbers beside it out of line.
	oppW, outcomeW, sideW int

	// message is the last thing that was refused or explained, shown under the
	// list until the next key.
	message string

	standHints, histHints []string

	width, height int
}

// participant is somebody the standings hold a line for.
type participant struct {
	// stored is the name the board records them under, prefix and all. It is
	// the identity: the history is asked for by this, and nothing is ever
	// resolved back out of what was painted on the screen.
	stored string
	// shown is what they are called on screen, with their kind spelled out
	// where two participants would otherwise be drawn as the same line of text.
	shown string
	// kind is what sort of participant they are, in the words the explanation
	// under the list uses.
	kind string
	// rank is their position, or zero where the board does not rank.
	rank          int
	st            leaderboard.Standing
	disambiguated bool
}

// The kinds of participant, which are also what tells two of them apart when
// they are shown under one name: a profile on this machine called
// "Reka (remote)" and a networked opponent called Reka are both drawn as
// "Reka (remote)", and the kind is the difference between them.
const (
	kindLocal  = "on this machine"
	kindPast   = "local history"
	kindRemote = "over the network"
	kindBot    = "a built-in opponent"
)

// historyRow is one game as the participant whose history this is played it.
type historyRow struct {
	// res is the result itself, as Board.History turned it round: the row's
	// identity, including the saved game it names, is this and not the text
	// drawn from it.
	res leaderboard.Result
	// opponent is the other side's name as it is drawn.
	opponent string
	// link is what is known about the saved game this result names. It is
	// worked out when the list is built, and worked out again before anything
	// is opened, so a game that changed under a list already on screen is
	// caught by the opening rather than by the listing.
	link linkState
}

// linkState is how far a result gets towards the game it was recorded from.
type linkState int

const (
	// linkNone is a result that never named a saved game: every row a board
	// written before the link holds, and every path that had no stored game to
	// point at. Which game it meant is not guessed from names and times.
	linkNone linkState = iota
	// linkMissing is a named game that is not on this machine, because it was
	// deleted or was never here.
	linkMissing
	// linkUnreadable is a named game whose record will not decode.
	linkUnreadable
	// linkChanged is a named game holding a record other than the one the
	// result was recorded from.
	linkChanged
	// linkReady is a named game whose stored record is exactly the one the
	// result carries the digest of.
	linkReady
)

// mark is the word at the end of a row, for reading down the list.
func (l linkState) mark() string {
	if l == linkReady {
		return "replay"
	}
	return "no replay"
}

// why says what stops the row being opened, in few enough words to survive the
// two lines a twenty-column pane gives the explanation. The store's own longer
// wording is added after this one, where there is room for it.
func (l linkState) why() string {
	switch l {
	case linkReady:
		return ""
	case linkMissing:
		return "saved game not found"
	case linkUnreadable:
		return "its record will not load"
	case linkChanged:
		return "its record has changed"
	}
	return "unlinked result"
}

// NewLeaderboardScreen reads the standings and prepares the screen.
func NewLeaderboardScreen(d Deps) (Screen, error) {
	if d.Board == nil {
		return nil, errors.New("there is no result log on this machine to show standings from")
	}
	km := shellKeymap(d)
	s := &LeaderboardScreen{deps: d, nav: newListKeys(km)}
	s.buildStandings()
	// The keys cannot change while the screen is up, so the line naming them is
	// built once here rather than per frame. Both lines name the same movement:
	// what differs between the two lists is what choosing and escaping do, and
	// the line says which of the two the player is looking at.
	quit := keyLabel(globalQuitKeys(km)...) + " quit"
	move := s.nav.moveHint() + " move"
	choose := keyLabel(s.nav.confirm...)
	s.standHints = []string{choose + " games", "esc back", move, quit, keyPrev + "/" + keyNext + " move"}
	s.histHints = []string{choose + " replay", "esc standings", move, quit, keyPrev + "/" + keyNext + " move"}
	return s, nil
}

// buildStandings reads the board and lays the two blocks out.
func (s *LeaderboardScreen) buildStandings() {
	board := s.deps.Board.Standings()
	// A position needs somebody to hold it against. With one player it would
	// say only that they are the only one, so the column waits for a second.
	ranked := len(board.Players) > 1
	s.people = make([]participant, 0, len(board.Players))
	for i, st := range board.Players {
		p := s.participantOf(st)
		if ranked {
			p.rank = i + 1
		}
		s.people = append(s.people, p)
	}
	s.bots = make([]participant, 0, len(board.Bots))
	for _, st := range board.Bots {
		s.bots = append(s.bots, s.participantOf(st))
	}
	s.disambiguate()
	s.rankW = 0
	if ranked {
		s.rankW = 3
		for n := len(s.people); n > 0; n /= 10 {
			s.rankW++
		}
	}
	// Measured in terminal cells, which is what padTo pads to: counting runes
	// makes the column too narrow for a fullwidth name and too wide for one
	// carrying combining marks, and the rating column beside it then moves by
	// the difference — the one thing a column of numbers must not do.
	s.nameW = ansi.StringWidth("player")
	for _, block := range [][]participant{s.people, s.bots} {
		for _, p := range block {
			s.nameW = max(s.nameW, ansi.StringWidth(p.shown))
		}
	}
	if s.standSel >= len(s.people) {
		s.standSel = max(0, len(s.people)-1)
	}
}

// participantOf describes one line of the board.
func (s *LeaderboardScreen) participantOf(st leaderboard.Standing) participant {
	// The name comes out of a file this machine's own programs write and a
	// player may edit by hand, so it is made safe before it is drawn: an escape
	// sequence in a name would be obeyed by the terminal rather than shown.
	return participant{
		stored: st.Name,
		shown:  replayLabel(leaderboard.DisplayName(st.Name)),
		kind:   s.kindOf(st.Name),
		st:     st,
	}
}

// kindOf says what sort of participant a stored name belongs to.
func (s *LeaderboardScreen) kindOf(stored string) string {
	switch {
	case leaderboard.IsBot(stored):
		return kindBot
	case strings.HasPrefix(stored, leaderboard.RemotePrefix):
		return kindRemote
	}
	if s.deps.Profiles != nil {
		if _, ok := s.deps.Profiles.Get(stored); ok {
			return kindLocal
		}
	}
	// The log is the history and a profile is only a name to play under today,
	// so a deleted profile keeps its games and its line here.
	return kindPast
}

// disambiguate spells the kind out on every name two participants share.
//
// Two of them really can be drawn as one line of text: a networked opponent
// called Reka is shown as "Reka (remote)", and so is a local profile somebody
// named "Reka (remote)". Selecting the right one is not enough — the row that
// is selected has to say which one it is, or the player is choosing between two
// identical lines and finding out afterwards.
func (s *LeaderboardScreen) disambiguate() {
	s.shownCounts = make(map[string]int, len(s.people)+len(s.bots))
	for _, p := range s.people {
		s.shownCounts[p.shown]++
	}
	for _, p := range s.bots {
		s.shownCounts[p.shown]++
	}
	mark := func(rows []participant) {
		for i := range rows {
			if s.shownCounts[rows[i].shown] > 1 {
				rows[i].shown = qualifiedParticipant(rows[i].stored, rows[i].shown, rows[i].kind)
				rows[i].disambiguated = true
			}
		}
	}
	mark(s.people)
	mark(s.bots)
}

func qualifiedParticipant(stored, shown, kind string) string {
	switch kind {
	case kindRemote:
		return "remote: " + replayLabel(leaderboard.BareName(stored))
	case kindBot:
		return "bot: " + replayLabel(leaderboard.BareName(stored))
	default:
		return "local: " + shown
	}
}

// Init implements tea.Model.
func (s *LeaderboardScreen) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (s *LeaderboardScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		// Only the frame's size changes. Which participant and which game are
		// highlighted are the player's place in the screen, and a resize is not
		// a request to lose it.
		s.width, s.height = m.Width, m.Height
	case ThemeChangedMsg:
		s.deps.Theme = m.Theme
		if m.Styles != nil {
			s.deps.Styles = m.Styles
		}
	case tea.KeyPressMsg:
		return s, s.key(m)
	}
	return s, nil
}

func (s *LeaderboardScreen) key(m tea.KeyPressMsg) tea.Cmd {
	key := m.String()
	if sel, moved := s.nav.move(key, s.selection(), s.rows()); moved {
		s.setSelection(sel)
		s.message = ""
		return nil
	}
	switch {
	case s.nav.isCancel(key):
		if s.inHistory {
			// Back to the standings, which are still exactly where they were:
			// the same participant highlighted, scrolled to the same place.
			s.inHistory = false
			s.message = ""
			return nil
		}
		return Back()
	case s.nav.isConfirm(key):
		if s.inHistory {
			return s.openGame()
		}
		s.openHistory()
		return nil
	}
	return nil
}

func (s *LeaderboardScreen) rows() int {
	if s.inHistory {
		return len(s.games)
	}
	return len(s.people)
}

func (s *LeaderboardScreen) selection() int {
	if s.inHistory {
		return s.histSel
	}
	return s.standSel
}

func (s *LeaderboardScreen) setSelection(i int) {
	if s.inHistory {
		s.histSel = i
		return
	}
	s.standSel = i
}

// openHistory reads the selected participant's results and only their referenced
// saved records. Record reconstruction is deferred until a replay is opened.
func (s *LeaderboardScreen) openHistory() {
	if s.standSel < 0 || s.standSel >= len(s.people) {
		return
	}
	who := s.people[s.standSel]
	rows := s.deps.Board.History(who.stored, 0)
	if len(rows) == 0 {
		s.message = who.shown + " has no recorded games yet."
		return
	}
	s.games = make([]historyRow, 0, len(rows))
	s.oppW, s.outcomeW, s.sideW = ansi.StringWidth("opponent"), ansi.StringWidth("result"), ansi.StringWidth("side")
	for _, r := range rows {
		opponent := replayLabel(leaderboard.DisplayName(r.Opponent))
		if s.shownCounts[opponent] > 1 {
			opponent = qualifiedParticipant(r.Opponent, opponent, s.kindOf(r.Opponent))
		}
		_, state, _ := s.linkedGame(r)
		row := historyRow{res: r, opponent: opponent, link: state}
		s.oppW = max(s.oppW, ansi.StringWidth(row.opponent))
		s.outcomeW = max(s.outcomeW, ansi.StringWidth(replayLabel(string(r.Outcome))))
		s.sideW = max(s.sideW, ansi.StringWidth(replayLabel(r.Side)))
		s.games = append(s.games, row)
	}
	s.who, s.inHistory, s.histSel, s.message = who, true, 0, ""
}

// openGame opens the highlighted row's game for replay, if it really is that
// game.
//
// Everything is checked again here rather than trusted from the listing. A list
// is a snapshot: the game it was built from can be deleted, replaced or edited
// while the player is reading it, and a cached "yes" would then open whatever
// is in the file now. The identifier is checked, the game is fetched by exactly
// that identifier, its record is decoded and its digest compared with the one
// the result carries, and only then is it replayed — by the replay screen,
// which refuses a record that does not lead where it says it does.
func (s *LeaderboardScreen) openGame() tea.Cmd {
	if s.histSel < 0 || s.histSel >= len(s.games) {
		return nil
	}
	row := &s.games[s.histSel]
	sv, state, err := s.linkedGame(row.res)
	// What was just found out replaces what the listing assumed, so the row
	// goes on saying the truth after the refusal as well as during it.
	row.link = state
	if err != nil {
		s.refuse(state, err)
		return nil
	}
	sc, err := NewReplayScreen(s.deps, sv)
	if err != nil {
		row.link = linkUnreadable
		s.refuse(linkUnreadable, err)
		return nil
	}
	s.message = ""
	return Open(sc)
}

// refuse explains why a row would not open. The short reason comes first so
// that it survives a pane narrow enough to give the explanation two lines, and
// the store's own words follow for a pane with room for them.
func (s *LeaderboardScreen) refuse(state linkState, err error) {
	s.message = "No replay: " + state.why()
	if err != nil {
		s.message += " (" + replayLabel(err.Error()) + ")"
	}
}

// linkedGame finds the saved game a result names, and refuses anything that is
// not exactly it.
func (s *LeaderboardScreen) linkedGame(r leaderboard.Result) (gamestore.Saved, linkState, error) {
	if !r.HasIdentity() {
		return gamestore.Saved{}, linkNone, errors.New("this result does not name the game it was recorded from")
	}
	if s.deps.Games == nil {
		return gamestore.Saved{}, linkUnreadable, errors.New("there is no game store on this machine")
	}
	sv, err := s.deps.Games.Get(r.GameID)
	if err != nil {
		if errors.Is(err, gamestore.ErrNotFound) {
			return gamestore.Saved{}, linkMissing, err
		}
		return gamestore.Saved{}, linkUnreadable, err
	}
	rec, err := game.DecodeRecord(sv.Record)
	if err != nil {
		return gamestore.Saved{}, linkUnreadable, err
	}
	if rec.Digest != r.RecordDigest {
		return gamestore.Saved{}, linkChanged, fmt.Errorf("saved game %s holds record %s, not the %s this result was recorded from",
			replayLabel(r.GameID), replayLabel(rec.Digest), replayLabel(r.RecordDigest))
	}
	return sv, linkReady, nil
}

// View implements tea.Model. The shell owns the alternate screen, so only the
// content is set.
func (s *LeaderboardScreen) View() tea.View {
	st := shellStyles(s.deps)
	w, h := s.width, s.height
	var content []string
	hints := s.standHints
	if s.inHistory {
		content, hints = s.historyPanel(st, w, max(0, h-1)), s.histHints
	} else {
		content = s.standingsPanel(st, w, max(0, h-1))
	}
	status := hintLine(w, hints...)
	if ansi.StringWidth(hints[0]+" · "+hints[1]) > w {
		status = hints[0] + " · esc"
		if ansi.StringWidth(status) > w {
			status = keyLabel(s.nav.confirm...) + " · esc"
		}
	}
	return tea.NewView(textFrame(st, w, h, content, paint(st, &st.Status, status)))
}

// standingsPanel draws the board: the people ranked against one another, and
// under them the bots they played, which are not ranked.
func (s *LeaderboardScreen) standingsPanel(st *ui.Styles, width, height int) []string {
	if len(s.people) == 0 && len(s.bots) == 0 {
		return listPanel(st, panelLayout{
			title:   "Leaderboard",
			message: s.message,
			help:    "No games recorded yet. Play one and it will appear here.",
		}, width, height)
	}
	cols := standingColumns(s.rankW, s.nameW, width)
	rows := make([]string, 0, len(s.people))
	for i, p := range s.people {
		marker := "  "
		if i == s.standSel {
			marker = paint(st, &st.Cursor, "> ")
		}
		rows = append(rows, marker+paint(st, &st.PanelText, s.standingLine(p, cols)))
	}
	help := "No rated games yet. The bots below are the ones that have been played."
	if s.standSel >= 0 && s.standSel < len(s.people) {
		help = s.people[s.standSel].detail()
	}
	return listPanel(st, panelLayout{
		title:   "Leaderboard",
		head:    []string{"  " + paint(st, &st.Label, s.standingHead(cols))},
		rows:    rows,
		sel:     s.standSel,
		foot:    s.botBlock(st, cols, width),
		message: s.message,
		help:    help,
	}, width, height)
}

// botBlock is the fixed-rating opponents, listed apart from the people.
//
// A bot's rating is a constant in the program rather than something it won, so
// one column holding both invites a comparison neither number supports: that is
// what made a player who had lost their only game read as the best on the
// machine. It is a block under the list rather than rows in it for the same
// reason, and because a tier has no history of its own to open.
func (s *LeaderboardScreen) botBlock(st *ui.Styles, cols, width int) []string {
	if len(s.bots) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.bots)+2)
	out = append(out, "")
	out = append(out, paint(st, &st.PanelText, truncateText("Bots are not ranked: a tier's rating is fixed, not earned.", width)))
	for _, b := range s.bots {
		out = append(out, "  "+paint(st, &st.Label, s.standingLine(b, cols)))
	}
	return out
}

// standingColumns decides how many of the numbers fit beside the names. The
// decision is made once, for the heading and every row of both blocks, so that
// a narrow pane drops a whole column instead of letting the frame cut each row
// in a different place.
func standingColumns(rankW, nameW, width int) int {
	const (
		marker = 2
		numW   = 7
		most   = 3
	)
	return min(max((width-marker-rankW-nameW)/numW, 0), most)
}

// standingHead names the columns that are being shown.
func (s *LeaderboardScreen) standingHead(cols int) string {
	return s.standingText("#", "player", []string{"rating", "games", "score"}, cols)
}

// standingLine is one participant's row.
func (s *LeaderboardScreen) standingLine(p participant, cols int) string {
	rank := ""
	if p.rank > 0 {
		rank = strconv.Itoa(p.rank)
	}
	return s.standingText(rank, p.shown, []string{
		strconv.Itoa(p.st.Rating),
		strconv.Itoa(p.st.Played),
		// The rate counts half of every draw, which is the quantity the rating
		// is derived from, so the column is a score and not a win rate.
		fmt.Sprintf("%.0f%%", p.st.WinRate*100),
	}, cols)
}

// standingText lays a row out. The position column is left out entirely rather
// than padded to nothing when the board does not rank, so an unranked board has
// no stub where the numbers would be.
func (s *LeaderboardScreen) standingText(rank, name string, nums []string, cols int) string {
	var b strings.Builder
	if s.rankW > 0 {
		b.WriteString(padTo(rank, s.rankW))
	}
	b.WriteString(padTo(name, s.nameW))
	for _, n := range nums[:min(cols, len(nums))] {
		fmt.Fprintf(&b, " %6s", n)
	}
	return b.String()
}

// detail is the sentence under the list while this participant is highlighted.
// The kind comes straight after the name because it is what tells two
// participants drawn under one name apart, and it therefore has to be inside
// the two lines a narrow pane gives this.
func (p participant) detail() string {
	label := p.shown
	if !p.disambiguated {
		label += ", " + p.kind
	}
	return fmt.Sprintf("rating %d · %s · played %d: %d won, %d lost, %d drawn · %.0f%% score",
		p.st.Rating, label, p.st.Played, p.st.Won, p.st.Lost, p.st.Drawn, p.st.WinRate*100)
}

// historyPanel draws one participant's games, most recent first.
func (s *LeaderboardScreen) historyPanel(st *ui.Styles, width, height int) []string {
	fit := s.fitHistory(width)
	rows := make([]string, 0, len(s.games))
	for i, g := range s.games {
		marker := "  "
		if i == s.histSel {
			marker = paint(st, &st.Cursor, "> ")
		}
		// A row that cannot be opened is drawn in the same style as a bot's
		// line: it is here to be read, and it is not a way in.
		style := &st.PanelText
		if g.link != linkReady {
			style = &st.Label
		}
		rows = append(rows, marker+paint(st, style, s.historyLine(g, fit)))
	}
	help := ""
	if s.histSel >= 0 && s.histSel < len(s.games) {
		help = s.games[s.histSel].detail(keyLabel(s.nav.confirm...))
	}
	return listPanel(st, panelLayout{
		title:   "Games — " + s.who.shown,
		head:    []string{"  " + paint(st, &st.Label, s.historyHead(fit))},
		rows:    rows,
		sel:     s.histSel,
		message: s.message,
		help:    help,
	}, width, height)
}

// The shapes of the time column. The board stores UTC, which is what keeps two
// machines' logs comparable, but this is read by one person on one machine: it
// is shown in the same local time as the saved-game list, since rendering the
// stored value as it stands dates every game an offset away from when the
// player remembers playing it. The short form is what a pane too narrow for the
// whole row keeps, because which day it was is what a player is scanning for.
const (
	histWhenLong  = "2006-01-02 15:04"
	histWhenShort = "2006-01-02"
	histWhenTiny  = "01-02"
)

// historyFit is how much of a history row is being drawn.
type historyFit struct {
	// when is the time format the first column uses.
	when    string
	compact bool
	oppW    int
	// side, moves and mark are the columns there is room for, given up in that
	// order from the right. The marker goes first because the highlighted row
	// says the same thing in words underneath it, and the other two are facts
	// about a game rather than what identifies it.
	side, moves, mark bool
}

// fitHistory decides how much of a row fits. As with the standings the decision
// is made once, for the heading and every row, so that a narrow pane drops a
// whole column rather than cutting rows at different places.
func (s *LeaderboardScreen) fitHistory(width int) historyFit {
	const (
		marker = 2
		movesW = 1 + 5
		markW  = 2 + len("no replay")
		gap    = 1
	)
	fit := historyFit{when: histWhenLong}
	core := marker + len(histWhenLong) + gap + s.oppW + gap + s.outcomeW
	if core > width {
		fit.when = histWhenShort
		if marker+len(histWhenShort)+gap+s.oppW+gap+s.outcomeW > width {
			fit.when, fit.compact = histWhenTiny, true
			fit.oppW = max(1, width-marker-len("result")-gap-len(histWhenTiny)-gap)
		}
		return fit
	}
	room := core + gap + s.sideW
	if room > width {
		return fit
	}
	fit.side = true
	if room+movesW > width {
		return fit
	}
	fit.moves = true
	if room+movesW+markW <= width {
		fit.mark = true
	}
	return fit
}

func (s *LeaderboardScreen) historyHead(fit historyFit) string {
	if fit.compact {
		return s.historyText(fit, "when", "vs", "result", "", "", "")
	}
	return s.historyText(fit, "when", "opponent", "result", "side", "moves", "")
}

func (s *LeaderboardScreen) historyLine(g historyRow, fit historyFit) string {
	return s.historyText(fit,
		g.res.Played.Local().Format(fit.when),
		g.opponent,
		replayLabel(string(g.res.Outcome)),
		replayLabel(g.res.Side),
		strconv.Itoa(g.res.Moves),
		g.link.mark(),
	)
}

func (s *LeaderboardScreen) historyText(fit historyFit, when, who, outcome, side, moves, mark string) string {
	if fit.compact {
		return padTo(ansi.Truncate(outcome, len("result"), "…"), len("result")) + " " +
			padTo(when, len(fit.when)) + " " + ansi.Truncate(who, fit.oppW, "…")
	}
	var b strings.Builder
	b.WriteString(padTo(when, len(fit.when)))
	b.WriteString(" " + padTo(who, s.oppW))
	b.WriteString(" " + padTo(outcome, s.outcomeW))
	if fit.side {
		b.WriteString(" " + padTo(side, s.sideW))
	}
	if fit.moves {
		fmt.Fprintf(&b, " %5s", moves)
	}
	if fit.mark && mark != "" {
		b.WriteString("  " + mark)
	}
	return b.String()
}

// detail is the sentence under the list while this row is highlighted. What
// stops a game being opened comes first, in the fewest words that say it, so
// that the reason is still on screen in a pane narrow enough to give this two
// lines: a row that cannot be replayed and does not say why is the one thing
// this list must not do.
func (g historyRow) detail(choose string) string {
	if g.link == linkReady {
		return choose + " replays saved game " + replayLabel(g.res.GameID) + "."
	}
	return "No replay: " + g.link.why() + "."
}
