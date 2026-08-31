package learn

import (
	"fmt"
	"slices"

	"github.com/BAKocska/twixtui/internal/game"
)

// The tutorial's content. Every position here is replayed and every task's
// answer is exercised by the package's tests, so a position that does not load
// or a task that rejects its own answer is a test failure rather than something
// the learner discovers.

var lessons = []Lesson{
	lessonBoard(),
	lessonLinks(),
	lessonBlocking(),
	lessonDoubleThreat(),
	lessonWinning(),
	lessonSwap(),
	lessonPractice(),
}

func lessonBoard() Lesson {
	return Lesson{
		ID:      "board",
		Title:   "The board and the borders",
		Summary: "The grid, the four missing corners, each player's border lines, and the one hole you may never use.",
		Steps: []Step{
			{
				Text: "TwixT is a game about joining opposite sides of a square grid of holes. " +
					"This tutorial uses a twelve by twelve grid so that a whole position fits on one screen; the boxed game uses twenty-four by twenty-four and changes nothing else. " +
					"Look at the four corners: they are missing. A corner hole would lie in a border line of each player at once, so the board leaves it out and no peg can ever stand there. " +
					"The highlighted holes are where the four border lines really begin and end.",
				Highlight: pts("B1", "A2", "K1", "L2", "A11", "B12", "L11", "K12"),
			},
			{
				Text: "One player joins the top of the board to the bottom. This tutorial calls that side Vertical, and Vertical moves first. " +
					"The highlighted holes are Vertical's own two border lines, the top row and the bottom row. " +
					"Vertical wins by building an unbroken run of its own pegs, each linked to the next, from a peg standing in the top row to a peg standing in the bottom row. " +
					"Reaching a border means a peg in it, not a peg near it.",
				Highlight: ownBorders(game.Vertical),
			},
			{
				Text: "The other player, Horizontal, joins the left edge to the right edge, and its border lines are the highlighted left and right columns. " +
					"The two goals cut across each other, so at most one of them can ever be finished, and finishing one ends the game on the spot. " +
					"If a position is reached where neither side can finish, the game is drawn, which almost never happens.",
				Highlight: ownBorders(game.Horizontal),
			},
			{
				Text: "A turn is simple: put exactly one peg of your own colour into an empty hole. There is no passing, and you never move a peg once it is down. " +
					"There is one restriction on where a peg may go, and it is the only one: never into your opponent's border line. " +
					"Your own border rows are open to you, and you have to use them, because that is where your chain has to begin and end.",
				Task: &Task{
					Prompt: "It is Vertical's move on an empty board. Play a peg anywhere Vertical is allowed to.",
					Answer: pt("F6"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						if g.IsBorderRow(game.Vertical, played) {
							return true, fmt.Sprintf("Legal, and worth noticing: %s is in one of Vertical's own border rows. Your own borders are always open to you, and a chain has to finish in both of them.", played)
						}
						return true, fmt.Sprintf("Good. %s is fine: every empty hole is open to you except the corners and the two columns that belong to Horizontal.", played)
					},
				},
			},
		},
	}
}

