package cover

// The xterm 256-colour palette above the 16 named colours is a 6x6x6 cube
// (indices 16..231) and a 24-step grey ramp (232..255). The named 16 are left
// alone: terminals let players redefine them, so "colour 1" is whatever the
// player's scheme says red is, and a cover quantised onto them would change
// with the scheme. The cube and ramp are fixed by convention, which is what a
// quantiser needs.

// cubeLevels are the channel values the cube actually contains. They are not
// evenly spaced: the step from 0 to 95 is nearly twice the later steps, a
// choice xterm made in 1999 and every emulator copied.
var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// xtermQuantise maps a colour to the nearest palette entry, returning the
// index and the colour that index actually shows. Nearest is by squared
// distance in sRGB. A perceptual space would be marginally better on paper,
// but the measured error against the reference scan (see the evaluation) was
// already well under a visible step, and sRGB keeps this a handful of integer
// operations per cell.
func xtermQuantise(c rgb) (uint8, rgb) {
	ci := cubeIndex(c.r)*36 + cubeIndex(c.g)*6 + cubeIndex(c.b)
	cube := rgb{cubeLevels[cubeIndex(c.r)], cubeLevels[cubeIndex(c.g)], cubeLevels[cubeIndex(c.b)]}

	gi := greyIndex(c)
	grey := rgb{greyLevel(gi), greyLevel(gi), greyLevel(gi)}

	if dist2(c, grey) < dist2(c, cube) {
		return uint8(232 + gi), grey
	}
	return uint8(16 + ci), cube
}

// cubeIndex returns the cube level nearest to one channel. The midpoints of
// the uneven ramp are precomputed; a scan through five thresholds beats a
// distance loop and reads as what it is.
func cubeIndex(v uint8) int {
	switch {
	case v < 48: // midpoint of 0 and 95
		return 0
	case v < 115: // midpoint of 95 and 135
		return 1
	default:
		return int(v-35) / 40
	}
}

// greyIndex returns the nearest grey-ramp step for a colour's average level.
// The ramp runs 8, 18, ... 238 in steps of ten.
func greyIndex(c rgb) int {
	avg := (int(c.r) + int(c.g) + int(c.b)) / 3
	gi := (avg - 3) / 10
	if gi < 0 {
		gi = 0
	}
	if gi > 23 {
		gi = 23
	}
	return gi
}

func greyLevel(gi int) uint8 {
	return uint8(8 + 10*gi)
}

func dist2(a, b rgb) int {
	dr := int(a.r) - int(b.r)
	dg := int(a.g) - int(b.g)
	db := int(a.b) - int(b.b)
	return dr*dr + dg*dg + db*db
}
