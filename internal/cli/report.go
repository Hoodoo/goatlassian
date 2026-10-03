package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/goatlassian/internal/portfolio"
	"github.com/Hoodoo/goatlassian/internal/store"
)

func (a *app) analyze(st *store.Store, keep func(*store.Project) bool) (*portfolio.Portfolio, error) {
	w := a.collect()
	a.toolWarnings(w)
	return portfolio.Analyze(context.Background(), st, w, keep)
}

// sortReports orders reports by a status --sort key.
func sortReports(rs []*portfolio.Report, key string) error {
	rank := map[string]int{portfolio.LevelAlert: 0, portfolio.LevelWarn: 1, portfolio.LevelInfo: 2, portfolio.LevelOK: 3}
	var less func(a, b *portfolio.Report) bool
	switch key {
	case "activity":
		less = func(a, b *portfolio.Report) bool { return a.LastActivity.After(b.LastActivity) }
	case "health":
		less = func(a, b *portfolio.Report) bool {
			if rank[a.Health] != rank[b.Health] {
				return rank[a.Health] < rank[b.Health]
			}
			return a.LastActivity.After(b.LastActivity)
		}
	case "cost":
		less = func(a, b *portfolio.Report) bool { return a.Metrics.CostUSD > b.Metrics.CostUSD }
	case "recent-cost":
		less = func(a, b *portfolio.Report) bool { return a.Metrics.CostRecentUSD > b.Metrics.CostRecentUSD }
	case "tokens":
		less = func(a, b *portfolio.Report) bool { return a.Metrics.Tokens > b.Metrics.Tokens }
	case "open":
		less = func(a, b *portfolio.Report) bool { return a.Metrics.IssuesOpen > b.Metrics.IssuesOpen }
	case "idle":
		less = func(a, b *portfolio.Report) bool { return a.LastActivity.Before(b.LastActivity) }
	case "slug":
		less = func(a, b *portfolio.Report) bool { return a.Project.Slug < b.Project.Slug }
	default:
		return fmt.Errorf("unknown --sort %q (activity, health, cost, recent-cost, tokens, open, idle, slug)", key)
	}
	sort.SliceStable(rs, func(i, j int) bool { return less(rs[i], rs[j]) })
	return nil
}

