package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/game"
	"github.com/BAKocska/twixtui/internal/gamestore"
	"github.com/BAKocska/twixtui/internal/study"
	"github.com/BAKocska/twixtui/internal/ui"
)

// The glyphs the entry list marks a study's entries with. They are characters
// rather than colours so that the marks survive colour being switched off, and
// ASCII so that every terminal draws them one cell wide. Neither is a character
// of the move notation, so a mark cannot be read as part of the entry beside it.
const (
	bookmarkGlyph = "*"
	noteGlyph     = "#"
)

// replayNotes is the study kept beside a replayed game: the notes and bookmarks
// a player has put on its entries. It is read once, when the screen opens, and
// written one change at a time.
//
// The record itself is never written. The study is a file of its own, keyed by
// the game's identifier and the record's digest, so a record that has changed
// since the notes were written refuses them rather than showing notes about a
// game that is no longer the one on screen.
type replayNotes struct {
	store  *study.Store
	target study.Target
	// doc is the study as the store last reported it. Every write is made
	// against its revision, so a write that another window got in first is
	// refused instead of being laid over it.
	doc study.Study
	// locked is why nothing may be written here, or nil when writing is
	// allowed: there is no store, the notes could not be read, or the game has
	// no result yet.
	locked studyNotice
	// problem is what the last request made of the notes ran into, and
	// problemAt the entry it was made on. It is shown on that entry only,
	// because a refusal belongs to what was asked, and the next entry was not
	// asked about.
	problem   studyNotice
	problemAt int
	// editor is the note being written, open only while one is.
	editor noteEditor
}

// openNotes finds the study of the game being replayed. Nothing about it stops
// the replay: a study that cannot be read leaves the review as it was and the
// notes read-only, with the reason in the panel, and the file exactly as it was
// found, so the program that can read it still finds it there.
func openNotes(store *study.Store, saved gamestore.Saved, record game.Record, entries int) replayNotes {
	n := replayNotes{
		store: store,
		target: study.Target{
			GameID:       saved.ID,
			RecordDigest: record.Digest,
			Entries:      entries,
			// The record's own result decides, as it does for the game store:
			// the label beside it is covered by no digest.
			Finished: record.Outcome != game.Ongoing,
		},
	}
	if store == nil {
		n.locked = studyNotice{"notes are unavailable here", "no notes here"}
		return n
	}
	doc, err := store.Load(n.target)
	if err != nil {
		n.locked = studyRefusal(err)
		return n
	}
	n.doc = doc
	if !n.target.Finished {
		n.locked = studyRefusal(study.ErrUnfinished)
	}
	return n
}

// studyNotice is something the notes tell the player, in forms that run from
// the whole sentence down to the few words a status line holds at the
// narrowest size, widest first. The panel has room for the first. The status
// line, which is all there is beside the board at the sizes with no room for
// a panel, takes the widest that fits whole, so a reason is shortened by
// saying less rather than by being cut off in the middle of a word.
type studyNotice []string

// full is the whole of it, which is what the panel shows.
func (n studyNotice) full() string {
	if len(n) == 0 {
		return ""
	}
	return n[0]
}

// brief is its shortest form, which a status line has room for at the
// narrowest size a board is drawn at.
func (n studyNotice) brief() string {
	if len(n) == 0 {
		return ""
	}
	return n[len(n)-1]
}

// fit is the widest form that fits in width cells, or the shortest one cut to
// width with a mark when none does.
func (n studyNotice) fit(width int) string {
	for _, form := range n {
		if ansi.StringWidth(form) <= width {
			return form
		}
	}
	return truncateText(n.brief(), width)
}

// of is the notice as the refusal of what was asked: named in front of the
// reason while there is room for both, and the reason alone at the narrowest,
// because the reason is what the player cannot work out for themselves.
func (n studyNotice) of(what string) studyNotice {
	return studyNotice{what + ": " + n.full(), what + ": " + n.brief(), n.brief()}
}

