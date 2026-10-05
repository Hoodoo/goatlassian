package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/goatlassian/internal/discover"
	"github.com/Hoodoo/goatlassian/internal/store"
)

func (a *app) projectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"projects", "p"},
		Short:   "Create, list, edit, and change the lifecycle of projects",
	}
	cmd.AddCommand(a.projectCreateCmd(), a.projectListCmd(), a.projectEditCmd(), a.projectStateCmd(), a.projectRmCmd())
	return cmd
}

func (a *app) projectCreateCmd() *cobra.Command {
	var name, desc string
	var tags []string
	cmd := &cobra.Command{
		Use:   "create <slug>",
		Short: "Create an empty project (see adopt to create one from a repository)",
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (default: the slug)")
	cmd.Flags().StringVarP(&desc, "description", "d", "", "what the project is")
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "tag (repeatable)")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		p, err := st.CreateProject(args[0], name, desc, tags)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(p)
		}
		fmt.Fprintf(a.out, "created %s\n", p.Slug)
		return nil
	})
	return cmd
}

func (a *app) projectListCmd() *cobra.Command {
	var state, tag string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List projects with their components (no tool calls; see status for metrics)",
		Args:    cobra.NoArgs,
	}
	cmd.Flags().StringVar(&state, "state", "", "only this lifecycle state")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "only projects with this tag")
	cmd.RunE = a.withStore(func(st *store.Store, _ []string) error {
		ps, err := st.Projects()
		if err != nil {
			return err
		}
		comps, err := st.Components(0)
		if err != nil {
			return err
		}
		type row struct {
			*store.Project
			Components []store.Component `json:"components"`
		}
		rows := []row{}
		for _, p := range ps {
			if (state != "" && p.State != state) || (tag != "" && !hasTag(p.Tags, tag)) {
				continue
			}
			r := row{Project: p, Components: []store.Component{}}
			for _, c := range comps {
				if c.ProjectID == p.ID {
					r.Components = append(r.Components, c)
				}
			}
			rows = append(rows, r)
		}
		if a.json {
			return a.printJSON(rows)
		}
		tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SLUG\tSTATE\tTAGS\tCOMPONENTS")
		for _, r := range rows {
			kinds := map[string]int{}
			var order []string
			for _, c := range r.Components {
				if kinds[c.Kind] == 0 {
					order = append(order, c.Kind)
				}
				kinds[c.Kind]++
			}
			var parts []string
			for _, k := range order {
				if kinds[k] > 1 {
					parts = append(parts, fmt.Sprintf("%s×%d", k, kinds[k]))
				} else {
					parts = append(parts, k)
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Slug, r.State, strings.Join(r.Tags, ","), strings.Join(parts, " "))
		}
		return tw.Flush()
	})
	return cmd
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == strings.ToLower(t) {
			return true
		}
	}
	return false
}

func (a *app) projectEditCmd() *cobra.Command {
	var slug, name, desc string
	var tags, untag []string
	cmd := &cobra.Command{
		Use:   "edit <slug>",
		Short: "Rename a project, change its description, or add and remove tags",
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&slug, "slug", "", "new slug")
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVarP(&desc, "description", "d", "", "new description")
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "add a tag (repeatable)")
	cmd.Flags().StringSliceVar(&untag, "untag", nil, "remove a tag (repeatable)")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		var e store.ProjectEdit
		if cmd.Flags().Changed("slug") {
			e.Slug = &slug
		}
		if cmd.Flags().Changed("name") {
			e.Name = &name
		}
		if cmd.Flags().Changed("description") {
			e.Description = &desc
		}
		e.AddTags, e.RemoveTags = tags, untag
		p, err := st.EditProject(args[0], e)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(p)
		}
		fmt.Fprintf(a.out, "%s: %s · %s · tags %s\n", p.Slug, p.Name, p.State, strings.Join(p.Tags, ", "))
		return nil
	})
	return cmd
}

func (a *app) projectStateCmd() *cobra.Command {
	var why string
	cmd := &cobra.Command{
		Use:   "state <slug> <" + strings.Join(store.States, "|") + ">",
		Short: "Move a project through its lifecycle, logging why",
		Long: `Lifecycle states:
  active    being worked on; flagged stale when idle past stale_days
  paused    on hold; activity on it is flagged
  done      finished; open issues on it are flagged
  archived  kept for the record; hidden from status unless --all`,
		Args: cobra.ExactArgs(2),
	}
	cmd.Flags().StringVarP(&why, "why", "m", "", "reason, kept in the project log")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		p, err := st.SetState(args[0], args[1], why)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(p)
		}
		fmt.Fprintf(a.out, "%s is %s\n", p.Slug, p.State)
		return nil
	})
	return cmd
}

