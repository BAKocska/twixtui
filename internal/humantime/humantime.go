// Package humantime renders an instant as the rough age a person would say out
// loud: "just now", "3 minutes ago", "yesterday".
//
// It lives in its own package because the command line and the interface both
// show the same facts — when a profile last played, when a game was last
// touched — and the same fact spelled two ways, or spelled "1 minutes ago",
// reads as a bug in the program rather than in its wording.
package humantime

import (
	"fmt"
	"time"
)

// Ago describes how long before now the moment then was. Anything older than a
// month is given as its date instead: "43 days ago" is harder to place than the
// day itself.
func Ago(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return count(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return count(int(d/time.Hour), "hour") + " ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return count(int(d/(24*time.Hour)), "day") + " ago"
	default:
		return then.Format("2 January 2006")
	}
}

// Since describes how long ago t was.
func Since(t time.Time) string { return Ago(time.Now(), t) }

// count pairs a number with its unit, singular when there is one of it.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