// studyRefusal says in the player's terms why the store would not read or
// write the notes. For the three refusals of a file it could not use, it also
// says what became of the file, which is nothing: the player's notes are
// somewhere, and knowing they were left alone is what makes the refusal
// something other than a loss. The brief form keeps only the reason.
func studyRefusal(err error) studyNotice {
	var why, brief string
	switch {
	case errors.Is(err, study.ErrRecordChanged):
		why, brief = "the notes were written for a different record of this game and are left as they are", "record changed"
	case errors.Is(err, study.ErrNewerVersion):
		why, brief = "the notes were written by a newer twixtui and are left as they are", "newer twixtui"
	case errors.Is(err, study.ErrCorrupt):
		why, brief = "the notes file is damaged and is left as it is", "notes damaged"
	case errors.Is(err, study.ErrUnfinished):
		why, brief = "notes are kept on finished games only", "unfinished game"
	default:
		// The remaining errors carry paths and identifiers, which are text
		// from this machine rather than from the program, and drawn as such.
		// None of that fits in a few words, so the brief form says only
		// where the trouble is and leaves the rest to the panel.
		why, brief = err.Error(), "notes file error"
	}
	return studyNotice{replayLabel(why), brief}
}

// refuse records what a request on entry ran into.
func (n *replayNotes) refuse(entry int, why studyNotice) { n.problem, n.problemAt = why, entry }

// flagged reports whether the entry list needs its column of marks at all. A
// game nobody has written on keeps the list it always had, and every cell of
// it goes to the entries.
func (n *replayNotes) flagged() bool { return len(n.doc.Marks) > 0 }

// flags is an entry's two cells in that column: the bookmark glyph, then the
// note glyph, each a blank when the entry has no such thing.
func (n *replayNotes) flags(entry int) string {
	mark := n.doc.At(entry)
	bookmark, note := " ", " "
	if mark.Bookmark {
		bookmark = bookmarkGlyph
	}
	if mark.Note != "" {
		note = noteGlyph
	}
	return bookmark + note
}

// toggleBookmark puts a bookmark on the entry on screen, or takes it off, and
// saves at once: a bookmark is one keypress, and a second one to confirm it
// would be a form around a single bit.
func (s *ReplayScreen) toggleBookmark() {
	n := &s.notes
	entry := s.step()
	if n.locked != nil {
		n.refuse(entry, n.locked.of("bookmark unchanged"))
		return
	}
	// The note travels with the bookmark because a mark is saved whole. It is
	// the note of the revision the write is made against, so a note saved
	// elsewhere since cannot be put back to what this screen remembered: that
	// write is the one refused as stale.
	mark := n.doc.At(entry)
	mark.Entry, mark.Bookmark = entry, !mark.Bookmark
	doc, err := n.store.Set(n.target, n.doc.Revision, mark)
	switch {
	case errors.Is(err, study.ErrStale):
		// The press is not repeated on the player's behalf. What is stored
		// now is shown, and the player decides whether it still wants the
		// change.
		n.doc = doc
		n.refuse(entry, studyNotice{
			"the notes were changed elsewhere and are shown as they are now; press again to change it",
			"changed elsewhere",
		}.of("bookmark unchanged"))
	case err != nil:
		n.refuse(entry, studyRefusal(err).of("bookmark unchanged"))
	default:
		n.doc = doc
		n.problem = nil
	}
}

// openNoteEditor opens the note input on the entry on screen, holding that
// entry's note as it is stored, so that editing a note starts from the note.
func (s *ReplayScreen) openNoteEditor() {
	n := &s.notes
	entry := s.step()
	if n.locked != nil {
		n.refuse(entry, n.locked.of("no note written"))
		return
	}
	n.problem = nil
	n.editor.start(entry, n.doc.At(entry).Note)
}

// editNote hands one key to the note input: the note is saved, the input is
// abandoned, or the key is text or the field's own editing.
func (s *ReplayScreen) editNote(m tea.KeyPressMsg) {
	switch key := m.String(); {
	case key == "esc":
		// Escape abandons the note and writes nothing. Whatever is stored is
		// left as it was, including a version that a refused save has just
		// put on screen.
		s.notes.editor.cancel()
	case matchesKey(key, s.confirm):
		s.saveNote()
	default:
		s.notes.editor.key(m)
	}
}

// saveNote writes the note being edited against the revision the screen last
// read.
//
// Only a write that is taken closes the editor. A refused one leaves the text
// where it was typed, because nothing the player wrote should be lost to a
// store that said no. A write refused because the notes changed elsewhere reads
// what is stored now and shows it under the field, so that the next save is a
// decision made with both versions on screen rather than an overwrite nobody saw
// happen.
//
// That decision is only one while the stored note is on screen whole, so the
// next save is taken only then. A terminal with no room for a panel, or with a
// panel too short or too narrow for all of the note, gets a refusal that says
// so instead, and the player's text stays in the field until the terminal is
// enlarged or the input is abandoned.
func (s *ReplayScreen) saveNote() {
	n := &s.notes
	e := &n.editor
	mark := n.doc.At(e.entry)
	text := e.edit.value()
	if text == mark.Note {
		// Nothing changed, so there is nothing to write: a revision records a
		// change, and another window's next save would be refused over none.
		e.cancel()
		return
	}
	if e.stale && !s.storedOnScreen() {
		// Nothing is written. The input's own note is what says why, so a
		// problem left standing from the text is cleared out of its way.
		e.problem = nil
		return
	}
	mark.Entry, mark.Note = e.entry, text
	doc, err := n.store.Set(n.target, n.doc.Revision, mark)
	switch {
	case errors.Is(err, study.ErrStale):
		n.doc = doc
		e.stale = true
		e.problem = nil
	case err != nil:
		e.problem = studyRefusal(err).of("not saved")
	default:
		n.doc = doc
		e.cancel()
	}
}

