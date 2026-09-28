package app

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/BAKocska/twixtui/internal/study"
	"github.com/BAKocska/twixtui/internal/ui"
)

// These are the seams between the replay's notes and its analysis: two
// features that each hold the status line, the panel's spare rows and the
// keyboard, written apart and composed in one screen. Each feature's own tests
// run with the other one idle; these run both at once.

// rpcEntry is the entry the fixtures keep a note, a bookmark and an analysis on,
// and rpcNote the note. Its words appear nowhere else on a replay's screen, so
// finding them is finding the note.
const (
	rpcEntry = 9
	rpcNote  = "knot-at-nine"
)

// rpcAt opens rpConnectionRecord with a study store, a note and a bookmark
// already stored on rpcEntry, and the stand-in engine, and goes to rpcEntry by
// its number.
func rpcAt(t *testing.T) (*rpaHarness, *rpaEngine, Deps, study.Target) {
	t.Helper()
	d := snDeps(t)
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	if _, err := study.Open(d.ConfigDir).Set(target, 0, study.Mark{Entry: rpcEntry, Bookmark: true, Note: rpcNote}); err != nil {
		t.Fatal(err)
	}
	eng := newRPAEngine()
	h := rpaOpen(t, d, sv, eng.analyse)
	rpJump(t, h.s, strconv.Itoa(rpcEntry))
	if h.s.step() != rpcEntry {
		t.Fatalf("the jump reached step %d, want %d", h.s.step(), rpcEntry)
	}
	return h, eng, d, target
}

// rpcAnalysed asks for an analysis of the entry on screen and has the stand-in
// engine answer it with rpaFixture.
func rpcAnalysed(t *testing.T, h *rpaHarness, eng *rpaEngine) {
	t.Helper()
	h.press("?")
	eng.next(t).answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.s.analysis.done })
	if !h.s.analysis.shown(h.s.step()) {
		t.Fatalf("the analysis of step %d is not shown:\n%s", h.s.step(), h.block())
	}
}

// rpcPanel is the panel as View lays it out for the size the screen was last
// given, and nil where that size has no panel.
func rpcPanel(s *ReplayScreen) (ui.Arrangement, []string) {
	arr := ui.Arrange(s.width, s.height, s.current().Size())
	if arr.TooSmall || arr.Panel == ui.PanelNone {
		return arr, nil
	}
	return arr, s.panel(arr.PanelW, arr.PanelH)
}

// rpcBottom is the frame's last row, the status line.
func rpcBottom(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[len(lines)-1]
}

// TestReplayComposeNotesAndAnalysisShareEveryFrame puts a note, a bookmark and
// a finished analysis on one entry and draws it at every size a review has to
// work in. The frame fits; the entry list keeps its promised rows and the row
// under review, with its marks, is drawn; the status line carries the analysis
// at every size, which at the smallest, with no panel, is the only place it
// is; and wherever the panel is beside the board it has room for the bookmark,
// the note and the analysis's choice all at once.
func TestReplayComposeNotesAndAnalysisShareEveryFrame(t *testing.T) {
	h, eng, _, _ := rpcAt(t)
	rpcAnalysed(t, h, eng)
	pair := rpaMove.String() + " placement-only"

	var panelless, beside int
	for _, size := range shellSizes {
		w, ht := size[0], size[1]
		h.feed(tea.WindowSizeMsg{Width: w, Height: ht})
		frame := h.frame()
		shellAssertFits(t, "replay with a note and an analysis", frame, w, ht)
		if bottom := rpcBottom(frame); !strings.Contains(bottom, pair) {
			t.Errorf("at %dx%d the bottom row %q does not carry the analysis %q", w, ht, bottom, pair)
		}

		arr, panel := rpcPanel(h.s)
		if panel == nil {
			panelless++
			continue
		}
		all := strings.Join(panel, "\n")
		if len(panel) > arr.PanelH {
			t.Errorf("at %dx%d the %d-row panel produced %d lines:\n%s", w, ht, arr.PanelH, len(panel), all)
		}
		for _, l := range panel {
			if lw := ansi.StringWidth(l); lw > arr.PanelW {
				t.Errorf("at %dx%d the %d-cell panel has a line %d cells wide: %q", w, ht, arr.PanelW, lw, l)
			}
		}
		rows := snRows(t, panel, 2)
		if want := min(arr.PanelH/2, h.s.entries()+1); len(rows) < want {
			t.Errorf("at %dx%d the panel shows %d entry rows, want at least %d:\n%s", w, ht, len(rows), want, all)
		}
		var marked []string
		for _, l := range panel {
			if r := snRows(t, []string{l}, 2); len(r) == 1 && r[0].marked {
				if r[0].number != rpcEntry || !r[0].bookmark || !r[0].noted {
					t.Errorf("at %dx%d the row under review is %+v, want entry %d with its bookmark and note", w, ht, r[0], rpcEntry)
				}
				marked = append(marked, l)
			}
		}
		if len(marked) != 1 {
			t.Errorf("at %dx%d the list marks %d rows, want exactly one:\n%s", w, ht, len(marked), all)
			continue
		}
		if !strings.Contains(frame, marked[0]) {
			t.Errorf("at %dx%d the panel builds the row under review %q and the frame does not draw it:\n%s", w, ht, marked[0], frame)
		}
		if arr.Panel == ui.PanelSide {
			beside++
			for _, want := range []string{bookmarkGlyph + " bookmarked", rpcNote, "engine's choice"} {
				if !strings.Contains(all, want) {
					t.Errorf("at %dx%d the panel beside the board lost %q:\n%s", w, ht, want, all)
				}
			}
		}
	}
	if panelless == 0 || beside == 0 {
		t.Fatalf("the sizes gave %d frames without a panel and %d with one beside the board; both kinds are needed", panelless, beside)
	}
}

