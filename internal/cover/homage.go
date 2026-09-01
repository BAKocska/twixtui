package cover

import "math"

// The homage is the artwork the player actually sees, and it is composed, not
// converted: a wordmark over a violet sky, a chain of black pegs linked
// across an ochre board, a red peg towering at each flank. Those are the
// elements the 1962 lid is remembered by, drawn fresh. Composition beats
// projection here because at terminal resolution a painting dissolves into
// mush while a drawing can spend every cell deliberately — the evaluation in
// the development tree bears that out at every size tried.

// The palette is the period's: muted violet, ochre and red, with a warm
// cream for lettering. Nothing is pure black or white; the box was printed
// on cardboard, not lit on a screen. Every value is also chosen for where it
// lands in the xterm cube, because the same bytes must survive Depth256: the
// three sky bands all quantise to the same mauve — the gradient collapses to
// a flat sky there rather than splitting into a pink band and a blue one —
// the red stays a red instead of drifting to orange, and the watcher keeps a
// violet cast instead of greying out. Nudging a value here means checking
// which side of a cube midpoint it falls on, or a 256-colour terminal gets a
// different picture, not a coarser one.
var (
	homageSkyHigh = rgb{0xa8, 0x97, 0xb2}
	homageSky     = rgb{0x9d, 0x8b, 0xa8}
	homageSkyDusk = rgb{0xa1, 0x85, 0xa3}
	homageFar     = rgb{0x75, 0x63, 0x86}
	homageInk     = rgb{0x22, 0x1b, 0x26}
	homageCream   = rgb{0xea, 0xe0, 0xc9}
	homageBoard   = rgb{0xd3, 0xa2, 0x4b}
	homageHole    = rgb{0x6e, 0x56, 0x28}
	homageRed     = rgb{0xaa, 0x2d, 0x28}
	homageBlack   = rgb{0x2b, 0x24, 0x1f}
)

// The lines are this project's own, written to the register of a 1962 box lid
// rather than copied from one.
const (
	homageTagline = "A GAME OF BARRIERS FOR TWO"
	homageSlogan  = "WIT AGAINST WIT, WALL AGAINST WALL"
)

func renderHomage(w, h int, depth Depth) []string {
	mono := depth == DepthMono
	cv := newCanvas(w, h)
	if !mono {
		// The sky dims towards the horizon in three flat bands — printed
		// colour separations, not a screen gradient.
		cv.fill(0, 0, w, h/3, homageSkyHigh)
		cv.fill(0, h/3, w, 2*h/3, homageSky)
		cv.fill(0, 2*h/3, w, h, homageSkyDusk)
	}

	wmY, wmH := drawWordmark(cv, w, h)

	// The tagline needs the full mark above it and honest room below it;
	// squeezed against the scene it reads as noise, so it is the first thing
	// dropped.
	sceneTop := wmY + wmH + 1
	if w >= len(homageTagline)+2 && h >= 17 && wmH >= 5 {
		cv.text((w-len(homageTagline))/2, sceneTop, homageTagline, &homageCream, nil)
		sceneTop += 2
	}

	if h-sceneTop >= 4 {
		drawScene(cv, w, h, sceneTop, mono)
	}

	return cv.lines(depth)
}

// drawWordmark places the largest mark the box carries comfortably and
// reports where it ended up. Below the minimum size there is no room for
// letter shapes at all, so the name is written out plainly rather than drawn
// badly.
func drawWordmark(cv *canvas, w, h int) (y, rows int) {
	y = 0
	if h >= 14 {
		y = 1
	}
	if h >= 32 {
		y = 2
	}

	art := wordmarkCompact
	if w >= artWidth(wordmarkFull)+2 && h >= 14 {
		art = wordmarkFull
		sx, sy := 1, 1
		if w >= 2*artWidth(wordmarkFull)+4 && h >= 22 {
			sx = 2
		}
		if sx == 2 && h >= 40 {
			sy = 2
		}
		art = scaleQuadArt(art, sx, sy)
	}

	if artWidth(art) > w {
		cv.text(max(0, (w-5)/2), y, "TWIXT", &homageInk, nil)
		return y, 1
	}
	cv.sprite((w-artWidth(art))/2, y, art, &homageInk)
	return y, len(art)
}

