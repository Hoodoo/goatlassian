// Package cli implements the goatlassian command line.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/goatlassian/internal/config"
	"github.com/Hoodoo/goatlassian/internal/portfolio"
	"github.com/Hoodoo/goatlassian/internal/sources"
	"github.com/Hoodoo/goatlassian/internal/store"
	"github.com/Hoodoo/goatlassian/internal/version"
)

type app struct {
	homeFlag string
	json     bool
	out      io.Writer

	home string
	cfg  config.Config
}

// Execute runs the command line and returns the exit code.
func Execute() int {
	a := &app{out: os.Stdout}
	if err := a.root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "goatlassian:", err)
		return 1
	}
	return 0
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "goatlassian",
		Short: "Group repos, kata trackers, owcli wikis, and agent sessions into projects",
		Long: `goatlassian is a thin project layer over the tools of agent-driven
development. A project groups components that other tools own: a git
repository, kata issue trackers, owcli wikis and workspaces, the coding-agent
sessions bossman indexes, and links to anything else (PRs, Slack threads,
mail). goatlassian stores only the grouping, a lifecycle state, notes, and
metric snapshots in its own database; it reads the tools through their
command lines and links to their web UIs. It never writes into a repository.

Start with:
  goatlassian discover          what the tools already know, by repository
  goatlassian adopt <dir>       make a project from a repository
  goatlassian status            the portfolio: activity, attention, cost
  goatlassian serve             the same in a local web UI`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			var err error
			if a.home, err = config.Home(a.homeFlag); err != nil {
				return err
			}
			a.cfg, err = config.Load(a.home)
			return err
		},
	}
	root.PersistentFlags().StringVar(&a.homeFlag, "home", "", "data directory (default $GOATLASSIAN_HOME or ~/.local/share/goatlassian)")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print JSON")
	root.AddGroup(
		&cobra.Group{ID: "report", Title: "Reporting:"},
		&cobra.Group{ID: "edit", Title: "Projects and components:"},
		&cobra.Group{ID: "env", Title: "Environment:"},
	)
	for _, c := range []*cobra.Command{a.statusCmd(), a.showCmd(), a.historyCmd(), a.snapshotCmd(), a.discoverCmd()} {
		c.GroupID = "report"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{a.adoptCmd(), a.projectCmd(), a.attachCmd(), a.detachCmd(), a.relocateCmd(), a.noteCmd(), a.logCmd()} {
		c.GroupID = "edit"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{a.serveCmd(), a.servicesCmd(), a.pathsCmd(), a.kindsCmd()} {
		c.GroupID = "env"
		root.AddCommand(c)
	}
	return root
}

// withStore wraps a command body that needs an open store.
func (a *app) withStore(fn func(st *store.Store, args []string) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		st, err := store.Open(a.home)
		if err != nil {
			return err
		}
		defer st.Close()
		return fn(st, args)
	}
}

func (a *app) collect() *sources.World {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return sources.Collect(ctx, a.cfg, sources.Exec{})
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// toolWarnings prints the tools that could not be read.
func (a *app) toolWarnings(w *sources.World) {
	for name, p := range map[string]string{"kata": w.Kata.Problem, "owcli": w.Owcli.Problem, "bossman": w.Bossman.Problem} {
		if p != "" {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", name, p)
		}
	}
}

func ago(t time.Time) string { return portfolio.Ago(time.Now(), t) }

func money(v float64) string {
	if v == 0 {
		return "-"
	}
	if v >= 100 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func readText(args []string) (string, error) {
	if len(args) == 1 && args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		return strings.TrimSpace(string(b)), err
	}
	return strings.TrimSpace(strings.Join(args, " ")), nil
}

func (a *app) pathsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "paths",
		Short: "Show where goatlassian reads and writes",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p := map[string]string{
				"home":     a.home,
				"database": a.home + "/goatlassian.db",
				"config":   a.home + "/config.toml",
				"logs":     a.home + "/logs",
			}
			if a.json {
				return a.printJSON(map[string]any{"paths": p, "config": a.cfg})
			}
			for _, k := range []string{"home", "database", "config", "logs"} {
				fmt.Fprintf(a.out, "%-9s %s\n", k, p[k])
			}
			return nil
		},
	}
}

func (a *app) kindsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kinds",
		Short: "List the component kinds goatlassian has adapters for",
		Long: `Component kinds and what their ref is. Any other kind can be attached too
(for example "slack" with a permalink, or "pr" with a URL); goatlassian
shows it and links URLs but reports nothing more until it has an adapter.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			kinds := [][2]string{
				{store.KindGit, "repository path (resolved to its top level)"},
				{store.KindKata, "kata project name (pinned by UID)"},
				{store.KindOwcliWiki, "owcli wiki ID or name (owcli wikis)"},
				{store.KindOwcliWorkspace, "owcli workspace ID or name"},
				{store.KindSessions, "directory; agent sessions started in or under it (--attr agent=claude|codex narrows)"},
				{store.KindSession, "one bossman session key (claude:<id>, codex:<id>); pins it here, overriding directories"},
				{store.KindLink, "URL of anything else: a PR, a Slack thread, a doc"},
			}
			if a.json {
				out := []map[string]string{}
				for _, k := range kinds {
					out = append(out, map[string]string{"kind": k[0], "ref": k[1]})
				}
				return a.printJSON(out)
			}
			for _, k := range kinds {
				fmt.Fprintf(a.out, "%-16s %s\n", k[0], k[1])
			}
			return nil
		},
	}
}
