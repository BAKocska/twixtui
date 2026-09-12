package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

// ScoreBound says what a candidate's score is: a measurement, a bound, or
// nothing at all. The root measures the move it ends up recommending and asks
// every later move only whether it beats that one, so most alternatives come
// back bounded from above and nothing further about them is known.
type ScoreBound string

const (
	// BoundExact is a score the search measured inside a window that
	// contained it.
	BoundExact ScoreBound = "exact"
	// BoundUpper is a ceiling: the move was shown not to beat the best found,
	// and may be worth far less than the number says.
	BoundUpper ScoreBound = "upper"
	// BoundLower is a floor. Nothing in this package produces one; it is
	// named so that a consumer can render the case if one ever appears, and a
	// score must never be labelled with it for having been searched or chosen.
	BoundLower ScoreBound = "lower"
	// BoundUnscored marks a candidate no search scored: either no iteration
	// finished and the list is the ordering heuristic's, or the root could not
	// play the move at all. Its Score is zero because there is no number, not
	// because zero was measured.
	BoundUnscored ScoreBound = "unscored"
)

// AnalysisCandidate is one root placement together with what the search
// established about it.
type AnalysisCandidate struct {
	Move  game.Point
	Score int
	Bound ScoreBound
}

// AnalysisProgress is what one finished deepening iteration reached.
type AnalysisProgress struct {
	// Recommended is the best move of this iteration.
	Recommended game.Point
	// Candidates is this iteration's root list, best first, and belongs to the
	// observer: editing it cannot reach the search.
	Candidates []AnalysisCandidate
	// Stats is the work spent up to this iteration. StopReason is empty
	// because the search has not stopped.
	Stats SearchStats
}

// AnalysisOptions configures one Analyze call.
type AnalysisOptions struct {
	// Limits replace the guards the analysis searches under. A zero field
	// keeps the analysis default, which is the hint's: the highest-effort
	// levers the package has under a two-second guard.
	Limits Limits
	// OnComplete, when set, is called once for every deepening iteration that
	// finished, in order. It is not called for an iteration that was cut
	// short, for the ordering fallback that answers a search no iteration
	// finished, or for a win taken without searching: none of those is a
	// finished iteration.
	//
	// It runs synchronously inside the search, on the goroutine that called
	// Analyze, so blocking in it spends the search's own budget; and the game
	// it was called about is the search's working position, so it must not be
	// edited from here. The snapshot it is given is its own, and changing it
	// cannot change the search. Analyze does not keep the function after it
	// returns.
	OnComplete func(AnalysisProgress)
}

// AnalysisResult is one bounded reading of one position: what to play, what
// else was considered and on what evidence, the evaluation on either side of
// the move, and the explanation derived from those numbers.
//
// Everything in it belongs to the caller. The slices and the pointed-to values
// are copies, not views into the searcher's scratch.
type AnalysisResult struct {
	// Result is the result of the position analysed, and never the result of
	// the position the recommendation would produce.
	Result game.Result
	// Policy is the restriction the analysis ran under.
	Policy AnalysisPolicy
	// Before is the decomposition of the analysed position for the side to
	// move. On a finished game that is the side the game was left with: a
	// connection win does not hand the turn over.
	Before Terms
	// After is the decomposition once the recommended move is played, nil when
	// there is no recommendation.
	After *Terms
	// Recommended is the hole to play, nil for a finished game.
	Recommended *game.Point
	// Candidates are the root placements the search kept, best first. It is
	// empty for a finished game.
	Candidates []AnalysisCandidate
	// Reason is the code of the explanation — "advance", "only-defence",
	// "deadlock" and so on. It is empty for a finished game, which gets no
	// explanation at all.
	Reason string
	// Headline and Detail are the explanation in words, empty alongside an
	// empty Reason. They state only what the decomposition measured.
	Headline, Detail string
	// Highlight lists the holes the explanation refers to, the recommended one
	// first. It is empty for a finished game.
	Highlight []game.Point
	// Stats is what the search spent, nil for a finished game because no
	// search ran.
	Stats *SearchStats
}

// Analyze returns an owned position analysis under the same placement-only
// policy as Hint, with Max levers and a two-second default search guard.
// Scores and bounds describe the selective search, not optimal full-rule play;
// terminal facts come from the rules engine.
//
// Time, node and context stops return the last completed iteration, or an
// unscored ordering fallback. Stats describes search work only: Nodes excludes
// root/tactical probes, and Evaluations excludes post-search explanation.
// Limits are checked at work boundaries, not as a hard real-time deadline.
//
// Invalid limits, nil input, staged turns and nonterminal positions with no
// placement return errors. A terminal position returns its result and terms
// without a recommendation. The search temporarily makes and unmakes moves;
// callers must not read or mutate g concurrently.
func Analyze(ctx context.Context, g *game.Game, opts AnalysisOptions) (AnalysisResult, error) {
	p := hintParams()
	if err := opts.Limits.apply(&p); err != nil {
		return AnalysisResult{}, err
	}
	// A request of its own gets a searcher of its own: the tables and per-ply
	// buffers a bot keeps between moves are worth reusing, a one-off reading
	// of a position is not.
	out, _, _, err := analyzeOn(ctx, newSearcher(p), g, opts.OnComplete)
	return out, err
}