// TestReplayComposeNoteInputOwnsTheAnalysisKey types the analysis key into an
// open note, with the other keys a review answers around it. While the input
// is open every one of them is text: nothing is analysed, marked, left or
// moved, and escape leaves the stored note as it was. Outside the input the
// same key does start an analysis, so the press inside it was taken by the
// input rather than ignored by the screen.
func TestReplayComposeNoteInputOwnsTheAnalysisKey(t *testing.T) {
	h, eng, d, target := rpcAt(t)
	h.press("e")
	if !h.s.notes.editor.open {
		t.Fatalf("e did not open the note input:\n%s", h.frame())
	}
	h.s.Update(tutorialKeyMsg("ctrl+u"))
	const typed = "?mq:h"
	snType(t, h.s, typed)
	if got := h.s.notes.editor.edit.value(); got != typed {
		t.Fatalf("the note input holds %q after %q was typed into it", got, typed)
	}
	if h.s.analysis.job != nil || h.s.analysis.running {
		t.Fatal("a ? typed into the note started an analysis")
	}

	h.press("esc")
	if h.s.notes.editor.open {
		t.Fatal("esc did not close the note input")
	}
	snAssertMarks(t, snLoad(t, d, target), study.Mark{Entry: rpcEntry, Bookmark: true, Note: rpcNote})

	h.press("?")
	eng.next(t).answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the analysis", func() bool { return h.s.analysis.done })
	if !h.s.analysis.shown(rpcEntry) {
		t.Fatalf("? outside the note input did not analyse the entry:\n%s", h.block())
	}
}

// TestReplayComposeSeekCancelsTheAnalysisAndFollowsTheNotes moves on from an
// entry with a note, a bookmark and a search still running. The search is
// cancelled and nothing of it is left on screen, and the study block follows
// the review: the next entry has no note and no bookmark, so none is shown,
// while the list still marks the entry that has them. Coming back shows the
// note again, and not the analysis, which moving put away for good.
func TestReplayComposeSeekCancelsTheAnalysisAndFollowsTheNotes(t *testing.T) {
	h, eng, _, _ := rpcAt(t)
	h.press("?")
	call := eng.next(t)
	h.press("l")
	if h.s.step() != rpcEntry+1 {
		t.Fatalf("l reached step %d, want %d", h.s.step(), rpcEntry+1)
	}
	if call.ctx.Err() == nil {
		t.Fatal("moving on left the search of the entry before running")
	}
	if h.s.analysis.job != nil {
		t.Fatal("the screen still holds an analysis of the entry it left")
	}

	_, panel := rpcPanel(h.s)
	all := strings.Join(panel, "\n")
	for _, gone := range []string{rpcNote, "bookmarked", "analysing", "? cancels"} {
		if strings.Contains(all, gone) {
			t.Errorf("at step %d the panel still shows %q:\n%s", h.s.step(), gone, all)
		}
	}
	if bottom := rpcBottom(h.frame()); strings.Contains(bottom, "analysing") {
		t.Errorf("at step %d the bottom row still reports the search: %q", h.s.step(), bottom)
	}
	rows := snRows(t, panel, 2)
	row := func(n int) snRow {
		t.Helper()
		i := slices.IndexFunc(rows, func(r snRow) bool { return r.number == n })
		if i < 0 {
			t.Fatalf("the list has no row for entry %d:\n%s", n, all)
		}
		return rows[i]
	}
	if here := row(rpcEntry + 1); !here.marked || here.bookmark || here.noted {
		t.Errorf("the row under review is %+v, want entry %d marked and without a bookmark or a note", here, rpcEntry+1)
	}
	if there := row(rpcEntry); there.marked || !there.bookmark || !there.noted {
		t.Errorf("the row of entry %d is %+v, want it unmarked with its bookmark and note", rpcEntry, there)
	}

	call.answer <- rpaAnswer{result: rpaFixture()}
	h.waitFor("the overtaken answer", func() bool { return h.answered == 1 })
	h.press("h")
	if h.s.step() != rpcEntry {
		t.Fatalf("h reached step %d, want %d", h.s.step(), rpcEntry)
	}
	frame := h.frame()
	_, panel = rpcPanel(h.s)
	all = strings.Join(panel, "\n")
	if !strings.Contains(all, rpcNote) || !strings.Contains(all, "bookmarked") {
		t.Errorf("back at step %d the panel lost the entry's note or bookmark:\n%s", rpcEntry, all)
	}
	if strings.Contains(frame, "engine's choice") || len(h.s.board.Highlights) != 0 {
		t.Errorf("back at step %d the analysis put away by moving came back:\n%s", rpcEntry, frame)
	}
}