func lessonLinks() Lesson {
	anchor := pt("F6")
	// Both blocking positions put the same link, F7 to H8, across the line from
	// F6 to G8; the first one belongs to Horizontal and the second to Vertical,
	// so the learner meets the identical geometry twice and can only conclude
	// that the colour of the blocking link makes no difference.
	opponentBlock := []string{"F6", "F7", "F2", "H8"}
	ownBlock := []string{"F6", "A5", "F7", "B7", "H8", "A9"}
	declined := []string{"F6", "A5", "F7", "B7", "H8 ~F7:H8", "A9"}
	gateway := pt("G8")

	// downwardLink accepts a peg that links to the anchor and makes ground
	// towards the bottom row, which is where Vertical has to go.
	downwardLink := func(success string) func(*game.Game, game.Point) (bool, string) {
		return func(g *game.Game, played game.Point) (bool, string) {
			if problem := placementProblem(g, played); problem != "" {
				return false, problem
			}
			if problem := linkProblem(g, anchor, played); problem != "" {
				return false, problem
			}
			if played.Row <= anchor.Row {
				return false, fmt.Sprintf("%s links to %s, so that much is right, but it is not below it: you would be heading back up the board, and Vertical has to reach the bottom row.", played, anchor)
			}
			return true, fmt.Sprintf(success, played)
		}
	}

	return Lesson{
		ID:      "links",
		Title:   "Pegs and links",
		Summary: "How pegs join up, why the join is a knight's move, why links block each other, and why you may refuse one.",
		Steps: []Step{
			{
				Setup: []string{"F6", "C9"},
				Text: "A peg on its own achieves nothing; what wins the game is pegs joined by links. " +
					"Two of your own pegs may be linked when they stand a knight's move apart, exactly as a knight jumps in chess: two holes one way and one hole the other. " +
					"The highlighted holes are every hole that could ever link to the peg on F6, eight of them and no more. " +
					"Holes side by side never link, and neither do diagonal neighbours, which is the first thing most new players get wrong. " +
					"The knight's move is what makes this a contest rather than a race: it is long enough that the two players' links run across one another, and a link that would cross an existing link cannot be made at all.",
				Highlight: pts("G4", "H5", "H7", "G8", "E8", "D7", "D5", "E4"),
			},
			{
				Setup: []string{"F6", "C9"},
				Text:  "Links are made for you as the peg goes down: place a peg a knight's move from one of your own and the link appears.",
				Task: &Task{
					Prompt: "Vertical has a peg on F6. Play a second Vertical peg that links to it.",
					Answer: pt("G8"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						if problem := linkProblem(g, anchor, played); problem != "" {
							return false, problem
						}
						return true, fmt.Sprintf("Good: %s is a knight's move from F6, so the link is made the moment the peg goes down.", played)
					},
				},
			},
			{
				Setup: opponentBlock,
				Text: "Links block links. Horizontal has linked F7 to H8, and that link lies straight across Vertical's way down the board. " +
					"Nothing stops Vertical putting a peg into any legal hole, but a link that would cross an existing link is never made, so a peg can land and achieve nothing at all.",
				Task: &Task{
					Prompt: "Vertical has to work down the board from F6. Play a peg below F6 that really does link to it.",
					Answer: pt("H7"),
					Accept: downwardLink("Good: the line from F6 to %s runs clear of Horizontal's F7 to H8 link, so the link is made and Vertical is a row further down."),
				},
			},
			{
				Setup: ownBlock,
				Text: "Here is the same shape with the colours changed: this time the link from F7 to H8 is Vertical's own. " +
					"Under these rules your own links block you exactly as your opponent's do. " +
					"Newcomers assume their own links are friendly, and they are not: a careless link walls off your own best route for as long as it stays there. " +
					"It need not stay, though: your own links may be taken off the board again, before you place your peg on any later turn, and doing so frees whatever they were crossing. " +
					"You can never remove your opponent's links, only your own, so the same mistake made by your opponent is one you have to play around.",
				Task: &Task{
					Prompt: "Again, play a Vertical peg below F6 that links to it.",
					Answer: pt("H7"),
					Accept: downwardLink("Good: the line from F6 to %s clears your own F7 to H8 link, so this time the link is made."),
				},
			},
			{
				Setup: declined,
				Text: "There is a way out of that trap. Linking is a choice, not something the board does to you: when a peg goes down you are offered every link it could make, and you may withdraw any of them before you finish your turn. " +
					"Vertical has just played H8 and declined the link to F7. A link that could have been made but was not is no barrier at all, so the highlighted G8 is still available, and with it the route down from F6 that the F7 to H8 link would have cut off. " +
					"Declining costs you nothing but the join itself.",
				Highlight: pts("G8"),
			},
			{
				Setup: declined,
				Text:  "The same position again. This is what the declined link bought.",
				Task: &Task{
					Prompt: "Play G8, the hole the declined link kept open, and watch the link appear.",
					Answer: pt("G8"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						if played != gateway {
							if problem := linkProblem(g, anchor, played); problem != "" {
								return false, problem
							}
							return false, fmt.Sprintf("%s links to F6 as well, and it is a perfectly sound move, but this step is about G8: it was closed in the position before this one and is open in this one, purely because the F7 to H8 link was never made.", played)
						}
						return true, "There it is. Two positions ago that same peg made no link at all. Nothing changed except that the link across its path was declined rather than taken."
					},
				},
			},
		},
	}
}

