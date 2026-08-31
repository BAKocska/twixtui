package game

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestColumnNames(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 23: "X", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for col, want := range cases {
		if got := ColumnName(col); got != want {
			t.Errorf("ColumnName(%d) = %q, want %q", col, got, want)
		}
		back, err := ParseColumn(want)
		if err != nil {
			t.Errorf("ParseColumn(%q): %v", want, err)
			continue
		}
		if back != col {
			t.Errorf("ParseColumn(%q) = %d, want %d", want, back, col)
		}
	}
	for col := range 60 {
		if back, err := ParseColumn(ColumnName(col)); err != nil || back != col {
			t.Errorf("column %d does not round-trip: %d, %v", col, back, err)
		}
	}
}

func TestPointNotation(t *testing.T) {
	// A1 is the top-left corner hole, so the swap diagonal runs from A1.
	if got := (Point{Col: 0, Row: 0}).String(); got != "A1" {
		t.Errorf("top-left renders as %q, want A1", got)
	}
	if got := (Point{Col: 1, Row: 3}).String(); got != "B4" {
		t.Errorf("{1,3} renders as %q, want B4", got)
	}
	p, err := ParsePoint("b4")
	if err != nil {
		t.Fatalf("lower case rejected: %v", err)
	}
	if p != (Point{Col: 1, Row: 3}) {
		t.Errorf("ParsePoint(b4) = %v", p)
	}
	for _, bad := range []string{"", "4", "B", "B0", "B-1", "??", "1B"} {
		if _, err := ParsePoint(bad); err == nil {
			t.Errorf("ParsePoint(%q) should fail", bad)
		}
	}
	for col := range 30 {
		for row := range 30 {
			want := Point{Col: col, Row: row}
			got, err := ParsePoint(want.String())
			if err != nil || got != want {
				t.Fatalf("%v does not round-trip: %v, %v", want, got, err)
			}
		}
	}
}

// TestSwapExampleFromLittleGolem reproduces the worked example published in
// Little Golem's rules: after 1.B4, a swap turns the peg black and moves it to
// D2. It anchors both the notation and the reflection to an external source.
func TestSwapExampleFromLittleGolem(t *testing.T) {
	g := MustNew(Std)
	if _, err := g.PlayPeg(at("B4")); err != nil {
		t.Fatal(err)
	}
	if err := g.Swap(); err != nil {
		t.Fatal(err)
	}
	if g.At(at("D2")) != Horizontal {
		t.Fatalf("after swapping 1.B4 the peg should stand on D2 for the second player")
	}
	if g.At(at("B4")) != NoPlayer {
		t.Error("B4 should be empty after the swap")
	}
}

func TestLinkNotation(t *testing.T) {
	l, err := ParseLink("D4:E6")
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	if l.From != at("D4") || l.To() != at("E6") {
		t.Errorf("parsed %v", l)
	}
	// A dash separator is accepted too.
	l2, err := ParseLink("D4-E6")
	if err != nil || l2 != l {
		t.Errorf("dash form gave %v, %v", l2, err)
	}
	// Endpoint order does not matter: both give the canonical link.
	l3, err := ParseLink("E6:D4")
	if err != nil || l3 != l {
		t.Errorf("reversed form gave %v, %v", l3, err)
	}
	if got := l.String(); got != "D4:E6" {
		t.Errorf("Link.String() = %q, want D4:E6", got)
	}
	if _, err := ParseLink("D4:E5"); err != ErrNotKnightMove {
		t.Errorf("non-knight link: got %v", err)
	}
	if _, err := ParseLink("D4"); err == nil {
		t.Error("a single hole should not parse as a link")
	}
}

