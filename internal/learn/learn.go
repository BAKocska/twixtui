// Package learn holds the interactive tutorial: an ordered set of lessons, each
// a sequence of steps that pairs a position on the board with a short piece of
// prose, and where useful with a task the learner answers by choosing a hole.
//
// The package renders nothing and reads no input. A step hands the UI a
// position, a set of holes worth marking, and a checker that turns the
// learner's chosen hole into feedback.
package learn

import (
	"strings"

	"github.com/BAKocska/twixtui/internal/game"
)

// tutorialSize is the board every lesson position is built on. Twelve is one of
// the documented board sizes, and it is small enough that a whole position and
// the prose beside it fit in an ordinary terminal. No rule behaves differently
// on it than on the standard twenty-four.
const tutorialSize = 12

// Rules returns the ruleset every lesson position assumes. Two of its settings
// are load-bearing rather than incidental: linking is deliberate, because one
// lesson teaches declining a link, and a player's own links block, because
// another teaches that they do. A caller must build the tutorial's game from
// this and not from the ruleset of whatever game the player was last in.
func Rules() game.Ruleset {
	rs := game.Std
	rs.Size = tutorialSize
	return rs
}

// Lesson is one topic, taught as a sequence of steps.
type Lesson struct {
	ID, Title, Summary string
	Steps              []Step
}

// Step is one screen of the tutorial.
type Step struct {
	// Text is the prose shown to the learner. It carries no line breaks, so the
	// UI may wrap it at any width.
	Text string
	// Setup is played onto a fresh game before the step, in game notation.
	Setup []string
	// Highlight marks holes the UI should call out.
	Highlight []game.Point
	// Task, when non-nil, requires the learner to make a move before
	// continuing.
	Task *Task
}

// Task is a move the learner has to find.
type Task struct {
	Prompt string
	// Answer is one hole that satisfies the task, so the UI can show the
	// learner an answer once they have struggled. Where several moves are
	// right, it is the one the prose talks about.
	Answer game.Point
	// Accept reports whether the learner's move satisfies the step, and returns
	// the feedback to show either way.
	//
	// It is given the position the step set up, with the learner's side to
	// move, and the hole the learner chose, including holes the learner is not
	// allowed to use: the two mistakes beginners actually make, reaching into
	// the opponent's border line and reaching for a corner, are illegal moves,
	// and a learner who is silently stopped from making them is never told why.
	// So the UI must pass the raw choice through rather than filtering it, and
	// play it onto the board only when Accept returns true. Accept never
	// modifies the game it is given.
	Accept func(g *game.Game, played game.Point) (ok bool, feedback string)
}

// Position returns a fresh game with the step's setup played onto it.
func (s Step) Position() (*game.Game, error) {
	return game.ReplayTranscript(Rules(), strings.Join(s.Setup, "; "))
}

// Lessons returns the tutorial in teaching order. The result must not be
// modified.
func Lessons() []Lesson { return lessons }

// Find returns the lesson with the given id. It is not called Lesson because
// the type of that name already holds the identifier.
func Find(id string) (Lesson, bool) {
	for _, l := range lessons {
		if l.ID == id {
			return l, true
		}
	}
	return Lesson{}, false
}