func (a *app) statusCmd() *cobra.Command {
	var all, record bool
	var state, tag, sortKey string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Portfolio: every project's activity, attention, issues, wiki drift, and cost",
		Long: `status reads kata, owcli, bossman, and git once and reports each project:
health (the worst of its flags), last activity across its components,
open and attention-needing issues, wiki drift, sessions, and cost. Recent
columns cover the last recent_days (config.toml, default 30).

Flags explain the health: problem (a component its tool cannot resolve),
stuck / needs-human / overdue (kata), stale (active but idle past
stale_days), activity (paused or done yet moving), open-issues (done with
open issues), wiki-behind, dirty, unpushed, empty.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&all, "all", false, "include archived projects")
	cmd.Flags().StringVar(&state, "state", "", "only this lifecycle state")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "only projects with this tag")
	cmd.Flags().StringVarP(&sortKey, "sort", "s", "health", "activity, health, cost, recent-cost, tokens, open, idle, slug")
	cmd.Flags().BoolVar(&record, "record", false, "also record a snapshot of every project's metrics")
	cmd.RunE = a.withStore(func(st *store.Store, _ []string) error {
		keep := func(p *store.Project) bool {
			if state != "" {
				return p.State == state
			}
			if !all && p.State == store.StateArchived {
				return false
			}
			return tag == "" || hasTag(p.Tags, tag)
		}
		pf, err := a.analyze(st, keep)
		if err != nil {
			return err
		}
		if err := sortReports(pf.Reports, sortKey); err != nil {
			return err
		}
		if record {
			if err := portfolio.Record(st, pf); err != nil {
				return err
			}
		}
		if a.json {
			return a.printJSON(pf)
		}
		if len(pf.Reports) == 0 {
			fmt.Fprintln(a.out, "no projects yet: try goatlassian discover, then goatlassian adopt <dir>")
			return nil
		}
		tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "PROJECT\tSTATE\tHEALTH\tACTIVE\tOPEN\tATTN\tCLOSED/%dd\tSESS/%dd\tCOST\tCOST/%dd\tTOKENS\tWIKI\tFLAGS\n", pf.RecentDays, pf.RecentDays, pf.RecentDays)
		for _, r := range pf.Reports {
			m := r.Metrics
			wiki := "-"
			if m.WikiDrift >= 0 {
				wiki = fmt.Sprintf("+%d", m.WikiDrift)
			}
			var codes []string
			for _, f := range r.Flags {
				codes = append(codes, f.Code)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d/%d\t%s\t%s\t%s\t%s\t%s\n",
				r.Project.Slug, r.Project.State, r.Health, ago(r.LastActivity),
				m.IssuesOpen, m.IssuesStuck+m.IssuesNeedsHuman+m.IssuesOverdue, m.IssuesClosedRecent,
				m.Sessions, m.SessionsRecent, money(m.CostUSD), money(m.CostRecentUSD),
				portfolio.Compact(m.Tokens), wiki, strings.Join(codes, " "))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		t := pf.Totals
		fmt.Fprintf(a.out, "\n%d projects · %d open issues (%d need attention) · %d sessions · %s total, %s in %dd\n",
			len(pf.Reports), t.IssuesOpen, t.IssuesStuck+t.IssuesNeedsHuman+t.IssuesOverdue, t.Sessions, money(t.CostUSD), money(t.CostRecentUSD), pf.RecentDays)
		if u := pf.Unassigned; u.Sessions > 0 {
			top := u.Paths
			if len(top) > 3 {
				top = top[:3]
			}
			fmt.Fprintf(a.out, "%d sessions (%s) belong to no project, mostly in %s\n", u.Sessions, money(u.CostUSD), strings.Join(top, ", "))
		}
		return nil
	})
	return cmd
}

func (a *app) showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <slug>",
		Short: "Show one project: components with their status and links, flags, recent log",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		p, err := st.Project(args[0])
		if err != nil {
			return err
		}
		pf, err := a.analyze(st, func(x *store.Project) bool { return x.ID == p.ID })
		if err != nil {
			return err
		}
		r := pf.Reports[0]
		evs, err := st.Events(p.ID, 10)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"report": r, "events": evs})
		}
		fmt.Fprintf(a.out, "%s — %s\n", p.Name, p.Slug)
		if p.Description != "" {
			fmt.Fprintf(a.out, "%s\n", p.Description)
		}
		fmt.Fprintf(a.out, "state %s · health %s · last activity %s", p.State, r.Health, ago(r.LastActivity))
		if r.LastActivityBy != "" {
			fmt.Fprintf(a.out, " (%s)", r.LastActivityBy)
		}
		if len(p.Tags) > 0 {
			fmt.Fprintf(a.out, " · tags %s", strings.Join(p.Tags, ", "))
		}
		fmt.Fprintln(a.out)
		if len(r.Flags) > 0 {
			fmt.Fprintln(a.out, "\nFlags:")
			for _, f := range r.Flags {
				fmt.Fprintf(a.out, "  [%s] %s\n", f.Level, f.Message)
			}
		}
		fmt.Fprintln(a.out, "\nComponents:")
		for _, c := range r.Components {
			label := c.Ref
			if c.Label != "" && c.Label != c.Ref {
				label += " (" + c.Label + ")"
			}
			fmt.Fprintf(a.out, "  #%d %s %s\n", c.ID, c.Kind, label)
			if c.Problem != "" {
				fmt.Fprintf(a.out, "      problem: %s\n", c.Problem)
			}
			if c.Summary != "" {
				fmt.Fprintf(a.out, "      %s\n", c.Summary)
			}
			for _, it := range c.Items {
				line := it.Title
				if it.Detail != "" {
					line += " — " + it.Detail
				}
				if !it.At.IsZero() {
					line += " (" + ago(it.At) + ")"
				}
				fmt.Fprintf(a.out, "      · %s\n", line)
			}
			for _, l := range c.Links {
				fmt.Fprintf(a.out, "      → %s: %s\n", l.Label, l.URL)
			}
		}
		m := r.Metrics
		fmt.Fprintf(a.out, "\nIssues %d open, %d closed (%d in %dd), %d blocked · sessions %d (%d in %dd) · cost %s (%s in %dd) · %s tokens · %.1f agent-hours · %d interventions\n",
			m.IssuesOpen, m.IssuesClosed, m.IssuesClosedRecent, pf.RecentDays, m.IssuesBlocked, m.Sessions, m.SessionsRecent, pf.RecentDays,
			money(m.CostUSD), money(m.CostRecentUSD), pf.RecentDays, portfolio.Compact(m.Tokens), m.AgentHours, m.Interventions)
		if len(evs) > 0 {
			fmt.Fprintln(a.out, "\nLog:")
			for _, e := range evs {
				fmt.Fprintf(a.out, "  %s  %-7s %s\n", e.At.Local().Format("2006-01-02 15:04"), e.Kind, e.Message)
			}
		}
		return nil
	})
	return cmd
}

func (a *app) snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Record every project's metrics for history (serve does this periodically)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.withStore(func(st *store.Store, _ []string) error {
		pf, err := a.analyze(st, nil)
		if err != nil {
			return err
		}
		if err := portfolio.Record(st, pf); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "recorded %d projects\n", len(pf.Reports))
		return nil
	})
	return cmd
}

func (a *app) historyCmd() *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "history <slug>",
		Short: "Show a project's recorded snapshots: issues, sessions, cost over time",
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().IntVar(&days, "days", 90, "how far back")
	cmd.RunE = a.withStore(func(st *store.Store, args []string) error {
		p, err := st.Project(args[0])
		if err != nil {
			return err
		}
		snaps, err := st.Snapshots(p.ID, time.Now().AddDate(0, 0, -days))
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(snaps)
		}
		if len(snaps) == 0 {
			fmt.Fprintln(a.out, "no snapshots yet: goatlassian snapshot records one (serve records them periodically)")
			return nil
		}
		tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "AT\tSTATE\tHEALTH\tOPEN\tCLOSED\tSESSIONS\tCOST\tTOKENS\tWIKI")
		for _, s := range snaps {
			var d portfolio.SnapshotData
			if json.Unmarshal(s.Data, &d) != nil {
				continue
			}
			m := d.Metrics
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%d\n", s.At.Local().Format("2006-01-02 15:04"), d.State, d.Health,
				m.IssuesOpen, m.IssuesClosed, m.Sessions, money(m.CostUSD), portfolio.Compact(m.Tokens), m.WikiDrift)
		}
		return tw.Flush()
	})
	return cmd
}
