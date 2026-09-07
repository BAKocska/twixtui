package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/leaderboard"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/ui"
)

// openBoard opens the result log for the resolved configuration directory.
func (o *options) openBoard() (*leaderboard.Board, error) {
	dir, err := o.configPath()
	if err != nil {
		return nil, err
	}
	return leaderboard.Open(dir)
}

func newLeaderboardCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "leaderboard",
		Aliases: []string{"board"},
		Short:   "See who is winning",
		Long: `See who is winning.

Every finished game is recorded once and ratings are replayed from that log, so
the log is the only stored fact and changing how ratings are worked out cannot
leave stale numbers behind. Only people are ranked. The bots are listed under
them because each tier plays at a rating fixed by the program, which it can
neither win nor lose: those fixed ratings are what make beating the pro count
for more than beating the beginner.`,
	}

	var limit int
	var player string
	show := &cobra.Command{
		Use:   "show",
		Short: "Show the standings, or one player's recent games",
		Long: `Show the standings, or one player's recent games.

--player takes anybody the log holds games for, which is not the same set as
the profiles on this machine: a deleted profile's games are still there, and so
are the games of everyone met over the network.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			board, err := opts.openBoard()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if player != "" {
				store, err := opts.openProfiles()
				if err != nil {
					return err
				}
				who, err := resolveHistoryName(historyParticipants(store, board), player)
				if err != nil {
					return err
				}
				history := board.History(who.stored, limit)
				if len(history) == 0 {
					_, err := fmt.Fprintf(out, "%s has no recorded games yet\n", who.display)
					return err
				}
				rows := make([][]string, 0, len(history)+1)
				rows = append(rows, []string{"WHEN", "OPPONENT", "SIDE", "RESULT", "MOVES"})
				for _, r := range history {
					// Stored in UTC, which is what keeps two machines' logs
					// comparable, but read here by one person on one machine:
					// shown in the same local time as the saved-game picker
					// and "game list". Rendering the stored value as it stands
					// dates every game an offset away from when the player
					// remembers playing it, with nothing on the row to say so.
					rows = append(rows, []string{
						r.Played.Local().Format("2006-01-02 15:04"),
						leaderboard.DisplayName(r.Opponent),
						r.Side,
						string(r.Outcome),
						strconv.Itoa(r.Moves),
					})
				}
				return ui.WriteTable(out, rows, 2)
			}
			standings := board.Standings()
			players, bots := standings.Players, standings.Bots
			if len(players) == 0 && len(bots) == 0 {
				_, err := fmt.Fprintln(out, "no games recorded yet; play one with: twixtui play bot")
				return err
			}
			// A position needs somebody to hold it against, so a board with one
			// player does not claim they are first. It is the whole board that
			// decides that, not the part --limit prints: the top of a longer
			// ranking is still a ranking.
			ranked := len(players) > 1
			// The limit is the top of the ranking. The bots below it are one
			// row per tier played, a fixed reference rather than a list that
			// grows, so there is nothing there worth cutting off.
			if limit > 0 && len(players) > limit {
				players = players[:limit]
			}

			rows := make([][]string, 0, len(players)+1)
			rows = append(rows, standingHead("PLAYER", ranked))
			for i, s := range players {
				row := standingRow(s)
				if ranked {
					row = append([]string{strconv.Itoa(i + 1)}, row...)
				}
				rows = append(rows, row)
			}
			if err := ui.WriteTable(out, rows, 2); err != nil {
				return err
			}
			if len(bots) == 0 {
				return nil
			}
			// The bots are a second table rather than more rows of the first:
			// their column is a list of tiers, and it is measured on its own so
			// that a fixed anchor never lines up under an earned rating as
			// though the two were the same kind of number.
			if _, err := fmt.Fprint(out, "\nBots are not ranked: a tier's rating is fixed, not earned.\n\n"); err != nil {
				return err
			}
			rows = make([][]string, 0, len(bots)+1)
			rows = append(rows, standingHead("BOT", false))
			for _, s := range bots {
				rows = append(rows, standingRow(s))
			}
			return ui.WriteTable(out, rows, 2)
		},
	}
	show.Flags().IntVar(&limit, "limit", 0, "show at most this many players (0 means all)")
	show.Flags().StringVar(&player, "player", "", "show this player's recent games instead of the standings")
	registerFlagCompletion(show, "player", opts.historyCompletions)

	var confirm bool
	reset := &cobra.Command{
		Use:   "reset",
		Short: "Delete the result log the ratings come from",
		Long: `Delete the result log the ratings come from.

This throws away every recorded result, which is where ratings come from, so it
cannot be undone. Saved games are kept: they are files of their own, and this
deletes the record of games that finished rather than the games themselves. It
needs --yes so that a mistyped command cannot do it.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			board, err := opts.openBoard()
			if err != nil {
				return err
			}
			if !confirm {
				return fmt.Errorf("this deletes every recorded result, and the ratings with them, and cannot be undone; pass --yes to do it")
			}
			if err := board.Reset(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "the leaderboard is empty")
			return err
		},
	}
	reset.Flags().BoolVar(&confirm, "yes", false, "confirm that the whole result log should be deleted")

	cmd.AddCommand(show, reset)
	return cmd
}