// studyKept is how many rows of what the notes say about the entry on screen
// are kept from the record's prose in a short panel. The prose is ordered most
// useful line first and loses its tail gracefully; a note the player wrote on
// the entry they are looking at is the reason they came back to it.
const studyKept = 2

// studyReserve is how many of the study block's rows are set aside before the
// record's prose is given any, counting the blank line that closes the block.
// The note input is what the player is doing while it is open, and a refusal
// is the answer to what they last asked, so all of either is set aside, the
// way the entry input's rows are; of anything else in the block, the first
// studyKept rows are.
func (s *ReplayScreen) studyReserve(width int, block []string) int {
	switch {
	case len(block) == 0:
		return 0
	case s.notes.editor.open:
		return len(block) + 1
	}
	return min(len(block), len(s.refusalLines(width))+studyKept) + 1
}

// studyRoom is how many rows the study block may take in a panel width cells
// wide and height rows tall: every row above the entry list's promised share,
// less the entry input's and the blank line that closes the block. The block
// is given its rows before the analysis's lead or the record's prose is given
// any, so these are the rows it is drawn in whenever it needs all of them,
// which is what lets a save be judged on whether they hold the stored note.
func (s *ReplayScreen) studyRoom(width, height int) int {
	return height - len(s.jump.lines(shellStyles(s.deps), width)) - s.listShare(height) - 1
}

// studyLines is the study block in the panel: the note input while it is open,
// otherwise whatever the notes have to say about the entry on screen — a
// refusal, whether it is bookmarked, and its note.
//
// A note is text from a file, which another program or a hand edit may have
// written anything into, so it is drawn inert, and a note longer than the rows
// it is given is cut with a mark saying so.
func (s *ReplayScreen) studyLines(width, rows int) []string {
	if width <= 0 || rows <= 0 {
		return nil
	}
	n := &s.notes
	if n.editor.open {
		// The note stored now is drawn only when all of it fits in these
		// rows, which is the test a save is held to as well.
		shown := n.storedFits(width, rows)
		return clampLines(n.editorLines(shellStyles(s.deps), width, shown), rows)
	}
	lines := s.refusalLines(width)
	mark := n.doc.At(s.step())
	if mark.Bookmark {
		lines = append(lines, truncateText(bookmarkGlyph+" bookmarked", width))
	}
	if mark.Note != "" {
		lines = append(lines, wrapText(noteGlyph+" note: "+replayLabel(mark.Note), width)...)
	}
	return cutRows(lines, rows, width)
}

// refusalLines is the refusal the study block opens with: what the last
// request made on the entry on screen ran into, or else why nothing may be
// written here at all.
func (s *ReplayScreen) refusalLines(width int) []string {
	n := &s.notes
	switch {
	case n.problem != nil && n.problemAt == s.step():
		return wrapText(n.problem.full(), width)
	case n.locked != nil && n.store != nil:
		// A screen without a store has nothing to report until it is asked:
		// that is a caller that keeps no notes, not a game whose notes are
		// being withheld.
		return wrapText(n.locked.full(), width)
	}
	return nil
}

// studyStatus is what the notes have to say on the status line: the refusal of
// the last request made on the entry on screen, in the few words the line has
// room for. At the sizes with no room for a panel the line is the whole of the
// interface beside the board, and a refusal said only in a panel that is not
// drawn is a key that does nothing without saying why.
func (s *ReplayScreen) studyStatus() string {
	n := &s.notes
	if n.problem == nil || n.problemAt != s.step() {
		return ""
	}
	return n.problem.brief()
}

// editorLines are the note input's rows: the field, then editorText.
func (n *replayNotes) editorLines(st *ui.Styles, width int, shown bool) []string {
	return append([]string{n.editor.render(st, width)}, n.editorText(width, shown)...)
}

