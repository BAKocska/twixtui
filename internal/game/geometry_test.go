package game

import (
	"fmt"
	"testing"
)

// allLinksNear enumerates every canonical link whose named endpoint lies within
// the given radius of the origin.
func allLinksNear(radius int) []Link {
	var out []Link
	for dCol := -radius; dCol <= radius; dCol++ {
		for dRow := -radius; dRow <= radius; dRow++ {
			for d := range Dir(4) {
				out = append(out, Link{From: Point{Col: dCol, Row: dRow}, Dir: d})
			}
		}
	}
	return out
}

// TestBlockerTableMatchesGeometry checks the precomputed table used at move time
// against the geometric rule it is derived from, over a neighbourhood far wider
// than the table's own search radius. If the table ever missed a crossing pair,
// illegal crossings would silently become legal.
func TestBlockerTableMatchesGeometry(t *testing.T) {
	const radius = 6
	for d := range Dir(4) {
		ref := Link{From: Point{}, Dir: d}
		inTable := map[Link]bool{}
		for _, b := range blockerTable[d] {
			inTable[Link{From: Point{Col: b.dCol, Row: b.dRow}, Dir: b.dir}] = true
		}
		for _, cand := range allLinksNear(radius) {
			if cand == ref {
				continue
			}
			want := LinksCross(ref, cand)
			got := inTable[cand]
			if want != got {
				t.Errorf("link %v vs %v: geometry says cross=%v, table says %v", ref, cand, want, got)
			}
		}
	}
}

