package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BAKocska/twixtui/internal/humantime"
	"github.com/BAKocska/twixtui/internal/profile"
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
	matches := store.Search(toComplete)
	out := make([]cobra.Completion, 0, len(matches))
	for _, m := range matches {
		out = append(out, cobra.CompletionWithDesc(m.Profile.Name, lastPlayed(m.Profile)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func lastPlayed(p profile.Profile) string {
	if p.LastUsed.IsZero() {
		return "never played"
	}
	return "last played " + humantime.Since(p.LastUsed)
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
		Args:  cobra.NoArgs,
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
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "\tNAME\tCREATED\tLAST PLAYED")
			for _, p := range profiles {
				marker := ""
				if p.Name == current {
					marker = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", marker, p.Name,
					p.Created.Format("2006-01-02"), lastPlayedColumn(p))
			}
			return w.Flush()
		},
	}

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a profile",
		Args:  cobra.ExactArgs(1),
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
		Args:              cobra.ExactArgs(1),
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
		Args:              cobra.ExactArgs(2),
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
		Args:              cobra.ExactArgs(1),
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
		Args:  cobra.NoArgs,
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
func resolveProfileName(store *profile.Store, query string) (string, error) {
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