// editorText is the note input's rows under the field: what the input says
// about itself and, once a save has found the notes changed elsewhere and
// shown is set, the note that is stored now.
func (n *replayNotes) editorText(width int, shown bool) []string {
	e := &n.editor
	said, _ := e.note(shown)
	lines := wrapText(said.full(), width)
	if e.stale && shown {
		stored := "stored now: no note"
		if note := n.doc.At(e.entry).Note; note != "" {
			stored = "stored now: " + replayLabel(note)
		}
		// A cell short of the width, because the frame shortens a line that
		// fills its width and ends in the truncation mark back to its last
		// whole word, and a stored note may well end in that character: the
		// word it took would be one the player never saw.
		lines = append(lines, wrapText(stored, width-1)...)
	}
	return lines
}

// storedFits reports whether rows rows of width cells hold the note input with
// the note stored now whole beneath it: the field, what the input says about
// itself, and every line of the stored note. It is the one measure of that.
// The input draws the stored note only when it passes, and a save may replace
// the note only when the rows the terminal's size gives the input pass it
// too, so the two cannot disagree about what the player has seen.
//
// The note is wrapped a cell short of width, and the widest character is two
// cells, so nothing narrower than three cells can hold every character of it.
func (n *replayNotes) storedFits(width, rows int) bool {
	return n.editor.stale && width > 2 && 1+len(n.editorText(width, true)) <= rows
}

// storedOnScreen reports whether the frame the terminal's size gives draws the
// note stored now whole. The frame is arranged from the size the screen was
// last given, as View arranges it, so a save is judged in Update on the frame
// the player is looking at rather than on whatever a View last happened to
// draw. Without a panel there is only the status line beside the board, and
// the stored note is not on it.
func (s *ReplayScreen) storedOnScreen() bool {
	arr := ui.Arrange(s.width, s.height, s.current().Size())
	if arr.TooSmall || arr.Panel == ui.PanelNone {
		return false
	}
	return s.notes.storedFits(arr.PanelW, s.studyRoom(arr.PanelW, arr.PanelH))
}

// noteInput is the note input as the status line draws it. At the widths that
// have no room for a panel it is the whole of the interface, as it is for the
// entry input, so the field is drawn here too, after what the input says about
// itself in the widest form that fits whole.
//
// While that is only which entry the note is for, the field keeps at least
// half the row. A refusal outranks the field, as the entry input's problem
// does: the player has to act on it before the text in the field can go
// anywhere, so the field then keeps only the room its prompt and caret need.
func (s *ReplayScreen) noteInput(width int) string {
	e := &s.notes.editor
	st := shellStyles(s.deps)
	said, refused := e.note(e.stale && s.storedOnScreen())
	least := width / 2
	if refused {
		least = min(3, width)
	}
	label := said.fit(width - least - 1)
	if label == "" {
		return e.render(st, width)
	}
	return label + " " + e.render(st, width-ansi.StringWidth(label)-1)
}

// cutRows keeps the first rows lines, marking the last one kept when some were
// dropped, so a note cut short reads as cut rather than as ending there.
func cutRows(lines []string, rows, width int) []string {
	if len(lines) <= rows {
		return lines
	}
	lines = lines[:rows]
	lines[rows-1] = ansi.Truncate(lines[rows-1], max(0, width-1), "") + ellipsis
	return lines
}

// noteEditor is the note input, which the e key opens on the entry on screen.
//
// It is modal for the reason the entry input is: a note is text, and the
// letters in it are the keys that move a review and leave it. A screen that
// went on answering them would step away from the entry being written about at
// the first h, and leave altogether at the first q.
type noteEditor struct {
	open bool
	// entry is the entry the note is about, fixed when the input opens, so
	// that the note is saved where it was begun.
	entry int
	edit  lineEdit
	// problem is why the last save or the last text was refused. It stands
	// until the text changes, because a message cleared by the next keypress is
	// gone before it has been read.
	problem studyNotice
	// stale is set once a save has found the notes changed elsewhere. From
	// then on the stored note is shown under the field wherever there is room
	// for all of it, so both versions are in front of the player when they
	// choose which one to keep, and a save is refused wherever there is not.
	stale bool
}

// start opens the input on entry, holding note.
func (e *noteEditor) start(entry int, note string) {
	*e = noteEditor{open: true, entry: entry}
	e.edit.setValue(note)
}

// cancel closes the input, forgetting what was typed.
func (e *noteEditor) cancel() { *e = noteEditor{} }

