package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/humantime"
	"github.com/BAKocska/twixtui/internal/profile"
	"github.com/BAKocska/twixtui/internal/ui"
)

// openProfiles opens the profile store for the resolved configuration directory.
func (o *options) openProfiles() (*profile.Store, error) {
	dir, err := o.configPath()
	if err != nil {
		return nil, err
	}
	return profile.Open(dir)
}

// profileCompletions completes a profile name, so a player who half-remembers
// their name can press TAB instead of guessing.
func (o *options) profileCompletions(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	store, err := o.openProfiles()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	current, _ := o.currentProfile(store)
	matches := store.Search(toComplete)
	out := make([]cobra.Completion, 0, len(matches))
	for _, m := range matches {
		out = append(out, cobra.CompletionWithDesc(m.Profile.Name, describeProfile(m.Profile, current)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// describeProfile is what a shell shows beside a completed name.
//
// It names the profile as well as saying when it last played, because when it
// last played is not always a distinguishing fact: two profiles that have
// never played, or that were both used by the same script, carry the same
// timestamp, and a shell that groups its candidates by their description then
// offers one row where there are two players. The name is what tells them
// apart in every case.
func describeProfile(p profile.Profile, current string) string {
	parts := make([]string, 0, 3)
	parts = append(parts, p.Name)
	if current != "" && p.Name == current {
		parts = append(parts, "current")
	}
	return strings.Join(append(parts, lastPlayed(p.LastUsed)), ", ")
}

// lastPlayed says when somebody last played, in the words a completion
// description has room for. It takes the timestamp rather than the profile
// because the result log names players who have no profile to read it from.
func lastPlayed(when time.Time) string {
	if when.IsZero() {
		return "never played"
	}
	return "last played " + humantime.Since(when)
}

func newProfileCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage the player names on this machine",
		Long: `Manage the player names on this machine.

A profile is just a name: there are no passwords, because the leaderboard only
has to tell local players apart. Names are searched loosely, so "profile use"
and TAB completion will still find you if you misremember the spelling.`,
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the profiles, most recently played first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			profiles := store.List()
			if len(profiles) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(),
					"no profiles yet; create one with: twixtui profile create <name>")
				return err
			}
			current, _ := opts.currentProfile(store)
			rows := make([][]string, 0, len(profiles)+1)
			rows = append(rows, []string{"", "NAME", "CREATED", "LAST PLAYED"})
			for _, p := range profiles {
				marker := ""
				if p.Name == current {
					marker = "*"
				}
				rows = append(rows, []string{marker, p.Name,
					p.Created.Format("2006-01-02"), lastPlayedColumn(p)})
			}
			return ui.WriteTable(cmd.OutOrStdout(), rows, 2)
		},
	}

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a profile",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			p, err := store.Create(args[0])
			if err != nil {
				return err
			}
			if _, err := store.UseCurrent(p.Name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "created %s and made it current\n", p.Name)
			return err
		},
	}

	use := &cobra.Command{
		Use:   "use <name>",
		Short: "Choose the profile to play as",
		Long: `Choose the profile to play as.

The name is matched loosely, so a near miss still finds the right profile. If
several profiles match, they are listed and nothing is changed.`,
		Args:              exactArgs(1),
		ValidArgsFunction: opts.profileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			name, err := resolveProfileName(store, args[0])
			if err != nil {
				return err
			}
			if _, err := store.UseCurrent(name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "playing as %s\n", name)
			return err
		},
	}

	rename := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile",
		Long: `Rename a profile.

Games already recorded keep the old name, because the leaderboard is a log of
what happened rather than a table that can be rewritten.`,
		Args:              exactArgs(2),
		ValidArgsFunction: opts.profileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			if err := store.Rename(args[0], args[1]); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "renamed %s to %s\n", args[0], args[1])
			return err
		},
	}

	del := &cobra.Command{
		Use:               "delete <name>",
		Short:             "Delete a profile",
		Args:              exactArgs(1),
		ValidArgsFunction: opts.profileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			if err := store.Delete(args[0]); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"deleted %s; games it already played stay on the leaderboard\n", args[0])
			return err
		},
	}

	whoami := &cobra.Command{
		Use:   "whoami",
		Short: "Print the profile currently in use",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := opts.openProfiles()
			if err != nil {
				return err
			}
			name, ok := opts.currentProfile(store)
			if !ok {
				_, err := fmt.Fprintln(cmd.OutOrStdout(),
					"no profile chosen yet; choose one with: twixtui profile use <name>")
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), name)
			return err
		},
	}

	cmd.AddCommand(list, create, use, rename, del, whoami)
	return cmd
}

func lastPlayedColumn(p profile.Profile) string {
	if p.LastUsed.IsZero() {
		return "never"
	}
	return humantime.Since(p.LastUsed)
}

// resolveProfileName turns what the player typed into exactly one profile name,
// accepting an exact name, or a unique loose match.
//
// A blank selector is refused rather than searched. The loose search answers a
// blank query with every profile, which is what makes it the browsable list
// behind TAB completion, and that same answer read as a selection means "the
// only profile" on a machine with one and "ambiguous" on a machine with two:
// the same command would play as somebody on one machine and refuse on the
// next. Nothing is lost, because a caller wanting the current profile leaves
// the selector out instead of passing an empty one.
func resolveProfileName(store *profile.Store, query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("no profile name given; run twixtui profile list to see them")
	}
	if p, ok := store.Get(query); ok {
		return p.Name, nil
	}
	matches := store.Search(query)
	switch len(matches) {
	case 0:
		if len(store.List()) == 0 {
			return "", fmt.Errorf("no profiles exist yet; create one with: twixtui profile create %s", query)
		}
		return "", fmt.Errorf("no profile matches %q; run twixtui profile list to see them", query)
	case 1:
		return matches[0].Profile.Name, nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.Profile.Name)
	}
	return "", fmt.Errorf("%q matches several profiles: %s", query, strings.Join(names, ", "))
}
