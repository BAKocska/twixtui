package netplay

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/BAKocska/twixtui/internal/game"
)

// positionTag prefixes the encoding so that a position hash can never collide
// with a digest this package computes over something else.
const positionTag = "twixt-position/1"

// Board coordinates are written as one byte each. This fails to compile if the
// engine ever allows a board larger than a byte can index.
const _ = uint8(game.MaxSize)

// PositionHash returns a hash of the position: the board, whose turn it is, the
// result, and the two further pieces of state that decide what may legally
// happen next, namely whether the swap option has been used and whose draw
// offer is standing.
//
// It is a function of the position alone and not of the moves that reached it,
// so two ends that reached the same board by different routes agree. It
// deliberately excludes the move count: the protocol carries the ply and a
// transcript digest separately, and folding history into a position hash would
// spoil the one job it has, which is answering "are we looking at the same
// board?".
func PositionHash(g *game.Game) string {
	sum := positionSum(g)
	return hex.EncodeToString(sum[:])
}

func positionSum(g *game.Game) [sha256.Size]byte {
	return sha256.Sum256(encodePosition(nil, g))
}

// encodePosition appends the canonical encoding of a position to dst.
//
// Records are fixed width and each section is tagged, so the encoding is
// injective: two positions produce the same bytes only if they are the same
// position. Pegs and links are walked in board order rather than in the order
// they were played, which is what makes the result independent of move order.
func encodePosition(dst []byte, g *game.Game) []byte {
	n := g.Size()
	res := g.Result()
	dst = append(dst, positionTag...)
	dst = binary.BigEndian.AppendUint16(dst, uint16(n))
	dst = append(dst, byte(g.Turn()), byte(res.Outcome), byte(res.Reason), byte(g.DrawOfferedBy()))
	if g.Swapped() {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}

	dst = append(dst, 'P')
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			if pl := g.At(p); pl != game.NoPlayer {
				dst = append(dst, byte(col), byte(row), byte(pl))
			}
		}
	}

	// Every link has exactly one endpoint from which it points in a canonical
	// direction, so walking the canonical directions of every hole visits each
	// link once.
	dst = append(dst, 'L')
	for row := range n {
		for col := range n {
			p := game.Point{Col: col, Row: row}
			mask := g.LinkMask(p)
			if mask == 0 {
				continue
			}
			for d := range game.Dir(game.NumDirs) {
				if !d.IsCanonical() || mask&(1<<d) == 0 {
					continue
				}
				l := game.Link{From: p, Dir: d}
				dst = append(dst, byte(col), byte(row), byte(d), byte(g.LinkOwner(l)))
			}
		}
	}
	return dst
}