// key applies a keypress: text is inserted or refused, and anything else is
// the field's own editing and movement.
func (e *noteEditor) key(m tea.KeyPressMsg) {
	if m.Text != "" {
		e.insert(m.Text)
		return
	}
	if e.edit.key(m) {
		e.problem = nil
	}
}

// size is how many bytes the note holds as UTF-8, which is what its bound is
// counted in.
func (e *noteEditor) size() int {
	n := 0
	for _, r := range e.edit.runes {
		n += utf8.RuneLen(r)
	}
	return n
}

// insert takes typed or pasted text. A paste is text from anywhere and can be
// a whole file, so what arrived is measured against the room left before any
// of it is copied, and what is kept is measured again: a byte that is not
// UTF-8 is kept as a replacement character, which is three bytes long.
func (e *noteEditor) insert(text string) {
	room := study.MaxNoteBytes - e.size()
	if len(text) > room {
		e.problem = noteTooLong()
		return
	}
	text = noteText(text)
	if len(text) > room {
		e.problem = noteTooLong()
		return
	}
	if text == "" {
		return
	}
	e.edit.insert(text)
	e.problem = nil
}

// noteTooLong is the refusal of text that would take a note past its bound.
func noteTooLong() studyNotice {
	kib := study.MaxNoteBytes >> 10
	return studyNotice{fmt.Sprintf("a note holds at most %d KiB", kib), fmt.Sprintf("%d KiB at most", kib)}
}

// noteText is typed or pasted text as the one-line field keeps it. Line breaks
// and tabs become spaces, since the field has one line to show them on, and the
// other control characters are dropped: nothing typed at a keyboard means them
// as part of a note. strings.Map also replaces any byte that is not UTF-8 with
// the replacement character, so what is kept is text a note may hold.
func noteText(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, text)
}

// What the input says once a save has found the notes changed elsewhere: that
// the note stored now is shown beneath the field and the next save replaces
// it, or, where there is no room to show all of it, that the next save is
// refused and what will let it be taken. The shortest form of the first
// points at the panel, which is where the note is whenever it is shown.
var (
	staleShown = studyNotice{
		"the note was changed elsewhere and is shown below; saving again replaces it",
		"changed elsewhere: saving replaces it",
		"save replaces it",
		"see above",
	}
	staleHidden = studyNotice{
		"not saved: the note was changed elsewhere and cannot be shown whole at this size; " +
			"enlarge the terminal to compare the two, or press esc to keep it",
		"not saved: changed elsewhere; enlarge to compare, or esc to keep it",
		"changed elsewhere: enlarge or esc",
		"enlarge or esc",
	}
)

// note is what the input says about itself: the problem while there is one,
// then the other version while there is one, and otherwise which entry the note
// is for. shown is whether the stored note is drawn whole beneath the field.
// It also reports whether what it says is a refusal the player has to act on,
// which the entry number is not and the other version on screen is not
// either. The keys that save and cancel are on the status line.
func (e *noteEditor) note(shown bool) (studyNotice, bool) {
	switch {
	case e.problem != nil:
		return e.problem, true
	case e.stale && shown:
		return staleShown, false
	case e.stale:
		return staleHidden, true
	}
	about := fmt.Sprintf("entry %d", e.entry)
	return studyNotice{"note on " + about, about}, false
}

// render draws the field in width cells.
//
// What the field holds is drawn inert. A note another program wrote can carry
// line breaks and escape sequences, and the field keeps them as they are, so
// that saving a note the player has only added to does not rewrite the parts
// they never touched. The copy that is drawn has them made safe instead, split
// at the caret so the caret stays where it is in the text.
func (e *noteEditor) render(st *ui.Styles, width int) string {
	if !slices.ContainsFunc(e.edit.runes, unsafeInNote) {
		return e.edit.render(st, width)
	}
	head := inertRunes(e.edit.runes[:e.edit.pos])
	shown := lineEdit{runes: append(head, inertRunes(e.edit.runes[e.edit.pos:])...), pos: len(head)}
	return shown.render(st, width)
}

// unsafeInNote reports a rune a terminal would act on rather than draw, or
// would draw as something other than the single space it stands for.
func unsafeInNote(r rune) bool {
	return r != ' ' && (unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}

// inertRunes is replayLabel for the field: spacing becomes a space and the
// characters a terminal would obey are dropped. Nothing is collapsed, and the
// two sides of the caret are made safe separately, so the caret is drawn
// between the same two characters it sits between in the note.
func inertRunes(runes []rune) []rune {
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		switch {
		case unicode.IsSpace(r):
			out = append(out, ' ')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
		default:
			out = append(out, r)
		}
	}
	return out
}