func lessonBlocking() Lesson {
	// Horizontal's chain ends at C6 and can reach its left border only through
	// A5 or A7. Vertical cannot occupy either, since column A is Horizontal's
	// border, so the only answer is a link of Vertical's own across both
	// routes: B5 to C7 crosses each of them.
	borderFight := []string{"E1", "C6", "G2", "E7", "B5", "G8"}
	target := pt("C6")

	return Lesson{
		ID:      "blocking",
		Title:   "Blocking",
		Summary: "What a peg obstructs, what a link obstructs, and why the fight is decided at the border.",
		Steps: []Step{
			{
				Setup: []string{"F6", "G7"},
				Text: "Both sides build at once, so most good moves do two jobs: they extend your own chain and they get in your opponent's way. It pays to be exact about what obstructs what. " +
					"A peg obstructs one thing only: its own hole. That can still decide a game, because a hole your opponent needed is a hole they will never get; had Horizontal taken the highlighted H7 instead, that route down would have gone for good. " +
					"But Horizontal's peg on G7 only looks as though it stands between F6 and H7, and it makes no difference whatever: a link runs between the holes and never through one, so no peg can ever be in a link's way. " +
					"Only a link blocks a link, and that is the other half of blocking.",
				Highlight: pts("H7"),
			},
			{
				Setup: borderFight,
				Text: "So blocking works where your opponent has fewest choices, and the tightest place on the board is the border. " +
					"Horizontal's chain ends at C6 and has to reach the left column. From C6 there are exactly two holes in that column a knight's move away, the highlighted A5 and A7. " +
					"Vertical cannot simply take one of them, because column A is Horizontal's border line and no peg of Vertical's may go there. " +
					"Vertical has to stop the links instead, and one link of Vertical's own, put in the right hole, crosses both of them at once.",
				Highlight: pts("A5", "A7"),
			},
			{
				Setup: borderFight,
				Text:  "Vertical already has a peg on B5. One more peg finishes the job.",
				Task: &Task{
					Prompt: "It is Vertical's move. Play the one peg that cuts C6 off from the left column for good.",
					Answer: pt("C7"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						c, _, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						left := borderReach(c, game.Horizontal, target)
						if len(left) == 0 {
							return true, fmt.Sprintf("That is it. Your link from B5 to %s crosses the line from C6 to A5 and the line from C6 to A7 at the same time, so while that link stands C6 cannot reach the left column at all. Horizontal cannot lift it, because a player may only ever remove their own links, so Horizontal has to start again somewhere else.", played)
						}
						if c.LinkMask(played) == 0 {
							return false, fmt.Sprintf("%s makes no link of yours at all, so it crosses nothing, and a lone peg cannot stop a link. Horizontal still links C6 to %s. Vertical's peg on B5 is one end of the link you need.", played, names(left))
						}
						return false, fmt.Sprintf("Closer: your link is on the board, but Horizontal still links C6 to %s. One link has to cross both of C6's routes into column A, not just one of them.", names(left))
					},
				},
			},
		},
	}
}

