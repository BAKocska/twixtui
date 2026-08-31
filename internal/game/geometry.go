package game

// Board geometry for TwixT.
//
// Coordinates are zero-based internally: Col 0 is the leftmost column of holes,
// Row 0 is the topmost row. Human notation counts rows from 1 at the top and
// names columns A, B, C ... (see notation.go), so internal {Col:1, Row:3}
// renders as "B4". The main diagonal used by the swap rule therefore runs from
// A1 (top-left) to the bottom-right corner, matching the SGF TwixT convention.
//
// Two pegs may be linked when they stand a chess knight's move apart. There are
// exactly eight such offsets, indexed 0..7 clockwise starting north-north-east.
// Index d and index d^4 are opposite directions, so Opposite(d) == (d+4)%8.

// Dir is one of the eight knight-move link directions.
type Dir uint8

// The eight link directions, clockwise from north-north-east. North is towards
// row 0, i.e. up on screen.
const (
	NNE Dir = 0
	ENE Dir = 1
	ESE Dir = 2
	SSE Dir = 3
	SSW Dir = 4
	WSW Dir = 5
	WNW Dir = 6
	NNW Dir = 7
)

// NumDirs is the number of link directions.
const NumDirs = 8

// dirOffsets holds the {dCol, dRow} displacement of each direction.
var dirOffsets = [NumDirs][2]int{
	NNE: {+1, -2},
	ENE: {+2, -1},
	ESE: {+2, +1},
	SSE: {+1, +2},
	SSW: {-1, +2},
	WSW: {-2, +1},
	WNW: {-2, -1},
	NNW: {-1, -2},
}

var dirNames = [NumDirs]string{"NNE", "ENE", "ESE", "SSE", "SSW", "WSW", "WNW", "NNW"}

// String returns the compass name of the direction.
func (d Dir) String() string {
	if d >= NumDirs {
		return "?"
	}
	return dirNames[d]
}

// Offset returns the column and row displacement of the direction.
func (d Dir) Offset() (dCol, dRow int) {
	o := dirOffsets[d]
	return o[0], o[1]
}

// Opposite returns the direction pointing the other way along the same link.
func (d Dir) Opposite() Dir { return (d + 4) % NumDirs }

// IsCanonical reports whether d is one of the four directions used to name a
// link uniquely. Every link has exactly one endpoint from which it points in a
// canonical direction, because no link direction has a zero column offset.
func (d Dir) IsCanonical() bool { return d < 4 }

// Point is a hole on the board.
type Point struct {
	Col int
	Row int
}

// Add returns the point displaced by the given direction.
func (p Point) Add(d Dir) Point {
	dc, dr := d.Offset()
	return Point{Col: p.Col + dc, Row: p.Row + dr}
}

// Link is an edge between two pegs a knight's move apart, named by its endpoint
// with the smaller column together with the canonical direction towards the
// other endpoint. Canonicalise with NewLink.
type Link struct {
	From Point
	Dir  Dir
}

// NewLink returns the canonical Link connecting a and b, and whether a and b are
// actually a knight's move apart.
func NewLink(a, b Point) (Link, bool) {
	dCol, dRow := b.Col-a.Col, b.Row-a.Row
	for d := range Dir(NumDirs) {
		o := dirOffsets[d]
		if o[0] == dCol && o[1] == dRow {
			if d.IsCanonical() {
				return Link{From: a, Dir: d}, true
			}
			return Link{From: b, Dir: d.Opposite()}, true
		}
	}
	return Link{}, false
}

// To returns the far endpoint of the link.
func (l Link) To() Point { return l.From.Add(l.Dir) }

// Ends returns both endpoints of the link.
func (l Link) Ends() (Point, Point) { return l.From, l.To() }

// Canonical returns the link named from its endpoint with the smaller column.
// A Link built by hand may point in any of the eight directions; anything that
// indexes a per-direction table needs the canonical form of the same edge.
func (l Link) Canonical() Link {
	if l.Dir.IsCanonical() {
		return l
	}
	return Link{From: l.To(), Dir: l.Dir.Opposite()}
}

// crossProduct returns the 2-D cross product of vectors (ax,ay) and (bx,by).
func crossProduct(ax, ay, bx, by int) int { return ax*by - ay*bx }

// segmentsProperlyCross reports whether the open segments p1p2 and p3p4
// intersect at a point interior to both. Endpoint contact and collinear overlap
// deliberately return false: two links that merely share a peg do not block each
// other. All coordinates are integers, so this test is exact.
func segmentsProperlyCross(p1, p2, p3, p4 Point) bool {
	d1 := crossProduct(p4.Col-p3.Col, p4.Row-p3.Row, p1.Col-p3.Col, p1.Row-p3.Row)
	d2 := crossProduct(p4.Col-p3.Col, p4.Row-p3.Row, p2.Col-p3.Col, p2.Row-p3.Row)
	d3 := crossProduct(p2.Col-p1.Col, p2.Row-p1.Row, p3.Col-p1.Col, p3.Row-p1.Row)
	d4 := crossProduct(p2.Col-p1.Col, p2.Row-p1.Row, p4.Col-p1.Col, p4.Row-p1.Row)
	return d1*d2 < 0 && d3*d4 < 0
}

// LinksCross reports whether two links geometrically cross, and is the single
// authority for the crossing rule. It is exact and colour-blind; whether a
// crossing is actually forbidden depends on the ruleset (see Ruleset.blocks).
func LinksCross(a, b Link) bool {
	a1, a2 := a.Ends()
	b1, b2 := b.Ends()
	return segmentsProperlyCross(a1, a2, b1, b2)
}

// blocker is a link that crosses a reference link, expressed relative to the
// reference link's From endpoint.
type blocker struct {
	dCol, dRow int
	dir        Dir
}

// blockerTable[d] lists every canonical link that crosses the canonical link
// {From: origin, Dir: d}, as offsets relative to that origin. It is derived once
// from LinksCross rather than transcribed, so the table and the geometric rule
// cannot drift apart.
var blockerTable = buildBlockerTable()

// blockerSearchRadius bounds the offsets examined when building the table. A
// knight's-move segment spans three columns and rows, so a crossing link's
// canonical endpoint can never be further than three holes away; four is used
// as a safety margin and the geometric test discards the rest.
const blockerSearchRadius = 4

func buildBlockerTable() [4][]blocker {
	var table [4][]blocker
	origin := Point{Col: 0, Row: 0}
	for d := range Dir(4) {
		ref := Link{From: origin, Dir: d}
		for dCol := -blockerSearchRadius; dCol <= blockerSearchRadius; dCol++ {
			for dRow := -blockerSearchRadius; dRow <= blockerSearchRadius; dRow++ {
				for other := range Dir(4) {
					cand := Link{From: Point{Col: dCol, Row: dRow}, Dir: other}
					if cand == ref {
						continue
					}
					if LinksCross(ref, cand) {
						table[d] = append(table[d], blocker{dCol: dCol, dRow: dRow, dir: other})
					}
				}
			}
		}
	}
	return table
}
