package leaderboard

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/BAKocska/twixtui/internal/game"
)

func ExampleBoard_Standings() {
	dir, err := os.MkdirTemp("", "twixtui-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	board, err := Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	played := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	games := []Result{
		{Player: "Balint", Opponent: BotName("beginner"), Outcome: Win},
		{Player: "Balint", Opponent: BotName("pro"), Outcome: Loss},
		{Player: "Balint", Opponent: "Bernadett", Outcome: Win},
		{Player: "Bernadett", Opponent: BotName("intermediate"), Outcome: Win},
	}
	for i, g := range games {
		g.Played = played.Add(time.Duration(i) * time.Hour)
		g.Side = game.Vertical.String()
		g.Moves = 60
		g.Ruleset = game.Std.Canonical()
		g.Duration = 12 * time.Minute
		if err := board.Record(g); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Printf("%-14s %-6s %-7s %s\n", "player", "rating", "w/l/d", "score")
	for _, s := range board.Standings() {
		fmt.Printf("%-14s %-6d %d/%d/%d   %3.0f%%\n",
			DisplayName(s.Name), s.Rating, s.Won, s.Lost, s.Drawn, 100*s.WinRate)
	}
	// Output:
	// player         rating w/l/d   score
	// pro            1800   1/0/0   100%
	// intermediate   1400   0/1/0     0%
	// Balint         1222   2/1/0    67%
	// Bernadett      1209   1/1/0    50%
	// beginner       1000   0/1/0     0%
}