func lessonDoubleThreat() Lesson {
	anchor := pt("E7")
	partner := pt("F10")
	probed := []string{"E7", "A3", "F10", "G8"}

	return Lesson{
		ID:      "double-threat",
		Title:   "Two ways to connect",
		Summary: "The setup: a pair of pegs your opponent cannot separate, and the idea the whole game turns on.",
		Steps: []Step{
			{
				Setup: []string{"E7", "A3", "F10", "C4"},
				Text: "Here is the idea the game really turns on. Vertical's pegs on E7 and F10 are not linked: they stand one column across and three rows apart, which is not a knight's move. " +
					"But two different holes would link to both of them, the highlighted G8 and D9, and Vertical need not play either yet. " +
					"If Horizontal takes one, Vertical takes the other. A threat that works two ways cannot be answered by one peg. " +
					"Players call the pair a setup, and a run of setups travels down the board far faster than a run of finished links. " +
					"Notice that the two joining holes are three columns apart, so no single enemy peg is near enough to threaten both.",
				Highlight: pts("G8", "D9"),
			},
			{
				Setup: probed,
				Text:  "Horizontal has taken one of the two joining holes, which is the natural try.",
				Task: &Task{
					Prompt: "Horizontal has taken G8. Join E7 to F10 anyway.",
					Answer: pt("D9"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem + " A setup keeps a second route for exactly this reason: use the other one."
						}
						c, _, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						toAnchor, toPartner := linked(c, played, anchor), linked(c, played, partner)
						switch {
						case toAnchor && toPartner:
							return true, fmt.Sprintf("Good. One peg on %s makes both links at once, so E7 and F10 are a single chain. Horizontal gained a peg on G8 and gained nothing else.", played)
						case toAnchor:
							return false, fmt.Sprintf("%s links to E7 but not to F10, so your two groups are still strangers. Count the knight's moves to both pegs before you commit.", played)
						case toPartner:
							return false, fmt.Sprintf("%s links to F10 but not to E7, so your two groups are still strangers. Count the knight's moves to both pegs before you commit.", played)
						}
						return false, fmt.Sprintf("Neither link is made. %s", linkProblem(g, anchor, played))
					},
				},
			},
			{
				Setup: []string{"E7", "A3"},
				Text:  "Making a setup is an ordinary move: you put a peg where two different holes would join it to a peg you already have.",
				Task: &Task{
					Prompt: "Vertical has a single peg on E7 and has to work down the board. Play a peg further down that keeps two ways of linking back to E7.",
					Answer: pt("F10"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						if _, ok := game.NewLink(anchor, played); ok {
							return false, fmt.Sprintf("%s is a knight's move from E7, so the link is made straight away. That is safe but slow, and it is not what this step asks for: a made link commits you to one route and buys a row or two. Look for a hole that is not linked to E7 at all, but that two other holes would join to it.", played)
						}
						if played.Row <= anchor.Row {
							return false, fmt.Sprintf("%s is not below E7, so it makes no ground towards the bottom row. A setup is worth having when it travels.", played)
						}
						c, _, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						joins := joiningHoles(c, game.Vertical, anchor, played)
						switch len(joins) {
						case 0:
							return false, fmt.Sprintf("Nothing joins %s to E7: no single hole is a knight's move from both. %s", played, offsetProblem(anchor, played))
						case 1:
							return false, fmt.Sprintf("Only %s would join %s to E7, so Horizontal takes %s and your two pegs are strangers. A setup needs two joining holes, not one.", names(joins), played, names(joins))
						}
						dc, dr := abs(played.Col-anchor.Col), abs(played.Row-anchor.Row)
						if (dc == 1 && dr == 3) || (dc == 3 && dr == 1) {
							shape, gain := "one column across and three rows down", "Three rows of ground for one peg, and nothing given away."
							if dc == 3 {
								shape, gain = "three columns across and one row down", "Less ground towards the bottom row, but the same pair your opponent cannot break."
							}
							return true, fmt.Sprintf("That is the shape: E7 and %s stand %s, and %s both join them, so Horizontal cannot take one and stop the other. %s",
								played, shape, names(joins), gain)
						}
						if adjacent(joins[0], joins[1]) {
							return false, fmt.Sprintf("Right idea, wrong shape: %s and %s do join %s to E7, but they are touching, so one enemy peg with a link can threaten both at once. The shape players rely on puts the joining holes well apart: one column across and three rows down, or three columns across and one row down.",
								joins[0], joins[1], played)
						}
						return false, fmt.Sprintf("%s and %s do join %s to E7, but the pair barely travels: you have spent a whole move to gain %s. Aim for one column across and three rows down, or three columns across and one row down.",
							joins[0], joins[1], played, plural(dr, "row"))
					},
				},
			},
		},
	}
}