// standingHead is the heading row of a standings table, under the name the
// first column holds.
func standingHead(first string, ranked bool) []string {
	head := []string{first, "RATING", "PLAYED", "WON", "LOST", "DREW", "SCORE"}
	if ranked {
		return append([]string{"#"}, head...)
	}
	return head
}

// standingRow is one participant's line: the name a reader knows them by, and
// the numbers behind it.
func standingRow(s leaderboard.Standing) []string {
	return []string{
		leaderboard.DisplayName(s.Name),
		strconv.Itoa(s.Rating),
		strconv.Itoa(s.Played),
		strconv.Itoa(s.Won),
		strconv.Itoa(s.Lost),
		strconv.Itoa(s.Drawn),
		fmt.Sprintf("%.0f%%", s.WinRate*100),
	}
}

// historyParticipant is somebody the result log can be asked about.
type historyParticipant struct {
	// stored is the name the log records, encoding prefix and all, which is
	// what the log has to be asked by.
	stored string
	// display is what that participant is called on screen.
	display string
	// live says a profile of this name still exists on this machine.
	live bool
	// lastUsed is when the profile last played, for a live one.
	lastUsed time.Time
	// games is how many results the log holds, for a participant with no
	// profile to date them by.
	games int
}

// historyParticipants lists everyone whose games can be asked for: the profiles
// on this machine first, then the names only the log still holds.
//
// The log is the history and a profile is only a name to play under today.
// Deleting a profile deliberately does not delete its games, and an opponent
// met over the network never had a profile here at all, so resolving --player
// against live profiles alone made exactly those histories unreachable — the
// ones somebody is most likely to be looking a name up for.
//
// Bots are left out. Standings keeps them apart from the people for the same
// reason: a tier's rating is a program constant rather than a record of play,
// and every tier that has been played is already printed under the standings.
func historyParticipants(store *profile.Store, board *leaderboard.Board) []historyParticipant {
	profiles := store.List()
	out := make([]historyParticipant, 0, len(profiles)+4)
	for _, p := range profiles {
		out = append(out, historyParticipant{
			stored:   p.Name,
			display:  p.Name,
			live:     true,
			lastUsed: p.LastUsed,
		})
	}
	for _, s := range board.Standings().Players {
		if _, ok := store.Get(s.Name); ok {
			// Already listed, under the spelling the profile carries.
			continue
		}
		out = append(out, historyParticipant{
			stored:  s.Name,
			display: leaderboard.DisplayName(s.Name),
			games:   s.Played,
		})
	}
	return out
}

// describe is what a shell shows beside a completed --player value, so that a
// name nobody recognises can be told from one that is merely spelled
// differently, and a networked opponent from the local player they share a
// name with.
func (h historyParticipant) describe() string {
	switch {
	case h.live:
		return h.display + ", " + lastPlayed(h.lastUsed)
	case strings.HasPrefix(h.stored, leaderboard.RemotePrefix):
		return h.display + ", " + recordedGames(h.games)
	default:
		return h.display + ", no longer a profile, " + recordedGames(h.games)
	}
}

func recordedGames(n int) string {
	if n == 1 {
		return "1 recorded game"
	}
	return strconv.Itoa(n) + " recorded games"
}

// resolveHistoryName turns what somebody typed into exactly one participant.
//
// An exact identity is taken first and in one order — the name as the log
// stores it, then the name as it is shown, then a networked player's bare name
// — so that a local profile and a networked opponent who go by the same name
// never make each other unreachable, and neither is silently answered with the
// other's games. Only then is the loose search asked, which is the same matcher
// profile names are found by rather than a second one that would find a
// different profile from the same typo.
func resolveHistoryName(participants []historyParticipant, query string) (historyParticipant, error) {
	if strings.TrimSpace(query) == "" {
		return historyParticipant{}, fmt.Errorf("no player name given; run twixtui leaderboard show to see who has played")
	}
	if h, ok := exactParticipant(participants, query); ok {
		return h, nil
	}
	matches := profile.SearchProfiles(participantProfiles(participants), query)
	if len(matches) == 1 {
		if h, ok := participantNamed(participants, matches[0].Profile.Name); ok {
			return h, nil
		}
	}
	if len(matches) == 0 {
		return historyParticipant{}, fmt.Errorf("no recorded player matches %q; run twixtui leaderboard show to see who has played", query)
	}
	shown := make([]string, 0, len(matches))
	for _, m := range matches {
		shown = append(shown, m.Profile.Name)
	}
	return historyParticipant{}, fmt.Errorf("%q matches several recorded players: %s", query, strings.Join(shown, ", "))
}