// TestReplayComposeStaleNoteBesideAnAnalysisIsReplacedOnlyWhenShownWhole is the
// refused save of the notes with an analysis of the same entry on screen, whose
// lead also asks the panel for rows. A second confirm is a choice between two
// versions only while the other window's note is in the frame whole, so at
// each size either every word of it is drawn and confirming again replaces it,
// or some word is not and confirming again leaves the file byte for byte as it
// was and the input holding what was typed. The sizes are the note test's:
// no panel, a panel too short for the note, and panels narrow enough to wrap
// it onto many rows. At least one of the sizes that show the note whole has to
// draw the analysis in the same panel, or the two never competed.
func TestReplayComposeStaleNoteBesideAnAnalysisIsReplacedOnlyWhenShownWhole(t *testing.T) {
	const (
		entry  = 4
		theirs = "theirs-1 theirs-2 theirs-3 theirs-4 theirs-5 theirs-6 theirs-7"
	)
	sv := rpConnectionRecord(t)
	target := snTarget(t, sv)
	// unseen is the first word of the other note the frame does not show, or
	// nothing when it shows all of them.
	unseen := func(frame string) string {
		for _, word := range strings.Fields(theirs) {
			if !strings.Contains(frame, word) {
				return word
			}
		}
		return ""
	}
	var shown, hidden, alongside int
	for _, size := range append(slices.Clone(shellSizes), [2]int{24, 30}, [2]int{20, 40}) {
		w, ht := size[0], size[1]
		d := snDeps(t)
		path := snFile(d, sv.ID)
		eng := newRPAEngine()
		h := rpaOpen(t, d, sv, eng.analyse)
		h.feed(tea.WindowSizeMsg{Width: w, Height: ht})
		rpJump(t, h.s, strconv.Itoa(entry))
		rpcAnalysed(t, h, eng)
		if _, err := study.Open(d.ConfigDir).Set(target, 0, study.Mark{Entry: entry, Note: theirs}); err != nil {
			t.Fatal(err)
		}
		stored := snBytes(t, path)

		h.press("e")
		snType(t, h.s, "mine")
		h.press("enter")
		if !h.s.notes.editor.open {
			t.Fatalf("at %dx%d a save refused as stale closed the note input", w, ht)
		}
		if !h.s.analysis.shown(entry) {
			t.Fatalf("at %dx%d the note input put the analysis away", w, ht)
		}
		frame := h.frame()
		shellAssertFits(t, "a stale note beside an analysis", frame, w, ht)
		missing := unseen(frame)
		_, panel := rpcPanel(h.s)
		analysed := strings.Contains(strings.Join(panel, "\n"), "placement-only")

		h.press("enter")
		if missing != "" {
			hidden++
			if after := snBytes(t, path); !bytes.Equal(after, stored) {
				t.Errorf("at %dx%d %q of the other note is not on screen, and a second save replaced it:\n%s", w, ht, missing, frame)
				continue
			}
			if !h.s.notes.editor.open || h.s.notes.editor.edit.value() != "mine" {
				t.Errorf("at %dx%d the refused save did not leave the input open holding what was typed", w, ht)
			}
			continue
		}
		shown++
		if analysed {
			alongside++
		}
		if h.s.notes.editor.open {
			t.Errorf("at %dx%d the other note is on screen whole and the second save was refused:\n%s", w, ht, h.frame())
			continue
		}
		snAssertMarks(t, snLoad(t, d, target), study.Mark{Entry: entry, Note: "mine"})
	}
	if shown == 0 || hidden == 0 || alongside == 0 {
		t.Fatalf("the other note was on screen whole at %d sizes, %d of them beside the analysis, and not at %d; the test needs all three",
			shown, alongside, hidden)
	}
}