func lessonWinning() Lesson {
	// A chain from C1 in the top row down to H11, one knight's move short of
	// the bottom row. Horizontal has spent its moves in columns K and L, too
	// far away for any of its links to cross the chain.
	chain := []string{"C1", "L4", "D3", "K3", "E5", "L6", "F7", "K5", "G9", "L8", "H11", "K7"}
	finished := append(slices.Clone(chain), "J12")
	tip := pt("H11")

	return Lesson{
		ID:      "winning",
		Title:   "Winning the game",
		Summary: "What a finished chain looks like, and how to put the last peg into it.",
		Steps: []Step{
			{
				Setup: chain,
				Text: "Vertical has a run of linked pegs from C1, in the top border row, down to H11. One more peg finishes it. " +
					"Only a peg standing in the bottom row will do, because reaching a border means occupying it, and from H11 the knight's moves into the bottom row are the highlighted F12 and J12.",
				Highlight: pts("F12", "J12"),
			},
			{
				Setup: chain,
				Text:  "The chain runs C1, D3, E5, F7, G9, H11. Finish it.",
				Task: &Task{
					Prompt: "Play the peg that wins the game for Vertical.",
					Answer: pt("J12"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						_, res, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						if res.Winner() == game.Vertical {
							return true, fmt.Sprintf("That is the game. C1, D3, E5, F7, G9, H11, %s runs from the top row to the bottom row without a break, and the game ends the instant the chain is complete.", played)
						}
						if problem := linkProblem(g, tip, played); problem != "" {
							return false, "The open end of the chain is H11. " + problem
						}
						if !g.IsBorderRow(game.Vertical, played) {
							return false, fmt.Sprintf("%s does link to H11, so the chain grows, but it stops short: a peg near the bottom row is not in it. Vertical needs a peg standing in row 12 itself.", played)
						}
						return false, fmt.Sprintf("%s does not finish the chain.", played)
					},
				},
			},
			{
				Setup: finished,
				Text: "That is a finished game. Vertical's chain, highlighted, reaches both of its own border rows, so Vertical has won and nothing Horizontal has on the board matters any longer. " +
					"Because the two players' chains would have to cross one another, and links may not cross, only one of the two can ever be completed: there is no position in which both have won. " +
					"A draw needs a position in which neither side can finish at all, which is very rare.",
				Highlight: pts("C1", "D3", "E5", "F7", "G9", "H11", "J12"),
			},
		},
	}
}

func lessonSwap() Lesson {
	return Lesson{
		ID:      "swap",
		Title:   "The swap rule",
		Summary: "Why the second player may take the first peg, and what that does to the board.",
		Steps: []Step{
			{
				Setup: []string{"F4"},
				Text: "Moving first is worth something, and near the middle of the board it is worth a great deal. The swap rule takes the sting out of that. " +
					"Vertical has played the opening peg on F4, and Horizontal now has a choice it will never have again: reply normally, or swap. " +
					"Swapping hands that first peg over. It changes colour and mirrors across the board's main diagonal, so it lands on the highlighted D6 as a Horizontal peg, and Vertical moves next. " +
					"The peg moves rather than the players changing seats because that leaves the borders and the coordinates where they were, which is how online play and written game records handle it.",
				Highlight: pts("D6"),
			},
			{
				Setup: []string{"F4", "swap"},
				Text: "This is the position after the swap. F4 is empty, Horizontal owns D6, and it is Vertical's move. " +
					"The effect is that the first player has to choose an opening they would not mind handing over, so the first move becomes a real decision rather than a free gift. " +
					"Swap is only ever offered in reply to the very first peg of a game, never later. " +
					"It is also a later addition to the rules: the original 1962 set has no swap, which is why this program lets you turn it off.",
				Highlight: pts("D6"),
			},
		},
	}
}