func (a *app) projectRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <slug>",
		Short: "Delete a project and its log (the artifacts it points at are untouched)",
		Long: `rm deletes the project's record, components, log, and snapshots from
goatlassian's database. Repositories, kata issues, wikis, and sessions are
not touched. Prefer "project state <slug> archived" to keep the history.`,
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		if !yes {
			return errors.New("this deletes the project's log and snapshots; pass --yes, or archive it instead")
		}
		if err := st.DeleteProject(args[0]); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "deleted %s\n", args[0])
		return nil
	})
	return cmd
}

func parseAttrs(kv []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range kv {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--attr %q: want key=value", s)
		}
		out[k] = v
	}
	return out, nil
}

func (a *app) attachCmd() *cobra.Command {
	var label string
	var attrs []string
	var raw bool
	cmd := &cobra.Command{
		Use:   "attach <slug> <kind> <ref>",
		Short: "Attach a component (see kinds) to a project",
		Example: `  goatlassian attach shop git ~/src/shop
  goatlassian attach shop kata shop-tracker
  goatlassian attach shop owcli-wiki shop-1a2b3c4d5e6f
  goatlassian attach shop sessions ~/src/shop --attr agent=codex
  goatlassian attach shop link https://github.com/me/shop/pull/12 --label "Checkout PR"
  goatlassian attach shop slack https://team.slack.com/archives/C1/p2 --label "launch thread"`,
		Args: cobra.ExactArgs(3),
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "display label")
	cmd.Flags().StringArrayVar(&attrs, "attr", nil, "attribute key=value (repeatable)")
	cmd.Flags().BoolVar(&raw, "raw", false, "store the ref as given, without resolving it through the tools")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		at, err := parseAttrs(attrs)
		if err != nil {
			return err
		}
		c := store.Component{Kind: args[1], Ref: args[2], Label: label, Attrs: at}
		if !raw {
			w := a.collect()
			if c, err = discover.Normalize(context.Background(), w, c); err != nil {
				return fmt.Errorf("%w (use --raw to attach anyway)", err)
			}
		}
		got, err := st.Attach(args[0], c)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(got)
		}
		fmt.Fprintf(a.out, "attached %s %s to %s (component %d)\n", got.Kind, got.Ref, args[0], got.ID)
		return nil
	})
	return cmd
}

func (a *app) detachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <slug> (<component-id> | <kind> <ref>)",
		Short: "Detach a component from a project",
		Args:  cobra.RangeArgs(2, 3),
	}
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		var err error
		if len(args) == 2 {
			id, perr := strconv.ParseInt(args[1], 10, 64)
			if perr != nil {
				return fmt.Errorf("%q is not a component ID; give <kind> <ref> instead", args[1])
			}
			err = st.Detach(args[0], id, "", "")
		} else {
			err = st.Detach(args[0], 0, args[1], args[2])
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, "detached")
		return nil
	})
	return cmd
}

func (a *app) relocateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "relocate <old-path> <new-path>",
		Short: "Rewrite component paths after repositories moved",
		Long: `Rewrite every component path at or under old-path to the same path under
new-path, in all projects: git refs and owcli-wiki repository roots are
rewritten; a sessions directory stays attached, since past sessions ran
there, and the new directory is attached next to it. Each change goes into
the project's log.

  goatlassian relocate ~/src ~/work
  goatlassian relocate /home/me /Users/me     # a new machine

Run it before "owcli relocate": wikis attached before goatlassian recorded
their repository roots are matched by their current owcli ID, which owcli
changes when it relocates. Paths in config.toml ([bin], [services]) are not
touched. Nothing is written
with --dry-run, or when a project would end up with the same git repository
twice.`,
		Args: cobra.ExactArgs(2),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		// Wikis attached before goatlassian recorded their roots are found
		// by their current owcli ID.
		roots := map[string]string{}
		if w := a.collect(); w.Owcli.Problem == "" {
			for _, wk := range w.Owcli.Wikis {
				roots[wk.ID] = wk.RepoRoot
			}
		}
		changes, err := st.Relocate(args[0], args[1], roots, dryRun)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"dry_run": dryRun, "changes": changes})
		}
		for _, c := range changes {
			fmt.Fprintf(a.out, "%-14s %-12s %-10s %s -> %s\n", c.Project, c.Kind, c.Field, c.From, c.To)
		}
		switch {
		case len(changes) == 0:
			fmt.Fprintf(a.out, "no component under %s\n", args[0])
		case dryRun:
			fmt.Fprintf(a.out, "dry run: %d change(s)\n", len(changes))
		default:
			fmt.Fprintf(a.out, "relocated: %d change(s)\n", len(changes))
		}
		return nil
	})
	return cmd
}

func (a *app) noteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note <slug> <text...|->",
		Short: "Add a note to a project's log (\"-\" reads stdin)",
		Args:  cobra.MinimumNArgs(2),
	}
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		text, err := readText(args[1:])
		if err != nil {
			return err
		}
		return st.AddNote(args[0], text)
	})
	return cmd
}

