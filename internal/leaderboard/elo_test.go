package leaderboard

import (
	"math"
	"testing"
)

// ratingAfter records the given results on a fresh board and returns the
// player's rating. It goes through recordBatch, so the settling runs below cost
// one write rather than one per game.
func ratingAfter(t *testing.T, player string, results ...Result) int {
	t.Helper()
	return standing(t, recordBatch(t, results...), player).Rating
}

func TestWinAgainstStrongerOpponentGainsMore(t *testing.T) {
	overBeginner := ratingAfter(t, "Balint", result("Balint", BotName("beginner"), Win, day(1)))
	overIntermediate := ratingAfter(t, "Balint", result("Balint", BotName("intermediate"), Win, day(1)))
	overPro := ratingAfter(t, "Balint", result("Balint", BotName("pro"), Win, day(1)))

	if !(overPro > overIntermediate && overIntermediate > overBeginner) {
		t.Fatalf("one win each: beginner %d, intermediate %d, pro %d; want strictly increasing",
			overBeginner, overIntermediate, overPro)
	}
	if gained := overBeginner - StartRating; gained <= 0 {
		t.Fatalf("beating the beginner bot gained %d, want a positive amount", gained)
	}
	// The whole point of the tier anchors: the reward has to differ by enough
	// for a player to notice, not by a point or two.
	proGain := overPro - StartRating
	beginnerGain := overBeginner - StartRating
	if proGain < 3*beginnerGain {
		t.Fatalf("pro win gained %d and beginner win gained %d; want the pro win worth several times more",
			proGain, beginnerGain)
	}
}

func TestLossAgainstWeakerOpponentCostsMore(t *testing.T) {
	toBeginner := ratingAfter(t, "Balint", result("Balint", BotName("beginner"), Loss, day(1)))
	toPro := ratingAfter(t, "Balint", result("Balint", BotName("pro"), Loss, day(1)))

	if toBeginner >= toPro {
		t.Fatalf("losing to the beginner left %d and losing to the pro left %d; want the beginner loss to cost more",
			toBeginner, toPro)
	}
	if toPro >= StartRating {
		t.Fatalf("losing to the pro bot left the rating at %d, want it below the seed %d", toPro, StartRating)
	}
}

func TestDrawBetweenEqualsIsNeutral(t *testing.T) {
	b := openBoard(t, t.TempDir())
	if err := b.Record(result("Ann", "Bob", DrawOutcome, day(1))); err != nil {
		t.Fatalf("Record: %v", err)
	}
	for _, name := range []string{"Ann", "Bob"} {
		if got := standing(t, b, name).Rating; got != StartRating {
			t.Fatalf("%s rating = %d after a draw between equals, want exactly %d", name, got, StartRating)
		}
	}
}

func TestDrawIsWorthHalfAGame(t *testing.T) {
	// Against a stronger opponent a draw is a good result, so it must gain
	// rating: exactly half of what the win would have gained, since the score
	// is half and the expectation is unchanged.
	drawn := ratingAfter(t, "Balint", result("Balint", BotName("pro"), DrawOutcome, day(1)))
	won := ratingAfter(t, "Balint", result("Balint", BotName("pro"), Win, day(1)))
	if drawn <= StartRating {
		t.Fatalf("drawing with the pro bot left the rating at %d, want a gain", drawn)
	}
	halfOfWin := float64(won-StartRating) / 2
	if diff := math.Abs(float64(drawn-StartRating) - halfOfWin); diff > 1 {
		t.Fatalf("draw gained %d, want about half of the win's %d", drawn-StartRating, won-StartRating)
	}
	if drawn := ratingAfter(t, "Balint", result("Balint", BotName("beginner"), DrawOutcome, day(1))); drawn >= StartRating {
		t.Fatalf("drawing with the beginner bot left the rating at %d, want a loss of rating", drawn)
	}
}

