package cover

// The wordmark is the project's own lettering: five block capitals with a
// dotted lowercase i in the middle, which is the one typographic gesture the
// 1962 box is remembered by. The letters are drawn once, in quadrant-block
// art, and scaled by expanding their bit grid, so the same shapes serve a
// 24-column banner and a 120-column one without a second drawing.

// wordmarkFull is the five-row mark, 27 columns wide.
var wordmarkFull = joinLetters(
	[]string{
		"█████",
		"▀▀█▀▀",
		"  █  ",
		"  █  ",
		" ▄█▄ ",
	},
	[]string{
		"█     █",
		"█  ▄  █",
		"█  █  █",
		"█ █ █ █",
		"▝█▘ ▝█▘",
	},
	[]string{
		"█",
		" ",
		"█",
		"█",
		"█",
	},
	[]string{
		"█▖ ▗█",
		" █ █ ",
		"  █  ",
		" █ █ ",
		"█▘ ▝█",
	},
	[]string{
		"█████",
		"▀▀█▀▀",
		"  █  ",
		"  █  ",
		" ▄█▄ ",
	},
)

// wordmarkCompact is the three-row mark, 19 columns wide, for boxes near the
// minimum size. The dotted i survives as a half-block dot over a two-row
// stem; everything else is pared to the strokes a letter cannot lose.
var wordmarkCompact = joinLetters(
	[]string{
		"███",
		" █ ",
		" █ ",
	},
	[]string{
		"█   █",
		"█ █ █",
		" █ █ ",
	},
	[]string{
		"▄",
		"█",
		"█",
	},
	[]string{
		"█ █",
		" █ ",
		"█ █",
	},
	[]string{
		"███",
		" █ ",
		" █ ",
	},
)

// joinLetters lays letter sprites side by side with one column between them.
func joinLetters(letters ...[]string) []string {
	rows := len(letters[0])
	out := make([]string, rows)
	for i, l := range letters {
		for r := range rows {
			if i > 0 {
				out[r] += " "
			}
			out[r] += l[r]
		}
	}
	return out
}

// quadBits maps the block elements the letter art is drawn with to their 2x2
// quadrant bitmap: bit 1 top-left, 2 top-right, 4 bottom-left, 8 bottom-right.
// It is the inverse of quadRunes, spelled out because only part of the family
// appears in the art and a loop over quadRunes would hide which part.
var quadBits = map[rune]int{
	' ': 0, '▘': 1, '▝': 2, '▀': 3, '▖': 4, '▌': 5, '▞': 6, '▛': 7,
	'▗': 8, '▚': 9, '▐': 10, '▜': 11, '▄': 12, '▙': 13, '▟': 14, '█': 15,
}

// scaleQuadArt magnifies block art by whole factors. The art is exploded to
// its quadrant bits, the bit grid is scaled, and the bits are regrouped into
// runes, which is what keeps a tapered edge tapered: a ▝ scaled wide becomes
// " ▀", not "▝▝".
func scaleQuadArt(art []string, sx, sy int) []string {
	if sx == 1 && sy == 1 {
		return art
	}
	h := len(art)
	w := 0
	grid := make([][]bool, 2*h)
	for y, row := range art {
		top := make([]bool, 0, 2*len(row))
		bottom := make([]bool, 0, 2*len(row))
		for _, r := range row {
			bits := quadBits[r]
			top = append(top, bits&1 != 0, bits&2 != 0)
			bottom = append(bottom, bits&4 != 0, bits&8 != 0)
		}
		grid[2*y] = top
		grid[2*y+1] = bottom
		if len(top)/2 > w {
			w = len(top) / 2
		}
	}
	bit := func(bx, by int) bool {
		row := grid[by/sy]
		i := bx / sx
		return i < len(row) && row[i]
	}
	out := make([]string, h*sy)
	for y := range h * sy {
		row := make([]rune, w*sx)
		for x := range w * sx {
			bits := 0
			if bit(2*x, 2*y) {
				bits |= 1
			}
			if bit(2*x+1, 2*y) {
				bits |= 2
			}
			if bit(2*x, 2*y+1) {
				bits |= 4
			}
			if bit(2*x+1, 2*y+1) {
				bits |= 8
			}
			row[x] = quadRunes[bits]
		}
		out[y] = string(row)
	}
	return out
}

// artWidth is the widest row of a sprite in cells. Rows are pure block
// elements and ASCII, one cell per rune.
func artWidth(art []string) int {
	w := 0
	for _, row := range art {
		if n := len([]rune(row)); n > w {
			w = n
		}
	}
	return w
}