func TestPlayNotationOrdinaryMoves(t *testing.T) {
	g := MustNew(Std)
	for _, m := range []string{"D4", "D6", "E6", "E4"} {
		if err := g.PlayNotation(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if g.Ply() != 4 {
		t.Errorf("ply = %d, want 4", g.Ply())
	}
	if g.At(at("E4")) != Horizontal {
		t.Error("E4 should hold a horizontal peg")
	}
}

// TestPlayNotationDeclinedLink round-trips a move that withdraws an offered
// link, the notation feature that makes deliberate omission recordable.
func TestPlayNotationDeclinedLink(t *testing.T) {
	g := MustNew(Std)
	if err := g.PlayNotation("D4"); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("D6"); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("E6 ~D4:E6"); err != nil {
		t.Fatalf("declining a link in notation: %v", err)
	}
	l, _ := NewLink(at("D4"), at("E6"))
	if g.HasLink(l) {
		t.Error("the declined link is on the board")
	}
	// The transcript must say so, otherwise a replay would silently re-add it.
	got, err := g.MoveNotation(2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "~") {
		t.Errorf("move notation %q does not record the declined link", got)
	}
}

func TestPlayNotationSpecialTokens(t *testing.T) {
	g := MustNew(Std)
	if err := g.PlayNotation("B4"); err != nil {
		t.Fatal(err)
	}
	if err := g.PlayNotation("swap"); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !g.Swapped() {
		t.Error("swap token did not swap")
	}

	g2 := MustNew(Std)
	if err := g2.PlayNotation("D4"); err != nil {
		t.Fatal(err)
	}
	if err := g2.PlayNotation("resign"); err != nil {
		t.Fatal(err)
	}
	if g2.Result().Outcome != VerticalWins {
		t.Errorf("horizontal resigned, result = %+v", g2.Result())
	}

	g3 := MustNew(Std)
	_ = g3.PlayNotation("D4")
	if err := g3.PlayNotation("draw?"); err != nil {
		t.Fatal(err)
	}
	if err := g3.PlayNotation("draw!"); err != nil {
		t.Fatal(err)
	}
	if g3.Result().Outcome != Draw {
		t.Errorf("result = %+v, want a draw", g3.Result())
	}
}

func TestPlayNotationRejectsIllegal(t *testing.T) {
	g := MustNew(Std)
	for _, bad := range []string{"", "A4", "ZZ99", "D4 +bogus", "D4 ?X1"} {
		if err := g.PlayNotation(bad); err == nil {
			t.Errorf("PlayNotation(%q) should fail", bad)
		}
	}
	// A rejected move must leave nothing staged behind.
	if s := g.Staged(); s.PegPlaced || len(s.Added) > 0 || len(s.Removed) > 0 {
		t.Errorf("a rejected move left staged edits: %+v", s)
	}
	if g.Ply() != 0 {
		t.Errorf("ply = %d after only rejected moves", g.Ply())
	}
}

// TestTranscriptRoundTrip plays random games, writes the transcript, replays it
// and checks the resulting position is identical. A transcript that does not
// replay exactly would corrupt saved games and desync networked opponents.
func TestTranscriptRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 43))
	for _, rs := range []Ruleset{withSize(Std, 10), withSize(PP, 10), withSize(Classic3M, 8)} {
		for trial := range 12 {
			g := MustNew(rs)
			for range 25 {
				if g.Result().Over() {
					break
				}
				ps := g.LegalPlacements(g.Turn())
				if len(ps) == 0 {
					break
				}
				if _, err := g.PlayPeg(ps[rng.IntN(len(ps))]); err != nil {
					t.Fatal(err)
				}
			}
			transcript, err := g.Transcript()
			if err != nil {
				t.Fatalf("%s trial %d: transcript: %v", rs.Canonical(), trial, err)
			}
			replayed, err := ReplayTranscript(rs, transcript)
			if err != nil {
				t.Fatalf("%s trial %d: replay %q: %v", rs.Canonical(), trial, transcript, err)
			}
			if got, want := snapshot(replayed), snapshot(g); got != want {
				t.Fatalf("%s trial %d: replayed position differs\ntranscript: %s", rs.Canonical(), trial, transcript)
			}
			if replayed.Result() != g.Result() {
				t.Errorf("%s trial %d: result differs: %+v vs %+v", rs.Canonical(), trial, replayed.Result(), g.Result())
			}
		}
	}
}

// TestTranscriptRoundTripWithLinkEdits covers the harder case: transcripts of
// games where players declined offered links and removed older ones.
func TestTranscriptRoundTripWithLinkEdits(t *testing.T) {
	g := MustNew(Std)
	steps := []string{
		"D4",        // vertical opens
		"D6",        // horizontal
		"E6 ~D4:E6", // vertical places E6 but declines the offered link
		"E4",        // horizontal, which now links D6-E4 through the gap
		"C6",        // vertical, auto-links D4-C6
		"G4",        // horizontal
		"B4 -D4:C6", // vertical removes the older link, then places B4
	}
	for _, s := range steps {
		if err := g.PlayNotation(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	transcript, err := g.Transcript()
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	replayed, err := ReplayTranscript(Std, transcript)
	if err != nil {
		t.Fatalf("replay %q: %v", transcript, err)
	}
	if got, want := snapshot(replayed), snapshot(g); got != want {
		t.Fatalf("replayed position differs\ntranscript: %s", transcript)
	}
}

// TestReplayRejectsTamperedTranscript checks a corrupted record is refused
// rather than silently producing a different position.
func TestReplayRejectsTamperedTranscript(t *testing.T) {
	if _, err := ReplayTranscript(Std, "D4; D4"); err == nil {
		t.Error("replaying the same hole twice should fail")
	}
	if _, err := ReplayTranscript(Std, "A4"); err == nil {
		t.Error("replaying an illegal opening should fail")
	}
	if _, err := ReplayTranscript(Std, "D4; D6; E6 ~D4:E7"); err == nil {
		t.Error("declining a link that is not a knight's move should fail")
	}
}