// recordBatch writes several results in one atomic write, taking the same
// validation and defaulting door Record does. Record writes one result at a
// time and pays a fsync for each, which is the durability the store owes a
// player and has nothing to do with what a rating is worth: the tests in this
// file are about the Elo replay, and TestRecordRoundTrips and the repeated-open
// cycles cover the write path they are skipping. What lands in the file is the
// same either way.
func recordBatch(t *testing.T, results ...Result) *Board {
	t.Helper()
	b := openBoard(t, t.TempDir())
	rows := make([]Result, 0, len(results))
	for _, r := range results {
		if err := normalise(&r); err != nil {
			t.Fatalf("invalid result %+v: %v", r, err)
		}
		rows = append(rows, r)
	}
	err := b.mutate(func(rs *[]Result) error {
		*rs = append(*rs, rows...)
		return nil
	})
	if err != nil {
		t.Fatalf("writing %d results: %v", len(rows), err)
	}
	return b
}

func TestEstablishedRatingMovesLessThanProvisional(t *testing.T) {
	// An even record against an equal burns through the provisional games while
	// leaving the rating near the seed, so the two upsets being compared start
	// from nearly the same place and differ mainly in the K factor.
	var settle []Result
	for i := range ProvisionalGames {
		outcome := Win
		if i%2 == 1 {
			outcome = Loss
		}
		settle = append(settle, result("Balint", "Sparring", outcome, day(i+1)))
	}
	upset := result("Balint", BotName("pro"), Win, day(ProvisionalGames+1))

	provisional := ratingAfter(t, "Balint", upset)
	established := ratingAfter(t, "Balint", append(append([]Result(nil), settle...), upset)...)

	settled := ratingAfter(t, "Balint", settle...)
	// Not exactly the seed: Elo is order-dependent, so a win and a loss at
	// different rating gaps do not cancel to the point.
	if settled < StartRating-ProvisionalK || settled > StartRating+ProvisionalK {
		t.Fatalf("even record left the rating at %d, want it within one K of the seed %d", settled, StartRating)
	}
	provisionalGain := provisional - StartRating
	establishedGain := established - settled
	if establishedGain >= provisionalGain {
		t.Fatalf("provisional gain %d, established gain %d; want the established rating to move less",
			provisionalGain, establishedGain)
	}
	ratio := float64(provisionalGain) / float64(establishedGain)
	if want := ProvisionalK / EstablishedK; math.Abs(ratio-want) > 0.25 {
		t.Fatalf("gain ratio %.2f, want about %.2f (the ratio of the K factors)", ratio, want)
	}
}

func TestRatingsStayFiniteOverHundredsOfResults(t *testing.T) {
	const games = 400
	rows := make([]Result, 0, 2*games)
	for i := range games {
		// A player who always beats the pro bot, and two humans trading wins:
		// the runaway case and the churning case at once.
		rows = append(rows, result("Climber", BotName("pro"), Win, day(i)))
		outcome := Win
		if i%3 == 0 {
			outcome = Loss
		} else if i%7 == 0 {
			outcome = DrawOutcome
		}
		rows = append(rows, result("Ann", "Bob", outcome, day(i)))
	}
	// Written in one go: this test is about the rating replay, and the write
	// path's durability is covered by the repeated-open cycles test.
	b := recordBatch(t, rows...)

	standings := allStandings(b.Standings())
	if len(standings) != 4 {
		t.Fatalf("Standings has %d rows, want 4", len(standings))
	}
	for _, s := range standings {
		if s.Rating < 0 || s.Rating > 5000 {
			t.Fatalf("%s rating = %d after %d games, want it bounded", s.Name, s.Rating, games)
		}
		if s.WinRate < 0 || s.WinRate > 1 {
			t.Fatalf("%s score rate = %v, want it within [0,1]", s.Name, s.WinRate)
		}
		if s.Played != s.Won+s.Lost+s.Drawn {
			t.Fatalf("%s played %d but has %d+%d+%d results", s.Name, s.Played, s.Won, s.Lost, s.Drawn)
		}
	}

	climber := standing(t, b, "Climber")
	if climber.Rating <= botRatings["pro"] {
		t.Fatalf("Climber rating = %d after %d wins over the pro bot, want it above the bot's %d",
			climber.Rating, games, botRatings["pro"])
	}
	// Elo self-limits: each further win against a fixed anchor is worth less,
	// so hundreds of wins must not run away to an absurd number.
	if climber.Rating > botRatings["pro"]+1200 {
		t.Fatalf("Climber rating = %d, want the gain over the anchor to flatten out", climber.Rating)
	}
	if ann, bob := standing(t, b, "Ann"), standing(t, b, "Bob"); ann.Rating+bob.Rating < 2*StartRating-1 || ann.Rating+bob.Rating > 2*StartRating+1 {
		t.Fatalf("Ann %d and Bob %d total %d, want %d conserved", ann.Rating, bob.Rating, ann.Rating+bob.Rating, 2*StartRating)
	}
}