func lessonPractice() Lesson {
	// Two groups of Vertical pegs, one hanging from the top row and one
	// standing on the bottom row, with a single hole joining them: F7 is a
	// knight's move from both E5 and G9 and is the only move that wins.
	winPuzzle := []string{"C1", "K2", "D3", "L4", "E5", "K6", "G9", "L8", "H11", "K10", "J12", "J7"}
	topTip, bottomTip := pt("E5"), pt("G9")

	// Horizontal has a group on each border and G6 is the only hole that links
	// to both, so a Vertical peg there is the only defence.
	savePuzzle := []string{"C1", "A3", "E2", "C4", "G1", "E5", "I2", "I7", "K1", "K8", "J3", "L10"}

	return Lesson{
		ID:      "practice",
		Title:   "Practice",
		Summary: "Two positions to settle: one to win on the spot, one to save.",
		Steps: []Step{
			{
				Setup: winPuzzle,
				Text: "Vertical has two groups: one hanging from the top row, one standing on the bottom row, with a gap between them. " +
					"Horizontal has spent its moves over on the right and is nowhere near finishing.",
				Task: &Task{
					Prompt: "Vertical to move. Find the move that wins the game at once.",
					Answer: pt("F7"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						c, res, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						if res.Winner() == game.Vertical {
							return true, fmt.Sprintf("Yes. %s is a knight's move from E5 and from G9, so one peg makes both links, and the two groups become one chain from the top row to the bottom row.", played)
						}
						top, bottom := linked(c, played, topTip), linked(c, played, bottomTip)
						switch {
						case top:
							return false, fmt.Sprintf("%s links to E5 at the bottom of your top group, but not to G9 at the top of your bottom group, so the gap is still there.", played)
						case bottom:
							return false, fmt.Sprintf("%s links to G9 at the top of your bottom group, but not to E5 at the bottom of your top group, so the gap is still there.", played)
						}
						return false, fmt.Sprintf("%s makes no link at all. Look at the gap between E5 and G9, and find the hole that is a knight's move from each of them.", played)
					},
				},
			},
			{
				Setup: savePuzzle,
				Text: "Now the other way round. Horizontal has a group on the left border and a group on the right border, and is one peg away from joining them. " +
					"Vertical's own pegs are strung along the top and are nowhere near finishing, so this move is purely about survival.",
				Task: &Task{
					Prompt: "Vertical to move. Stop Horizontal from winning next move.",
					Answer: pt("G6"),
					Accept: func(g *game.Game, played game.Point) (bool, string) {
						if problem := placementProblem(g, played); problem != "" {
							return false, problem
						}
						c, res, err := after(g, played)
						if err != nil {
							return false, fmt.Sprintf("%s cannot be played here: %v.", played, err)
						}
						if res.Over() {
							return false, fmt.Sprintf("%s ends the game the wrong way round.", played)
						}
						if _, threatened := immediateWin(c); !threatened {
							return true, fmt.Sprintf("Correct. %s was the only hole linking to both E5 and I7, so it was Horizontal's only join; with a Vertical peg in it, Horizontal has to find a new route and several moves of its work are wasted.", played)
						}
						return false, fmt.Sprintf("%s does not stop it: Horizontal still joins its two groups with one peg. Look for the single hole that is a knight's move from both E5 and I7, and take it before Horizontal does.", played)
					},
				},
			},
		},
	}
}