// drawScene lays the board and pegs into the rows below sceneTop. Sizes and
// positions are fractions of the box rather than fixed counts, so the scene
// deepens as the box grows instead of rattling around in it: the board takes
// about a third of the band, the red flankers rise past the horizon, and the
// black chain recedes across the board between them.
func drawScene(cv *canvas, w, h, sceneTop int, mono bool) {
	bh := h - sceneTop
	boardH := clampInt(2+bh/3, 2, 12)
	boardTop := h - boardH

	// The board's edge is a half-block bevel: in colour it reads as the lit
	// rim of the platform, in monochrome as the horizon line the pegs stand
	// against.
	for x := range w {
		cv.set(x, boardTop, '▄', &homageBoard, nil)
	}
	if !mono {
		cv.fill(0, boardTop+1, w, h, homageBoard)
	}
	for y := boardTop + 1; y < h; y++ {
		for x := 1 + ((y-boardTop)%2)*2; x < w; x += 5 {
			cv.set(x, y, '•', &homageHole, nil)
		}
	}

	// The watcher: the lid's ghost of a player leaning over the board,
	// reduced to a background silhouette the pegs stand in front of. It is
	// painted only into the background, so it never fights the foreground
	// for runes and vanishes entirely in monochrome.
	if !mono && bh >= 18 && w >= 64 {
		drawWatcher(cv, w, sceneTop+1, boardTop)
	}

	// A second board rim far behind the pegs, only where there is room and
	// colour to keep it quiet: the game continuing out of frame. Monochrome
	// leaves it out — in runes alone it is indistinguishable from clutter.
	if !mono && bh >= 16 && w >= 70 {
		fy := boardTop - 2
		x0, x1 := w*34/100, w*58/100
		for x := x0 + 2; x < x1-1; x++ {
			cv.set(x, fy, '─', &homageFar, nil)
		}
		for _, fx := range []int{x0, (x0 + x1) / 2, x1} {
			cv.set(fx, fy, '█', &homageFar, nil)
			cv.set(fx, fy+1, '█', &homageFar, nil)
		}
	}

	frontPH := clampInt(bh*3/5, 4, 18)

	lx := max(2, 1+int(0.02*float64(w-3)+0.5))
	rx := min(w-3, 1+int(0.98*float64(w-3)+0.5))
	frontTop := h - frontPH

	// The black chain, linked and marching across the board as it recedes,
	// is the game itself in one line. The red flankers stand nearer the
	// viewer, unlinked: the barrier is the other player's. Caps are held
	// almost level — the feet recede, the heights absorb the difference —
	// because at cell resolution a slanted link is a staircase of diagonals
	// and a staircase reads as scratches, not as a rod.
	type peg struct {
		x, top, bottom int
	}
	n := clampInt(w/16, 2, 5)
	chainBase := h - 2 - boardH/3
	chainTop := max(sceneTop, boardTop-clampInt(bh/3, 3, 8))
	chain := make([]peg, 0, n)
	for i := range n {
		fx := 0.24 + 0.52*float64(i)/float64(n-1)
		x := 1 + int(fx*float64(w-3)+0.5)
		bottom := chainBase - i*(boardH-2)/max(1, n-1)
		if bottom < boardTop+1 {
			bottom = boardTop + 1
		}
		top := chainTop + i&1
		if bottom-top+1 < 3 {
			top = bottom - 2
		}
		if bottom-top+1 > 11 {
			// A chain peg has a height budget; past it the foot lifts,
			// which pushes the peg back into the scene instead of growing
			// it into a second flanker.
			bottom = top + 10
		}
		chain = append(chain, peg{x, top, bottom})
	}
	for i := range len(chain) - 1 {
		drawLink(cv, chain[i].x+2, chain[i].top, chain[i+1].x-1, chain[i+1].top, homageInk)
	}

	// A second red peg behind the left flanker, red-linked to it at cap
	// height, so red is seen to be building too rather than standing alone.
	hasBuddy := w >= 56 && frontPH >= 7
	var buddy peg
	if hasBuddy {
		buddy = peg{1 + int(0.15*float64(w-3)+0.5), frontTop, h - 2}
		drawLink(cv, lx+3, frontTop, buddy.x-2, buddy.top, homageRed)
	}

	for _, p := range chain {
		drawPeg(cv, p.x, p.bottom, p.bottom-p.top+1, homageBlack, mono)
	}
	if hasBuddy {
		drawPeg(cv, buddy.x, buddy.bottom, buddy.bottom-buddy.top+1, homageRed, false)
	}
	drawPeg(cv, lx, h-1, frontPH, homageRed, false)
	drawPeg(cv, rx, h-1, frontPH, homageRed, false)

	// The slogan sits printed on the board's near edge, clear of both red
	// flankers; if the box cannot give it that space it is not shown at all.
	if h >= 26 {
		sx := lx + 4
		if hasBuddy {
			sx = max(sx, buddy.x+4)
		}
		if sx+len(homageSlogan) <= rx-4 {
			// Padded by a cell each side so no peg hole crowds the words.
			cv.label(sx-1, h-2, " "+homageSlogan+" ", &homageInk)
		}
	}
}