func (a *app) logCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log <slug>",
		Short: "Show a project's log: lifecycle changes, notes, attach and detach",
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "at most this many entries (0 for all)")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		p, err := st.Project(args[0])
		if err != nil {
			return err
		}
		evs, err := st.Events(p.ID, limit)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(evs)
		}
		for _, e := range evs {
			fmt.Fprintf(a.out, "%s  %-8s %-8s %s\n", e.At.Local().Format("2006-01-02 15:04"), e.Kind, e.Actor, e.Message)
		}
		return nil
	})
	return cmd
}

func (a *app) adoptCmd() *cobra.Command {
	var slug, name, desc string
	var tags []string
	var dry bool
	cmd := &cobra.Command{
		Use:   "adopt [dir]",
		Short: "Create a project from a repository and everything the tools know about it",
		Long: `adopt resolves the repository containing dir (default: the current
directory) and attaches its git repository, every kata project bound inside
it, every owcli wiki rooted in it, and its agent sessions. With --slug naming
an existing project, the components are added to that project instead.`,
		Args: cobra.MaximumNArgs(1),
	}
	cmd.Flags().StringVar(&slug, "slug", "", "project slug (default: the directory name)")
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVarP(&desc, "description", "d", "", "what the project is")
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "tag (repeatable)")
	cmd.Flags().BoolVarP(&dry, "dry-run", "n", false, "show what would be attached")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		w := a.collect()
		a.toolWarnings(w)
		c, err := discover.ForDir(context.Background(), w, dir)
		if err != nil {
			return err
		}
		if slug != "" {
			c.Slug = slug
		}
		if dry {
			if a.json {
				return a.printJSON(c)
			}
			a.printCandidate(c)
			return nil
		}
		p, err := discover.Adopt(st, c, c.Slug, name, desc, tags)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(p)
		}
		fmt.Fprintf(a.out, "%s: %d components from %s\n", p.Slug, len(c.Components), c.Root)
		for _, comp := range c.Components {
			fmt.Fprintf(a.out, "  %-16s %s\n", comp.Kind, comp.Ref)
		}
		return nil
	})
	return cmd
}

func (a *app) printCandidate(c *discover.Candidate) {
	fmt.Fprintf(a.out, "%s  (%s)\n", c.Root, c.Slug)
	for _, e := range c.Evidence {
		fmt.Fprintf(a.out, "  · %s\n", e)
	}
	for _, comp := range c.Components {
		fmt.Fprintf(a.out, "  + %-16s %s\n", comp.Kind, comp.Ref)
	}
}

func (a *app) discoverCmd() *cobra.Command {
	var all, adopt, missing bool
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "List repositories the tools know about that no project covers",
		Long: `discover gathers every directory kata, owcli, and bossman know about
(kata workspace bindings, wiki roots, session directories), groups them by
repository, and lists those no project covers yet. --all includes covered
ones; --adopt creates a project for each listed candidate.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&all, "all", false, "include directories a project already covers")
	cmd.Flags().BoolVar(&missing, "missing", false, "include directories that no longer exist")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "adopt every listed candidate")
	cmd.RunE = a.withStore(func(st *store.Store, _ []string) error {
		w := a.collect()
		a.toolWarnings(w)
		cs, err := discover.Discover(context.Background(), st, w)
		if err != nil {
			return err
		}
		var keep []*discover.Candidate
		for _, c := range cs {
			if (c.ClaimedBy != "" && !all) || (c.Missing && !missing) {
				continue
			}
			keep = append(keep, c)
		}
		if adopt {
			for _, c := range keep {
				if c.ClaimedBy != "" {
					continue
				}
				p, err := discover.Adopt(st, c, c.Slug, "", "", nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "adopt %s: %v\n", c.Root, err)
					continue
				}
				fmt.Fprintf(a.out, "adopted %s as %s (%d components)\n", c.Root, p.Slug, len(c.Components))
			}
			return nil
		}
		if a.json {
			if keep == nil {
				keep = []*discover.Candidate{}
			}
			return a.printJSON(keep)
		}
		tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ROOT\tSLUG\tCOVERED BY\tSESSIONS\tCOST\tKNOWN TO")
		for _, c := range keep {
			var known []string
			for _, comp := range c.Components {
				if comp.Kind != store.KindSessions {
					known = append(known, comp.Kind)
				}
			}
			if c.Missing {
				known = append(known, "(missing)")
			}
			cov := c.ClaimedBy
			if cov == "" {
				cov = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", c.Root, c.Slug, cov, c.Sessions, money(c.CostUSD), strings.Join(known, " "))
		}
		return tw.Flush()
	})
	return cmd
}