// exactParticipant finds the one participant a name identifies outright.
func exactParticipant(participants []historyParticipant, query string) (historyParticipant, bool) {
	i, ok := exactParticipantIndex(participants, query)
	if !ok {
		return historyParticipant{}, false
	}
	return participants[i], true
}

// exactParticipantIndex is exactParticipant as a position in the list, which
// is what asking whether a name identifies one particular participant — rather
// than merely one of them — needs.
//
// It tries the three names a participant answers to in order of how exactly
// they say which participant is meant. A name that identifies two of them at
// one level identifies neither, and is left to the loose search to report.
func exactParticipantIndex(participants []historyParticipant, query string) (int, bool) {
	key := foldName(query)
	for _, nameOf := range []func(historyParticipant) string{
		func(h historyParticipant) string { return h.stored },
		func(h historyParticipant) string { return h.display },
		func(h historyParticipant) string { return leaderboard.BareName(h.stored) },
	} {
		found, hits := 0, 0
		for i, h := range participants {
			if foldName(nameOf(h)) == key {
				found, hits = i, hits+1
			}
		}
		if hits == 1 {
			return found, true
		}
	}
	return 0, false
}

// participantSelector is the value to offer for one participant: the name they
// are shown under, where that name identifies them, and the name the log
// stores them under where it does not.
//
// Two participants can be shown under one name — a local profile called
// "Reka (remote)" and a networked opponent called Reka are both shown that way
// — and completing to it would name neither of them. Leaving both out instead,
// which is what the collision used to do, loses exactly the two histories
// somebody typing that name is looking for. A stored name is the identity
// itself and answers for nobody else, so it is what a shell offers where the
// shown name cannot say which player is meant.
//
// Each candidate is put back through the resolver rather than judged here, so
// that what is offered is what typing it would find: a value that would
// resolve to somebody else is never offered, and the resolver's own order —
// stored name first, which is what keeps a local profile named literally what
// a networked opponent is shown as — is the order that decides.
func participantSelector(participants []historyParticipant, i int) (string, bool) {
	for _, candidate := range []string{participants[i].display, participants[i].stored} {
		if at, ok := exactParticipantIndex(participants, candidate); ok && at == i {
			return candidate, true
		}
	}
	return "", false
}

// participantProfiles presents the participants to the profile matcher, under
// the names they are shown by: a query is typed against what was on screen, and
// the stored encoding is not something anybody reads.
func participantProfiles(participants []historyParticipant) []profile.Profile {
	out := make([]profile.Profile, 0, len(participants))
	for _, h := range participants {
		out = append(out, profile.Profile{Name: h.display, LastUsed: h.lastUsed})
	}
	return out
}

// participantNamed finds the participant shown under a display name, and
// reports nothing if two are, so that a coincidence of names is answered as the
// ambiguity it is rather than by picking one.
func participantNamed(participants []historyParticipant, display string) (historyParticipant, bool) {
	var found historyParticipant
	hits := 0
	for _, h := range participants {
		if h.display == display {
			found, hits = h, hits+1
		}
	}
	return found, hits == 1
}

// foldName is the identity of a participant name: lower-cased with runs of
// whitespace collapsed. It is the rule the profile store's duplicate detection
// and the result log's participant keys both use, stated here because comparing
// a name from one against a name from the other happens on this side and
// neither package's own rule is reachable from outside it.
func foldName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// historyCompletions completes a --player value with everybody the log can be
// asked about, live profile or not, so that a history that outlived its profile
// can still be found by pressing TAB.
//
// What is offered for each of them is a value that resolves back to that one
// participant; see participantSelector for the names that share a spelling and
// what is offered for those.
func (o *options) historyCompletions(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	store, err := o.openProfiles()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	board, err := o.openBoard()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	participants := historyParticipants(store, board)
	matches := profile.SearchProfiles(participantProfiles(participants), toComplete)
	out := make([]cobra.Completion, 0, len(matches))
	shown := make(map[string]bool, len(matches))
	for _, m := range matches {
		// The matcher is given the names participants are shown under, so a
		// name two of them share comes back once for each. Everybody shown
		// under it is offered the first time it is seen, and the second match
		// on the same name adds nobody again.
		if shown[m.Profile.Name] {
			continue
		}
		shown[m.Profile.Name] = true
		for i, h := range participants {
			if h.display != m.Profile.Name {
				continue
			}
			value, ok := participantSelector(participants, i)
			if !ok {
				continue
			}
			out = append(out, cobra.CompletionWithDesc(value, h.describe()))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