// drawWatcher paints a head and shoulders into the sky's background between
// top and the horizon, one shade duskier than the sky around it: present
// when looked for, ignorable when not, which is the weight the lid gives its
// own ghost. Background-only, so every rune drawn later sits in front.
func drawWatcher(cv *canvas, w, top, horizon int) {
	ghost := rgb{0x8b, 0x71, 0x96}
	if horizon-top < 8 {
		return
	}
	// The figure keeps human proportions no matter how deep the sky is: it
	// grows to a point and then stops, seated behind the board rather than
	// filling the heavens.
	hgt := clampInt(horizon-top, 8, 18)
	top = horizon - hgt
	cx := w * 13 / 25
	headH := clampInt(hgt/4, 3, 5)
	// A cell is half as wide as it is tall, so a round-looking head needs
	// nearly twice as many columns as rows.
	headW := 2*headH - 1
	for y := range headH {
		v := (float64(y)+0.5)/float64(headH)*2 - 1
		hw := int(float64(headW) / 2 * math.Sqrt(1-v*v))
		cv.fill(cx-hw, top+y, cx+hw+1, top+y+1, ghost)
	}
	// One row of neck, two of flare, and level shoulders the rest of the
	// way down to the board.
	neckHW := headW/2 - 1
	cv.fill(cx-neckHW, top+headH, cx+neckHW+1, top+headH+1, ghost)
	shoulderHW := clampInt(w/6, headW, 20)
	for y := top + headH + 1; y < horizon; y++ {
		hw := shoulderHW
		switch y - (top + headH + 1) {
		case 0:
			hw = neckHW + (shoulderHW-neckHW)/3
		case 1:
			hw = neckHW + 2*(shoulderHW-neckHW)/3
		}
		cv.fill(cx-hw, y, cx+hw+1, y+1, ghost)
	}
}

// pegSprite draws the tapered silhouette at a given height: flared cap and
// foot, thin waist. Tall pegs widen so the foreground reads as nearer rather
// than merely stretched.
func pegSprite(ph int, shaded bool) []string {
	var rows []string
	switch {
	case ph <= 3:
		rows = []string{"███", " █ ", "███"}
	case ph < 9:
		rows = append(rows, "███", "▝█▘")
		for range ph - 4 {
			rows = append(rows, " █ ")
		}
		rows = append(rows, "▗█▖", "███")
	case ph < 14:
		rows = append(rows, "█████", "▝███▘", " ▝█▘ ")
		for range ph - 6 {
			rows = append(rows, "  █  ")
		}
		rows = append(rows, " ▗█▖ ", "▗███▖", "█████")
	default:
		rows = append(rows, "███████", "▝█████▘", " ▝███▘ ")
		for range ph - 6 {
			rows = append(rows, "  ▐█▌  ")
		}
		rows = append(rows, " ▗███▖ ", "▗█████▖", "███████")
	}
	if !shaded {
		return rows
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = shadeRow(r)
	}
	return out
}

// shadeRow substitutes the shaded fill monochrome uses for the black player's
// pegs — texture is the only channel left for telling the sides apart — and
// drops the corner quadrants, which have no shaded form.
func shadeRow(row string) string {
	rs := []rune(row)
	for i, r := range rs {
		switch r {
		case '█', '▐', '▌':
			rs[i] = '▓'
		case '▝', '▘', '▗', '▖':
			rs[i] = ' '
		}
	}
	return string(rs)
}

// drawPeg stands a peg with its centre column at x and its foot on row
// bottom. In colour both players' pegs are solid silhouettes; in monochrome
// the black player's are shaded, see shadeRow.
func drawPeg(cv *canvas, x, bottom, ph int, colour rgb, shadeBlack bool) {
	sprite := pegSprite(ph, shadeBlack && colour == homageBlack)
	cv.sprite(x-artWidth(sprite)/2, bottom-ph+1, sprite, &colour)
}

// drawLink draws a taut link between two attachment points, one rune per
// column: flat runs as dashes, each drop as a diagonal. Links are drawn
// before pegs so a peg cap occludes the line the way a nearer object should.
func drawLink(cv *canvas, x0, y0, x1, y1 int, colour rgb) {
	if x1 <= x0 {
		return
	}
	dx := x1 - x0
	dy := y1 - y0
	yAt := func(x int) int {
		return y0 + (2*dy*(x-x0)+dx)/(2*dx)
	}
	for x := x0; x <= x1; x++ {
		y := yAt(x)
		r := '─'
		if x < x1 {
			switch next := yAt(x + 1); {
			case next > y:
				r = '╲'
			case next < y:
				r = '╱'
			}
		}
		cv.set(x, y, r, &colour, nil)
	}
}