// TestBlockerTableIsSymmetric checks that blocking is mutual: if A blocks B then
// B blocks A. An asymmetric table would make legality depend on move order.
func TestBlockerTableIsSymmetric(t *testing.T) {
	for d := range Dir(4) {
		a := Link{From: Point{}, Dir: d}
		for _, b := range blockerTable[d] {
			other := Link{From: Point{Col: b.dCol, Row: b.dRow}, Dir: b.dir}
			// Re-express a relative to other and look it up in other's table.
			found := false
			for _, rb := range blockerTable[other.Dir] {
				cand := Link{
					From: Point{Col: other.From.Col + rb.dCol, Row: other.From.Row + rb.dRow},
					Dir:  rb.dir,
				}
				if cand == a {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%v blocks %v but not the other way round", other, a)
			}
		}
	}
}

// TestLinkPairGeometryInvariants pins down the three geometric facts the engine
// relies on, so that a future change to the link offsets cannot quietly break
// the exact integer crossing test.
func TestLinkPairGeometryInvariants(t *testing.T) {
	const radius = 5
	links := allLinksNear(radius)
	origin := []Link{
		{From: Point{}, Dir: NNE},
		{From: Point{}, Dir: ENE},
		{From: Point{}, Dir: ESE},
		{From: Point{}, Dir: SSE},
	}

	t.Run("no link passes through a hole", func(t *testing.T) {
		// A knight's-move segment has no lattice point strictly between its
		// ends, so a peg can never sit on a link and only links block links.
		for _, l := range origin {
			a, b := l.Ends()
			for col := -radius; col <= radius; col++ {
				for row := -radius; row <= radius; row++ {
					p := Point{Col: col, Row: row}
					if p == a || p == b {
						continue
					}
					// Collinear with a-b and within the bounding box?
					cp := crossProduct(b.Col-a.Col, b.Row-a.Row, p.Col-a.Col, p.Row-a.Row)
					if cp != 0 {
						continue
					}
					between := min(a.Col, b.Col) <= p.Col && p.Col <= max(a.Col, b.Col) &&
						min(a.Row, b.Row) <= p.Row && p.Row <= max(a.Row, b.Row)
					if between {
						t.Errorf("hole %v lies on link %v", p, l)
					}
				}
			}
		}
	})

	t.Run("links sharing a peg never cross", func(t *testing.T) {
		for _, a := range links {
			for _, b := range links {
				if a == b {
					continue
				}
				a1, a2 := a.Ends()
				b1, b2 := b.Ends()
				shares := a1 == b1 || a1 == b2 || a2 == b1 || a2 == b2
				if shares && LinksCross(a, b) {
					t.Errorf("links %v and %v share a peg yet report crossing", a, b)
				}
			}
		}
	})

	t.Run("no two distinct links overlap along a line", func(t *testing.T) {
		// If two links could overlap collinearly, the strict cross-product test
		// would miss the conflict.
		for _, a := range links {
			for _, b := range links {
				if a == b {
					continue
				}
				a1, a2 := a.Ends()
				b1, b2 := b.Ends()
				d := crossProduct(a2.Col-a1.Col, a2.Row-a1.Row, b1.Col-a1.Col, b1.Row-a1.Row)
				e := crossProduct(a2.Col-a1.Col, a2.Row-a1.Row, b2.Col-a1.Col, b2.Row-a1.Row)
				if d != 0 || e != 0 {
					continue // not collinear
				}
				// Collinear: the open segments must not overlap.
				if segmentsOverlapCollinear(a1, a2, b1, b2) {
					t.Errorf("links %v and %v overlap collinearly", a, b)
				}
			}
		}
	})
}

// segmentsOverlapCollinear reports whether two known-collinear segments share
// more than a single endpoint.
func segmentsOverlapCollinear(a1, a2, b1, b2 Point) bool {
	// Project onto the dominant axis.
	proj := func(p Point) int {
		if a1.Col != a2.Col {
			return p.Col
		}
		return p.Row
	}
	lo1, hi1 := min(proj(a1), proj(a2)), max(proj(a1), proj(a2))
	lo2, hi2 := min(proj(b1), proj(b2)), max(proj(b1), proj(b2))
	return min(hi1, hi2)-max(lo1, lo2) > 0
}

func TestOppositeDirection(t *testing.T) {
	for d := range Dir(NumDirs) {
		dc, dr := d.Offset()
		oc, or := d.Opposite().Offset()
		if dc != -oc || dr != -or {
			t.Errorf("%v offset (%d,%d) is not the negation of %v offset (%d,%d)", d, dc, dr, d.Opposite(), oc, or)
		}
		if d.Opposite().Opposite() != d {
			t.Errorf("%v: double opposite is not identity", d)
		}
	}
}

func TestNewLinkCanonicalises(t *testing.T) {
	a := Point{Col: 5, Row: 5}
	for d := range Dir(NumDirs) {
		b := a.Add(d)
		l1, ok1 := NewLink(a, b)
		l2, ok2 := NewLink(b, a)
		if !ok1 || !ok2 {
			t.Fatalf("direction %v: knight move not recognised", d)
		}
		if l1 != l2 {
			t.Errorf("direction %v: NewLink is not symmetric: %v vs %v", d, l1, l2)
		}
		if !l1.Dir.IsCanonical() {
			t.Errorf("direction %v: canonical form has non-canonical dir %v", d, l1.Dir)
		}
	}
	if _, ok := NewLink(a, Point{Col: 6, Row: 6}); ok {
		t.Error("a diagonal step was accepted as a knight's move")
	}
	if _, ok := NewLink(a, a); ok {
		t.Error("a zero step was accepted as a knight's move")
	}
}

// TestKnownCrossingPair checks one hand-verified crossing so the whole geometric
// layer cannot pass by agreeing with itself.
func TestKnownCrossingPair(t *testing.T) {
	// The two long diagonals of the 2x3 box with corners (0,0) and (1,2):
	// (0,0)-(1,2) and (0,2)-(1,0) plainly cross in the middle of the box.
	a, _ := NewLink(Point{Col: 0, Row: 0}, Point{Col: 1, Row: 2})
	b, _ := NewLink(Point{Col: 0, Row: 2}, Point{Col: 1, Row: 0})
	if !LinksCross(a, b) {
		t.Fatalf("expected %v and %v to cross", a, b)
	}
	// Two parallel links one column apart do not cross.
	c, _ := NewLink(Point{Col: 4, Row: 0}, Point{Col: 5, Row: 2})
	d, _ := NewLink(Point{Col: 6, Row: 0}, Point{Col: 7, Row: 2})
	if LinksCross(c, d) {
		t.Fatalf("expected %v and %v not to cross", c, d)
	}
}

func TestBlockerTableSizes(t *testing.T) {
	// Recorded so that an accidental change in the offsets or the search radius
	// shows up as a failing test rather than as subtly different rules.
	for d := range Dir(4) {
		if len(blockerTable[d]) == 0 {
			t.Fatalf("direction %v has no blockers", d)
		}
	}
	got := fmt.Sprintf("%d/%d/%d/%d",
		len(blockerTable[NNE]), len(blockerTable[ENE]), len(blockerTable[ESE]), len(blockerTable[SSE]))
	const want = "9/9/9/9"
	if got != want {
		t.Errorf("blocker counts per direction = %s, want %s", got, want)
	}
}