// analyzeOn is the one path behind both Analyze and Hint: guard the position,
// run a single search on s, and read the explanation out of that search rather
// than composing it beside a second one.
//
// The reason and the decomposition come back alongside the result so that the
// prose can be checked against the numbers it claims to describe. Both are
// meaningless when the result carries no explanation.
func analyzeOn(ctx context.Context, s *searcher, g *game.Game, observe func(AnalysisProgress)) (AnalysisResult, reason, deltas, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if g == nil {
		return AnalysisResult{}, reasonBalanced, deltas{}, errors.New("bot: no game")
	}
	if staged(g) {
		// The search takes its trial moves back, and taking a move back
		// restores the position from before the turn, so the staged edits
		// would be searched and then thrown away.
		return AnalysisResult{}, reasonBalanced, deltas{}, ErrStagedTurn
	}
	me := g.Turn()
	result := g.Result()
	if result.Over() {
		// A finished game has no move to search for. It still has a position,
		// so it gets its result and its decomposition and nothing beyond them:
		// no recommendation, no candidates, no prose and no search to report.
		var an analysis
		an.templates = s.p.templates
		an.load(g)
		return AnalysisResult{
			Result:     result,
			Policy:     PlacementOnlyPolicy(),
			Before:     an.terms(me),
			Candidates: []AnalysisCandidate{},
			Highlight:  []game.Point{},
		}, reasonBalanced, deltas{}, nil
	}
	if !g.HasLegalPlacement(me) {
		return AnalysisResult{}, reasonBalanced, deltas{}, ErrNoMove
	}

	if observe != nil {
		s.onDepth = func(res *rootResult, elapsed time.Duration) {
			// The searcher only records its elapsed time when it stops, so the
			// boundary's own figure is the one that means anything here.
			st := s.stats()
			st.Elapsed = elapsed
			observe(AnalysisProgress{
				Recommended: res.best,
				Candidates:  candidatesOf(res),
				Stats:       st,
			})
		}
		// The observer belongs to this request: a later search on the same
		// searcher must not reach a caller that has already been answered.
		defer func() { s.onDepth = nil }()
	}

	res, err := s.root(ctx, g)
	if err != nil {
		return AnalysisResult{}, reasonBalanced, deltas{}, err
	}

	next := g.Clone()
	// The result of the move is read back off the game the move produces. It is
	// the only thing that may put a finished game in the explanation: the
	// evaluation answers a question about walks on the board and cannot end a
	// game.
	outcome, err := next.PlayPeg(res.best)
	if err != nil {
		return AnalysisResult{}, reasonBalanced, deltas{}, fmt.Errorf("bot: recommended move %v is not playable: %w", res.best, err)
	}
	// The decomposition after the move has to be read the same way the search
	// read the one before it, or the difference would be between two
	// evaluations rather than the move's doing.
	var after analysis
	after.templates = s.p.templates
	after.load(next)

	d := deltas{
		Before:     res.terms,
		After:      after.terms(me),
		Threatened: res.threatened,
		Defences:   res.defences,
		Close:      len(res.moves) > 1 && res.moves[0].exact && res.moves[1].exact && res.moves[0].score-res.moves[1].score < distWeight,
		Won:        outcome.Winner() == me,
		OwnPegs:    g.PegCount(me),
	}
	if partner, carriers, gap, ok := findSetup(&after, me, res.best); ok {
		d.Partner, d.Carriers, d.Gap, d.HasSetup = partner, carriers, gap, true
	}

	r := chooseReason(d)
	if err := verifyReason(r, d); err != nil {
		// The priority order and the templates have drifted apart. Say the one
		// thing that is true of every position rather than a specific claim the
		// numbers do not support.
		r = reasonBalanced
	}
	headline, detail := describe(r, d, me, res.best)

	// Copies, because the root list, the loaded analyses and the per-ply
	// buffers are scratch the next search overwrites.
	move, afterTerms, stats := res.best, d.After, s.stats()
	return AnalysisResult{
		Result:      result,
		Policy:      PlacementOnlyPolicy(),
		Before:      d.Before,
		After:       &afterTerms,
		Recommended: &move,
		Candidates:  candidatesOf(&res),
		Reason:      r.String(),
		Headline:    headline,
		Detail:      detail,
		Highlight:   highlightFor(r, d, res.an, &after, me, res.best),
		Stats:       &stats,
	}, r, d, nil
}

// candidatesOf copies a search's root list into candidates the caller owns,
// labelling each score with what the search established about it.
func candidatesOf(res *rootResult) []AnalysisCandidate {
	// Before any iteration finished the list is the cheap ordering pass, whose
	// scores are zeroes nothing measured. A win taken without searching is a
	// measurement all the same.
	scored := res.depth > 0 || res.immediate
	out := make([]AnalysisCandidate, 0, len(res.moves))
	for _, m := range res.moves {
		c := AnalysisCandidate{Move: m.at, Bound: BoundUnscored}
		switch {
		case !scored, m.score == -infScore && !m.exact:
			// Nothing scored it: no finished iteration, or a root move the
			// game refused to play.
		case m.exact:
			c.Score, c.Bound = m.score, BoundExact
		default:
			c.Score, c.Bound = m.score, BoundUpper
		}
		out = append(out, c)
	}
	return out
}

// hint projects an analysis onto the Hint surface.
func (a AnalysisResult) hint() Hint {
	h := Hint{Headline: a.Headline, Detail: a.Detail, Highlight: a.Highlight, Policy: a.Policy}
	if a.Recommended != nil {
		h.Move = *a.Recommended
	}
	return h
}