func TestExpectedScore(t *testing.T) {
	if got := expectedScore(1200, 1200); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("expectedScore(equal) = %v, want 0.5", got)
	}
	// A 400-point lead is ten-to-one odds by construction.
	if got := expectedScore(1600, 1200); math.Abs(got-10.0/11.0) > 1e-9 {
		t.Fatalf("expectedScore(+400) = %v, want %v", got, 10.0/11.0)
	}
	if a, b := expectedScore(1600, 1200), expectedScore(1200, 1600); math.Abs(a+b-1) > 1e-9 {
		t.Fatalf("expectations %v and %v do not sum to 1", a, b)
	}
}

func TestBotAnchorsAreOrdered(t *testing.T) {
	if !(botRatings["beginner"] < botRatings["intermediate"] && botRatings["intermediate"] < botRatings["pro"]) {
		t.Fatalf("bot anchors %v are not ordered by tier strength", botRatings)
	}
	if botRatings["beginner"] >= StartRating {
		t.Fatalf("beginner anchor %d is not below the seed %d: a new player is meant to beat it",
			botRatings["beginner"], StartRating)
	}
	if botRatings["pro"] <= StartRating {
		t.Fatalf("pro anchor %d is not above the seed %d", botRatings["pro"], StartRating)
	}
	for tier, rating := range botRatings {
		got, fixed := anchorRating(BotName(tier))
		if !fixed || got != rating {
			t.Fatalf("anchorRating(%q) = %d, %v; want %d, true", BotName(tier), got, fixed, rating)
		}
	}
	if _, fixed := anchorRating("Balint"); fixed {
		t.Fatal("anchorRating treated a profile name as a fixed anchor")
	}
	if _, fixed := anchorRating(RemoteName("kata")); fixed {
		t.Fatal("anchorRating treated a remote opponent as a fixed anchor")
	}
}

// TestDisplayNameIsTheOneSpelling pins what each kind of participant is called
// on screen. The exact words matter less than there being one set of them, but
// a test that asks DisplayName what DisplayName returns cannot fail, so they are
// written out here and the surfaces are checked against this function.
func TestDisplayNameIsTheOneSpelling(t *testing.T) {
	for stored, want := range map[string]string{
		BotName("beginner"):     "beginner bot",
		BotName("intermediate"): "intermediate bot",
		BotName("pro"):          "pro bot",
		BotName("grandmaster"):  "grandmaster bot",
		RemoteName("kata"):      "kata (remote)",
		"Balint":                "Balint",
		// A prefix with no name after it has nothing to show but itself.
		BotPrefix:    BotPrefix,
		RemotePrefix: RemotePrefix,
	} {
		if got := DisplayName(stored); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", stored, got, want)
		}
	}
}

// TestBareNameStaysTheEncodingAccessor: the stored name is parsed back into a
// tier and re-recorded, so stripping the prefix has to keep giving the bare
// string however the on-screen spelling changes.
func TestBareNameStaysTheEncodingAccessor(t *testing.T) {
	for stored, want := range map[string]string{
		BotName("pro"):     "pro",
		RemoteName("kata"): "kata",
		"Balint":           "Balint",
	} {
		if got := BareName(stored); got != want {
			t.Errorf("BareName(%q) = %q, want %q", stored, got, want)
		}
	}
}

func TestKFactor(t *testing.T) {
	if got := kFactor(0); got != ProvisionalK {
		t.Fatalf("kFactor(0) = %v, want %v", got, ProvisionalK)
	}
	if got := kFactor(ProvisionalGames - 1); got != ProvisionalK {
		t.Fatalf("kFactor(%d) = %v, want %v", ProvisionalGames-1, got, ProvisionalK)
	}
	if got := kFactor(ProvisionalGames); got != EstablishedK {
		t.Fatalf("kFactor(%d) = %v, want %v", ProvisionalGames, got, EstablishedK)
	}
}
