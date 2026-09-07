package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/ui"
)

// pkProfiles seeds a store with names that are genuinely confusable, so a
// search test proves ranking and not merely that something matched. The sleep
// spaces the timestamps out: most-recently-used order is only testable if the
// store can tell the writes apart.
func pkProfiles(t *testing.T, d Deps, names ...string) {
	t.Helper()
	for i, n := range names {
		if _, err := d.Profiles.Create(n); err != nil {
			t.Fatalf("creating %q: %v", n, err)
		}
		if i < len(names)-1 {
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// pkPicker returns a picker sized for a normal terminal whose choice is
// captured instead of starting a game.
func pkPicker(t *testing.T, d Deps, chosen *string) *Picker {
	t.Helper()
	p := NewPicker(d, "Who is playing?")
	if chosen != nil {
		p.Chosen(func(name string) tea.Cmd {
			*chosen = name
			return nil
		})
	}
	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return p
}

func pkType(t *testing.T, p *Picker, text string) {
	t.Helper()
	for _, r := range text {
		key := string(r)
		if r == ' ' {
			key = "space"
		}
		shellSend(t, p, key)
	}
}

func pkNames(p *Picker) []string {
	out := make([]string, 0, len(p.rows))
	for _, r := range p.rows {
		if r.create {
			out = append(out, "+"+r.name)
			continue
		}
		out = append(out, r.name)
	}
	return out
}

// TestPickerTypoFindsTheIntendedProfile uses the transposition a player
// actually makes, against two names that both begin with the same letter, and
// requires the intended one to be picked out first rather than merely listed.
func TestPickerTypoFindsTheIntendedProfile(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Balint", "Bernadett", "Bella Ackland")
	var chosen string
	p := pkPicker(t, d, &chosen)

	pkType(t, p, "balitn")
	if len(p.rows) == 0 {
		t.Fatalf("no rows for a transposed query; store holds %v", pkNames(p))
	}
	if got := p.rows[0].name; got != "Balint" {
		t.Fatalf("first row is %q, want Balint; rows %v", got, pkNames(p))
	}
	if p.sel != 0 {
		t.Fatalf("selection is %d after typing, want the best match", p.sel)
	}

	shellSend(t, p, "enter")
	if chosen != "Balint" {
		t.Fatalf("chose %q, want Balint", chosen)
	}
}

// TestPickerHighlightsTheMatchedCharacters asserts on the rendered frame with
// colour on, because highlighting is an output property. The second case is the
// one that catches byte offsets used as rune indexes: the two disagree from the
// accented character onwards.
func TestPickerHighlightsTheMatchedCharacters(t *testing.T) {
	styles := ui.DefaultStyles()
	d := shellTestDeps(t)
	d.Styles = &styles
	pkProfiles(t, d, "Balint", "Zsófia")

	for _, tc := range []struct{ query, want string }{
		{"Bal", "Bal"},
		{"fia", "fia"},
	} {
		p := pkPicker(t, d, nil)
		pkType(t, p, tc.query)
		frame := p.View().Content
		if !strings.Contains(frame, styles.Highlight.Render(tc.want)) {
			t.Errorf("query %q: the matched run %q is not highlighted in\n%q", tc.query, tc.want, frame)
		}
	}
}

// TestPickerEmptyQueryBrowsesInMostRecentlyPlayedOrder is R14's browsable list:
// the player who cannot recall the name at all scrolls it, so the order has to
// be the useful one and Touch has to be what maintains it.
func TestPickerEmptyQueryBrowsesInMostRecentlyPlayedOrder(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Anna", "Bernadett", "Cecilia")

	p := pkPicker(t, d, nil)
	if got := pkNames(p); !equalStrings(got, []string{"Cecilia", "Bernadett", "Anna"}) {
		t.Fatalf("empty query lists %v, want newest first", got)
	}

	// Playing as Anna must move her to the top, which is only true if the
	// picker touches the profile it hands on.
	time.Sleep(2 * time.Millisecond)
	var chosen string
	p2 := pkPicker(t, d, &chosen)
	shellSend(t, p2, "down")
	shellSend(t, p2, "down")
	if got := p2.rows[p2.sel].name; got != "Anna" {
		t.Fatalf("two downs selected %q, want Anna", got)
	}
	shellSend(t, p2, "enter")
	if chosen != "Anna" {
		t.Fatalf("chose %q, want Anna", chosen)
	}

	p3 := pkPicker(t, d, nil)
	if got := pkNames(p3); !equalStrings(got, []string{"Anna", "Cecilia", "Bernadett"}) {
		t.Fatalf("after playing as Anna the list is %v, want Anna moved to the front", got)
	}
}

// TestPickerMovesWithBothKeyPairs proves the emacs pair works as well as the
// arrows, and that both wrap.
func TestPickerMovesWithBothKeyPairs(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Anna", "Bernadett", "Cecilia")
	p := pkPicker(t, d, nil)

	shellSend(t, p, "ctrl+n")
	if got := p.rows[p.sel].name; got != "Bernadett" {
		t.Errorf("ctrl+n selected %q, want Bernadett", got)
	}
	shellSend(t, p, "ctrl+p")
	shellSend(t, p, "up")
	if got := p.rows[p.sel].name; got != "Anna" {
		t.Errorf("up from the top selected %q, want the last entry Anna", got)
	}
}

// TestPickerOffersToCreateAnUnknownName requires the offer to be a row of its
// own, marked differently from the profiles, so that choosing and inventing
// cannot be confused.
func TestPickerOffersToCreateAnUnknownName(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Balint")
	var chosen string
	p := pkPicker(t, d, &chosen)

	pkType(t, p, "Zsofia")
	last := p.rows[len(p.rows)-1]
	if !last.create {
		t.Fatalf("no creation row for an unknown name; rows %v", pkNames(p))
	}
	if last.name != "Zsofia" {
		t.Fatalf("the creation row offers %q, want Zsofia", last.name)
	}
	frame := p.View().Content
	if !strings.Contains(frame, "new profile") {
		t.Errorf("the creation row is not distinguished on screen:\n%s", frame)
	}

	p.sel = len(p.rows) - 1
	shellSend(t, p, "enter")
	if chosen != "Zsofia" {
		t.Fatalf("chose %q, want Zsofia", chosen)
	}
	if _, ok := d.Profiles.Get("Zsofia"); !ok {
		t.Error("the profile was not created")
	}
	if _, ok := d.Profiles.Get("Balint"); !ok {
		t.Error("creating a profile disturbed an existing one")
	}
}

// TestPickerExistingNameIsNeverOfferedForCreation covers the case that would
// otherwise split a player's results across two identities: the store treats
// names differing only in case as one profile, and so must the offer.
func TestPickerExistingNameIsNeverOfferedForCreation(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Balint")
	var chosen string
	p := pkPicker(t, d, &chosen)

	pkType(t, p, "balint")
	for _, r := range p.rows {
		if r.create {
			t.Fatalf("offered to create %q although it is the same profile as Balint", r.name)
		}
	}
	shellSend(t, p, "enter")
	if chosen != "Balint" {
		t.Fatalf("chose %q, want the stored spelling Balint", chosen)
	}
	if got := len(d.Profiles.List()); got != 1 {
		t.Fatalf("%d profiles after choosing, want 1", got)
	}
}

// TestPickerNamesTheRuleARejectedNameBroke checks the specific reason, not a
// generic complaint: the store has one sentinel per rule so that this screen can
// say which one.
func TestPickerNamesTheRuleARejectedNameBroke(t *testing.T) {
	tooLong := strings.Repeat("n", profile.MaxNameRunes+1)
	for _, tc := range []struct {
		name  string
		typed string
		want  string
	}{
		{"trailing space", "Bal ", "No spaces at the start or the end."},
		{"too long", tooLong, "Too long: at most 32 characters."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := shellTestDeps(t)
			pkProfiles(t, d, "Bernadett")
			var chosen string
			p := pkPicker(t, d, &chosen)

			pkType(t, p, tc.typed)
			p.sel = len(p.rows) - 1
			if !p.rows[p.sel].create {
				t.Fatalf("expected the creation row to be selectable; rows %v", pkNames(p))
			}
			shellSend(t, p, "enter")

			if p.problem != tc.want {
				t.Fatalf("problem is %q, want %q", p.problem, tc.want)
			}
			if !strings.Contains(p.View().Content, tc.want) {
				t.Errorf("the reason is not on screen:\n%s", p.View().Content)
			}
			if chosen != "" {
				t.Errorf("an invalid name was accepted as %q", chosen)
			}
			if got := len(d.Profiles.List()); got != 1 {
				t.Errorf("%d profiles stored, want the invalid one refused", got)
			}
		})
	}
}

// TestPickerFreshInstallTakesAName is the first-run path: nothing to browse, so
// the screen says so and accepts a name straight away.
func TestPickerFreshInstallTakesAName(t *testing.T) {
	d := shellTestDeps(t)
	var chosen string
	p := pkPicker(t, d, &chosen)

	if len(p.rows) != 0 {
		t.Fatalf("a fresh store produced rows %v", pkNames(p))
	}
	frame := p.View().Content
	if !strings.Contains(frame, "No profiles") {
		t.Errorf("the empty store is not explained:\n%s", frame)
	}

	// Enter with nothing typed explains the rule instead of doing nothing.
	shellSend(t, p, "enter")
	if p.problem != "Type a name first." {
		t.Errorf("empty enter said %q", p.problem)
	}
	if chosen != "" {
		t.Fatalf("an empty name was accepted as %q", chosen)
	}

	pkType(t, p, "Balint")
	shellSend(t, p, "enter")
	if chosen != "Balint" {
		t.Fatalf("chose %q, want Balint", chosen)
	}
	got, ok := d.Profiles.Get("Balint")
	if !ok {
		t.Fatal("the profile was not created")
	}
	if got.LastUsed.IsZero() {
		t.Error("the new profile was not touched, so it will not sort as most recently played")
	}
}

// TestPickerEmitsByReplacingItselfWithTheMenu pins the default wiring: the
// picker hands the name on by replacing itself, which is what makes it usable
// as the program's first screen without a caller supplying anything.
func TestPickerEmitsByReplacingItselfWithTheMenu(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Balint")
	p := NewPicker(d, "")
	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	cmd := shellSend(t, p, "enter")
	if cmd == nil {
		t.Fatal("choosing a profile produced no command")
	}
	done, ok := cmd().(DoneMsg)
	if !ok {
		t.Fatalf("choosing produced %T, want a DoneMsg", cmd())
	}
	menu, ok := done.Next.(*Menu)
	if !ok {
		t.Fatalf("the picker replaced itself with %T, want the menu", done.Next)
	}
	if menu.player != "Balint" {
		t.Errorf("the menu was given player %q", menu.player)
	}
}

// TestPickerOffersEscapeOnlyWhereItGoesSomewhere is F22: the launch footer
// advertised "esc back" on the first screen of the program, with nothing behind
// it to go back to. The property is that the offer and the key agree — escape
// is named exactly where a caller has given it somewhere to go — so it holds
// however the hint is worded.
func TestPickerOffersEscapeOnlyWhereItGoesSomewhere(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Balint")

	root := pkPicker(t, d, nil)
	if frame := root.View().Content; strings.Contains(frame, "esc") {
		t.Errorf("the launch footer offers escape with nothing behind the screen:\n%s", frame)
	}
	if cmd := shellSend(t, root, "esc"); cmd != nil {
		t.Errorf("escape at the root produced %T, want the key ignored", cmd())
	}

	// Embedded in another screen, escape has the screen underneath to return to,
	// and is offered again.
	backed := false
	embedded := NewPicker(d, "Who is playing the other side?").
		Cancelled(func() tea.Cmd {
			backed = true
			return nil
		})
	embedded.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if frame := embedded.View().Content; !strings.Contains(frame, "esc back") {
		t.Errorf("the embedded picker does not offer the escape it answers:\n%s", frame)
	}
	shellSend(t, embedded, "esc")
	if !backed {
		t.Error("escape did not back out of the embedded picker")
	}
}

// TestPickerNamesEachMovementPairOnce is the other half of F22: the footer
// listed the arrows and the emacs pair as two entries that both said "move".
// Collapsing them must not lose a key, so all four are still named and no two
// hints describe the same thing.
func TestPickerNamesEachMovementPairOnce(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Anna", "Bernadett")
	p := pkPicker(t, d, nil)

	seen := map[string]string{}
	for _, hint := range p.hints {
		fields := strings.Fields(hint)
		if len(fields) < 2 {
			t.Fatalf("the hint %q names a key without saying what it does", hint)
		}
		does := fields[len(fields)-1]
		if other, ok := seen[does]; ok {
			t.Errorf("the hints %q and %q both end in %q", other, hint, does)
		}
		seen[does] = hint
	}

	frame := p.View().Content
	for _, key := range []string{"↑", "↓", keyPrev, keyNext} {
		if !strings.Contains(frame, key) {
			t.Errorf("the footer no longer names %q, which still moves the list:\n%s", key, frame)
		}
	}
}

// TestPickerAgeColumnFitsEveryWording pins the column the age is drawn in
// against every wording humantime can produce. The rung past a month is a date,
// and September gives the calendar its longest month name, so a row carrying the
// longest allowed name beside the longest possible date still has to fit the
// terminal.
func TestPickerAgeColumnFitsEveryWording(t *testing.T) {
	const width = 80
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d := shellTestDeps(t)
	d.Now = func() time.Time { return now }
	pkProfiles(t, d, "Balint")
	p := pkPicker(t, d, nil)
	st := shellStyles(p.deps)
	longest := strings.Repeat("W", profile.MaxNameRunes)

	for _, age := range []time.Duration{
		0,
		30 * time.Second,
		5 * time.Minute,
		3 * time.Hour,
		30 * time.Hour,
		5 * 24 * time.Hour,
		365 * 24 * time.Hour,
	} {
		then := now.Add(-age)
		p.rows = []pickerRow{{name: longest, lastUsed: then}}
		row := p.renderRow(st, 0, width)
		if got := ansi.StringWidth(row); got > width {
			t.Errorf("a row aged %s is %d cells wide in a %d column terminal: %q", age, got, width, row)
		}
		if want := playedAgo(now, then); !strings.Contains(row, want) {
			t.Errorf("a row aged %s does not show %q: %q", age, want, row)
		}
	}

	// A profile that has never played is its own wording, and the one the
	// shared ladder cannot supply, since a zero time is not an age.
	p.rows = []pickerRow{{name: longest}}
	if row := p.renderRow(st, 0, width); !strings.Contains(row, "never played") {
		t.Errorf("an unplayed profile does not say so: %q", row)
	}
}

func TestPickerFitsEverySize(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Anna", "Bernadett", "Cecilia", "Zsófia", "Bella Ackland")
	for _, size := range shellSizes {
		w, h := size[0], size[1]
		p := NewPicker(d, "Who is playing?")
		p.Update(tea.WindowSizeMsg{Width: w, Height: h})
		shellAssertFits(t, "picker", p.View().Content, w, h)

		// With a query, an offer to create, and a complaint on screen at once.
		pkType(t, p, "qqqqqqqq")
		shellSend(t, p, "enter")
		shellAssertFits(t, "picker with a complaint", p.View().Content, w, h)
	}
}

// TestPickerSurvivesShrinkAndRegrow is R3 at the level of one screen: the frame
// must come back identical, which can only hold if the query, the list and the
// selection all survived.
func TestPickerSurvivesShrinkAndRegrow(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Anna", "Bernadett", "Cecilia", "Zsófia", "Bella Ackland")
	p := NewPicker(d, "Who is playing?")
	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	pkType(t, p, "e")
	shellSend(t, p, "down")

	before := p.View().Content
	wanted := p.rows[p.sel].name
	shellAssertFits(t, "picker", before, 80, 24)

	p.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	shrunk := p.View().Content
	shellAssertFits(t, "picker shrunk", shrunk, 20, 8)
	if !strings.Contains(shrunk, wanted) {
		t.Errorf("the selected profile %q scrolled out of view at 20x8:\n%s", wanted, shrunk)
	}

	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	after := p.View().Content
	if after != before {
		t.Errorf("the frame changed across shrink and regrow:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if got := p.rows[p.sel].name; got != wanted {
		t.Errorf("the selection moved to %q across resizes, want %q", got, wanted)
	}
}

// TestPickerEditsTheQueryLine covers the editing keys, which is the difference
// between a text field and a place characters go to die.
func TestPickerEditsTheQueryLine(t *testing.T) {
	d := shellTestDeps(t)
	pkProfiles(t, d, "Bella Ackland")
	p := pkPicker(t, d, nil)

	pkType(t, p, "Bella Ack")
	if got := p.edit.value(); got != "Bella Ack" {
		t.Fatalf("typed text is %q", got)
	}
	shellSend(t, p, "backspace")
	if got := p.edit.value(); got != "Bella Ac" {
		t.Errorf("after backspace: %q", got)
	}
	shellSend(t, p, "ctrl+w")
	if got := p.edit.value(); got != "Bella " {
		t.Errorf("after ctrl+w: %q", got)
	}
	shellSend(t, p, "ctrl+u")
	if got := p.edit.value(); got != "" {
		t.Errorf("after ctrl+u: %q", got)
	}
	if got := pkNames(p); !equalStrings(got, []string{"Bella Ackland"}) {
		t.Errorf("clearing the query did not restore the full list: %v", got)
	}

	// Tab fills the query in from the highlighted row.
	shellSend(t, p, "tab")
	if got := p.edit.value(); got != "Bella Ackland" {
		t.Errorf("tab produced %q", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pkWideNames are legal profile names on which runes and cells disagree, which
// is most of the world's names: any script is allowed (profile.ValidateName),
// so a name of the maximum thirty-two characters can be sixty-four cells wide,
// or sixteen.
var pkWideNames = map[string]string{
	"fullwidth": strings.Repeat("日", profile.MaxNameRunes),
	"combining": strings.Repeat("e\u0301", profile.MaxNameRunes/2),
	"emoji":     strings.Repeat("🙂", profile.MaxNameRunes/2),
	"ascii":     strings.Repeat("W", profile.MaxNameRunes),
	"mixed":     "Zso\u0301fia 日本語 🙂",
}

// TestPickerRowsFitTheWidthTheyAreGiven is the display-width half of R14. A row
// budgeted in runes overflows for any name whose characters are not one cell
// wide, and the frame then cuts the line — taking the age column the row was
// widened for, and with it the one thing that tells two similar names apart.
//
// Every row therefore has to fit the width it was handed, whatever the script,
// at the selected width and unselected, and for the offer to create a name as
// well as for a profile that exists.
func TestPickerRowsFitTheWidthTheyAreGiven(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d := shellTestDeps(t)
	d.Now = func() time.Time { return now }
	p := pkPicker(t, d, nil)
	st := shellStyles(p.deps)
	then := now.Add(-3 * time.Hour)

	for what, name := range pkWideNames {
		if err := profile.ValidateName(name); err != nil {
			t.Fatalf("%s: the test name is not a legal profile name: %v", what, err)
		}
		for _, width := range []int{2, 3, 10, 20, 40, 51, 52, 80, 200} {
			for _, create := range []bool{false, true} {
				kind := "profile"
				if create {
					kind = "create"
				}
				for _, sel := range []int{0, 1} {
					p.rows = []pickerRow{{name: name, lastUsed: then, create: create}}
					p.sel = sel
					row := p.renderRow(st, 0, width)
					if got := ansi.StringWidth(row); got > width {
						t.Errorf("%s: a %s row (selected %t) is %d cells wide in a %d column terminal: %q",
							what, kind, sel == 0, got, width, row)
					}
				}
			}
		}
	}
}

// TestPickerGivesTheNameTheRoomBeforeTheAge is the priority the list has always
// stated: the age helps you recognise your row, the name is the row, so where
// both will not fit it is the age that goes. A thirty-two character CJK name is
// sixty-four cells and does not fit the name column of an eighty column
// terminal; cutting it to keep a date would say less about whose row it is than
// the whole name does. Given the room, the age comes back.
func TestPickerGivesTheNameTheRoomBeforeTheAge(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d := shellTestDeps(t)
	d.Now = func() time.Time { return now }
	p := pkPicker(t, d, nil)
	st := shellStyles(p.deps)
	then := now.Add(-3 * time.Hour)
	age := playedAgo(now, then)
	wide := strings.Repeat("日", profile.MaxNameRunes)

	p.rows = []pickerRow{{name: wide, lastUsed: then}}
	row := p.renderRow(st, 0, 80)
	if !strings.Contains(row, wide) {
		t.Errorf("a 64-cell name is not shown whole in 80 columns: %q", row)
	}
	if strings.Contains(row, ellipsis) {
		t.Errorf("the name was cut although dropping the age left room for it: %q", row)
	}
	if strings.Contains(row, age) {
		t.Errorf("the age %q is drawn beside a name that needs the whole row: %q", age, row)
	}
	if got := ansi.StringWidth(row); got > 80 {
		t.Errorf("the row is %d cells wide in 80 columns: %q", got, row)
	}

	// Given a terminal wide enough for both, the age is back.
	if row := p.renderRow(st, 0, 200); !strings.Contains(row, wide) || !strings.Contains(row, age) {
		t.Errorf("in 200 columns the row shows one of the name and the age but not both: %q", row)
	}

	// A name that fits its column keeps every character and its age.
	p.rows = []pickerRow{{name: "Zso\u0301fia", lastUsed: then}}
	row = p.renderRow(st, 0, 80)
	if !strings.Contains(row, "Zso\u0301fia") || strings.Contains(row, ellipsis) {
		t.Errorf("a six-cell name was not shown whole: %q", row)
	}
	if !strings.Contains(row, age) {
		t.Errorf("a six-cell name lost the age column: %q", row)
	}

	// The age is a column: it starts in the same cell whatever the name in
	// front of it, or the list reads as scattered dates.
	p.rows = []pickerRow{{name: "Bo", lastUsed: then}, {name: "Bernadett", lastUsed: then}}
	p.sel = 0
	first, second := p.renderRow(st, 0, 80), p.renderRow(st, 1, 80)
	if a, b := pkColumnOf(t, first, age), pkColumnOf(t, second, age); a != b {
		t.Errorf("the age starts at cell %d on one row and %d on the next:\n%q\n%q", a, b, first, second)
	}
}

// pkColumnOf returns the display column at which text starts in a rendered
// row, which is what a reader sees line up.
func pkColumnOf(t *testing.T, row, text string) int {
	t.Helper()
	i := strings.Index(row, text)
	if i < 0 {
		t.Fatalf("%q is not in the row %q", text, row)
	}
	return ansi.StringWidth(row[:i])
}

// TestPickerCutsNamesBetweenCharacters holds the cut to character boundaries.
// A row shortened between a letter and its combining accent, or inside an
// emoji, puts a fragment of a character on the screen, which a terminal draws
// as a stray accent or as nothing at all.
func TestPickerCutsNamesBetweenCharacters(t *testing.T) {
	d := shellTestDeps(t)
	p := pkPicker(t, d, nil)
	st := shellStyles(p.deps)

	for _, name := range []string{
		strings.Repeat("e\u0301", profile.MaxNameRunes/2),
		strings.Repeat("🙂", profile.MaxNameRunes/2),
		strings.Repeat("日", profile.MaxNameRunes),
	} {
		boundaries := pkClusterBoundaries(name)
		for width := 4; width <= 24; width++ {
			p.rows = []pickerRow{{name: name}}
			p.sel = 0
			row := p.renderRow(st, 0, width)
			// The selection marker is two cells; what follows it is the name
			// as much of it as was shown, and the mark if it was cut.
			shown := strings.TrimSuffix(strings.TrimPrefix(ansi.Strip(row), "> "), ellipsis)
			if !strings.HasPrefix(name, shown) {
				t.Errorf("at %d columns the row shows %q, which is not the start of %q", width, shown, name)
				continue
			}
			if !boundaries[len(shown)] {
				t.Errorf("at %d columns the row shows %q, cut inside a character of %q", width, shown, name)
			}
		}
	}
}

// pkClusterBoundaries reports the byte offsets of name that fall between
// characters as a reader sees them, the whole string included.
func pkClusterBoundaries(name string) map[int]bool {
	out := map[int]bool{0: true}
	for off := 0; off < len(name); {
		cluster, _ := ansi.FirstGraphemeCluster(name[off:], ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		off += len(cluster)
		out[off] = true
	}
	return out
}

// TestPickerHighlightKeepsACharacterWhole is the match highlight's own version
// of the same rule. An accent written as a combining mark is a rune of its own,
// and a highlight that takes the letter without it puts a style boundary inside
// one character on screen: the terminal then draws an unstyled accent over a
// highlighted letter.
func TestPickerHighlightKeepsACharacterWhole(t *testing.T) {
	st := ui.DefaultStyles()
	const name = "e\u0301clair"
	want := paint(&st, &st.Highlight, "e\u0301")
	if !strings.Contains(want, "\x1b") {
		t.Fatalf("the highlight style emits no escape sequence, so this test cannot tell the cases apart: %q", want)
	}

	got := highlightRunes(&st, name, []int{0})
	if !strings.Contains(got, want) {
		t.Errorf("the accent is styled apart from its letter:\ngot  %q\nwant %q inside it", got, want)
	}
	if plain := ansi.Strip(got); plain != name {
		t.Errorf("the highlighted name reads %q, want %q", plain, name)
	}

	// Adjacent matches are one styled run, and a position past the end of the
	// name changes nothing rather than dropping characters.
	if run, want := highlightRunes(&st, "Balint", []int{1, 2, 3}), paint(&st, &st.Highlight, "ali"); !strings.Contains(run, want) {
		t.Errorf("adjacent matches are styled separately:\ngot  %q\nwant %q inside it", run, want)
	}
	if got := highlightRunes(&st, name, []int{99}); got != name {
		t.Errorf("a position outside the name changed it to %q", got)
	}
}

// pkKey encodes the editing keys shellKeyPress does not know, checking the
// encoding so that a test cannot dispatch a name the terminal never produces.
func pkKey(t *testing.T, name string) tea.KeyPressMsg {
	t.Helper()
	var k tea.Key
	switch name {
	case "left":
		k = tea.Key{Code: tea.KeyLeft}
	case "right":
		k = tea.Key{Code: tea.KeyRight}
	case "delete":
		k = tea.Key{Code: tea.KeyDelete}
	default:
		return shellKeyPress(name)
	}
	press := tea.KeyPressMsg(k)
	if got := press.String(); got != name {
		t.Fatalf("key %q encodes as %q", name, got)
	}
	return press
}

// TestPickerQueryKeepsTheCaretInView is the query field's clipping boundary.
// The caret says where the next character will go, so a field too narrow for
// the text has to scroll around the caret: cutting the finished line from the
// left dropped the caret whenever the cursor was left of the overflow, leaving
// the end of a name on screen and no cursor anywhere on it.
//
// The text is fullwidth, so every character is two cells and the window edges
// fall between characters rather than on them, which is also where a field one
// cell too wide for its frame came from.
func TestPickerQueryKeepsTheCaretInView(t *testing.T) {
	d := shellTestDeps(t)
	p := pkPicker(t, d, nil)
	st := shellStyles(p.deps)
	const query = "日本語日本語日本語日本語日本語日本語日"
	runes := []rune(query)

	for _, width := range []int{3, 4, 9, 20, 21, 40} {
		for _, at := range []int{0, 1, len(runes) / 2, len(runes) - 1, len(runes)} {
			p.edit.setValue(query)
			p.edit.pos = at
			line := p.edit.render(st, width)
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("cursor at %d: the field is %d cells wide in %d columns: %q", at, got, width, line)
			}
			body, ok := strings.CutPrefix(ansi.Strip(line), "> ")
			if !ok {
				t.Fatalf("cursor at %d: the field lost its prompt: %q", at, line)
			}
			i := strings.Index(body, caret)
			if i < 0 {
				t.Errorf("cursor at %d in %d columns: the caret is not on screen: %q", at, width, line)
				continue
			}
			// What is shown must be a window onto the text around the cursor:
			// the part before the caret ends where the cursor is, the part
			// after it starts there.
			if head, want := body[:i], string(runes[:at]); !strings.HasSuffix(want, head) {
				t.Errorf("cursor at %d in %d columns: %q is drawn before the caret, which is not the end of %q",
					at, width, head, want)
			}
			if tail, want := body[i+len(caret):], string(runes[at:]); !strings.HasPrefix(want, tail) {
				t.Errorf("cursor at %d in %d columns: %q is drawn after the caret, which is not the start of %q",
					at, width, tail, want)
			}
		}
	}
}

// TestPickerQueryEditsWholeCharacters is the editing half of the same rule. An
// accent written as a combining mark and an emoji written as several runes
// joined together are each one character on screen: a cursor inside one has no
// cell to be drawn in, and a backspace inside one strips the accent off its
// letter, leaving a name nobody typed.
func TestPickerQueryEditsWholeCharacters(t *testing.T) {
	d := shellTestDeps(t)
	p := pkPicker(t, d, nil)

	// "é" as a letter and a combining accent, then a plain letter.
	const accented = "e\u0301x"
	p.edit.setValue(accented)
	p.edit.key(pkKey(t, "left"))
	if got := p.edit.pos; got != 2 {
		t.Errorf("one left from the end of %q puts the cursor at rune %d, want 2", accented, got)
	}
	p.edit.key(pkKey(t, "left"))
	if got := p.edit.pos; got != 0 {
		t.Errorf("left again puts the cursor at rune %d, between the letter and its accent", got)
	}
	p.edit.key(pkKey(t, "right"))
	if got := p.edit.pos; got != 2 {
		t.Errorf("right from the start puts the cursor at rune %d, between the letter and its accent", got)
	}

	p.edit.setValue(accented)
	p.edit.key(pkKey(t, "backspace"))
	p.edit.key(pkKey(t, "backspace"))
	if got := p.edit.value(); got != "" {
		t.Errorf("two backspaces over %q leave %q, want the accented letter gone whole", accented, got)
	}

	p.edit.setValue(accented)
	p.edit.pos = 0
	p.edit.key(pkKey(t, "delete"))
	if got := p.edit.value(); got != "x" {
		t.Errorf("delete over the accented letter of %q leaves %q, want %q", accented, got, "x")
	}

	// An emoji joined out of several runes is one character as well. It is not
	// a legal profile name — the joiner is an invisible character, which
	// profile.ValidateName refuses — but the field is a text field, and what a
	// player types into it has to behave.
	const family = "\U0001F469\u200D\U0001F467z"
	p.edit.setValue(family)
	p.edit.key(pkKey(t, "backspace"))
	p.edit.key(pkKey(t, "backspace"))
	if got := p.edit.value(); got != "" {
		t.Errorf("two backspaces over %q leave %q, want the joined emoji gone whole", family, got)
	}
}
