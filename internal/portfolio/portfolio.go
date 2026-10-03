// Package portfolio joins goatlassian's projects with what the sibling
// tools report about their components: per-component status and links,
// per-project metrics, last activity, and health flags.
package portfolio

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hoodoo/goatlassian/internal/sources"
	"github.com/Hoodoo/goatlassian/internal/store"
)

// Flag levels, in increasing severity.
const (
	LevelOK    = "ok"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelAlert = "alert"
)

func levelRank(l string) int {
	switch l {
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelAlert:
		return 3
	}
	return 0
}

// Flag is one health observation about a project.
type Flag struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Link is a labelled URL into another tool's UI.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Item is a notable thing inside a component: an issue needing attention,
// a recent session, an unmerged branch.
type Item struct {
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
	URL    string    `json:"url,omitempty"`
	At     time.Time `json:"at,omitempty"`
	Level  string    `json:"level,omitempty"`
}

// ComponentReport is a component with what its tool says about it.
type ComponentReport struct {
	store.Component
	Problem      string    `json:"problem,omitempty"`
	Summary      string    `json:"summary"`
	Links        []Link    `json:"links"`
	Items        []Item    `json:"items"`
	LastActivity time.Time `json:"last_activity,omitempty"`
	Data         any       `json:"data,omitempty"`
}

// Metrics are a project's numbers, summed over its components. Recent
// means within the configured recent window.
type Metrics struct {
	IssuesOpen         int     `json:"issues_open"`
	IssuesClosed       int     `json:"issues_closed"`
	IssuesClosedRecent int     `json:"issues_closed_recent"`
	IssuesStuck        int     `json:"issues_stuck"`
	IssuesNeedsHuman   int     `json:"issues_needs_human"`
	IssuesOverdue      int     `json:"issues_overdue"`
	IssuesBlocked      int     `json:"issues_blocked"`
	Sessions           int     `json:"sessions"`
	SessionsRecent     int     `json:"sessions_recent"`
	CostUSD            float64 `json:"cost_usd"`
	CostRecentUSD      float64 `json:"cost_recent_usd"`
	Tokens             int64   `json:"tokens"`
	TokensRecent       int64   `json:"tokens_recent"`
	OutputTokens       int64   `json:"output_tokens"`
	Interventions      int     `json:"interventions"`
	ToolErrors         int     `json:"tool_errors"`
	AgentHours         float64 `json:"agent_hours"`
	CommitsRecent      int     `json:"commits_recent"`
	GitDirty           int     `json:"git_dirty"`
	GitAhead           int     `json:"git_ahead"`
	WikiDrift          int     `json:"wiki_drift"` // largest drift of any wiki; -1 unknown
}

// Report is a project with its components, metrics, and health.
type Report struct {
	Project        *store.Project    `json:"project"`
	Components     []ComponentReport `json:"components"`
	Metrics        Metrics           `json:"metrics"`
	Flags          []Flag            `json:"flags"`
	Health         string            `json:"health"`
	LastActivity   time.Time         `json:"last_activity,omitempty"`
	LastActivityBy string            `json:"last_activity_by,omitempty"`
	// LastTouched is the latest goatlassian log entry (note, state change,
	// attach). It is bookkeeping, so it does not count as activity.
	LastTouched time.Time `json:"last_touched,omitempty"`
}

// Portfolio is every project's report plus what belongs to no project.
type Portfolio struct {
	CollectedAt time.Time      `json:"collected_at"`
	RecentDays  int            `json:"recent_days"`
	Reports     []*Report      `json:"reports"`
	Totals      Metrics        `json:"totals"`
	Unassigned  Unassigned     `json:"unassigned"`
	Tools       map[string]any `json:"tools"`
}

// Unassigned summarizes sessions no project claims.
type Unassigned struct {
	Sessions int      `json:"sessions"`
	CostUSD  float64  `json:"cost_usd"`
	Paths    []string `json:"paths"` // the busiest unclaimed directories
}

// Analyze builds the portfolio. Only projects for which keep returns true
// are reported (nil keeps all), but session ownership is decided across
// every project so a session is never counted twice.
func Analyze(ctx context.Context, st *store.Store, w *sources.World, keep func(*store.Project) bool) (*Portfolio, error) {
	projects, err := st.Projects()
	if err != nil {
		return nil, err
	}
	comps, err := st.Components(0)
	if err != nil {
		return nil, err
	}
	lastEvents, err := st.LastEvents()
	if err != nil {
		return nil, err
	}
	cfg := w.Config()
	recentSince := w.CollectedAt.AddDate(0, 0, -cfg.RecentDays)
	owner := assignSessions(comps, w.Bossman.Sessions)

	byProject := map[int64][]store.Component{}
	for _, c := range comps {
		byProject[c.ProjectID] = append(byProject[c.ProjectID], c)
	}
	pf := &Portfolio{CollectedAt: w.CollectedAt, RecentDays: cfg.RecentDays, Reports: []*Report{}, Tools: map[string]any{
		"kata":    map[string]string{"problem": w.Kata.Problem, "web_url": w.Kata.WebURL},
		"owcli":   map[string]string{"problem": w.Owcli.Problem},
		"bossman": map[string]string{"problem": w.Bossman.Problem},
	}}
	for _, p := range projects {
		if keep != nil && !keep(p) {
			continue
		}
		r := &Report{Project: p, Components: []ComponentReport{}, Flags: []Flag{}}
		r.Metrics.WikiDrift = -1
		for _, c := range byProject[p.ID] {
			cr := describe(ctx, w, c, owner, recentSince, &r.Metrics)
			r.Components = append(r.Components, cr)
			if cr.LastActivity.After(r.LastActivity) {
				r.LastActivity, r.LastActivityBy = cr.LastActivity, c.Kind
			}
		}
		r.LastTouched = lastEvents[p.ID]
		r.Flags = flags(r, w.CollectedAt, cfg.StaleDays, cfg.WikiDriftCommits)
		r.Health = LevelOK
		for _, f := range r.Flags {
			if levelRank(f.Level) > levelRank(r.Health) {
				r.Health = f.Level
			}
		}
		pf.Reports = append(pf.Reports, r)
		addMetrics(&pf.Totals, r.Metrics)
	}
	paths := map[string]int{}
	for i, s := range w.Bossman.Sessions {
		if owner[i] == 0 {
			pf.Unassigned.Sessions++
			pf.Unassigned.CostUSD += s.CostUSD
			paths[s.Project]++
		}
	}
	for p := range paths {
		pf.Unassigned.Paths = append(pf.Unassigned.Paths, p)
	}
	sort.Slice(pf.Unassigned.Paths, func(i, j int) bool {
		a, b := pf.Unassigned.Paths[i], pf.Unassigned.Paths[j]
		if paths[a] != paths[b] {
			return paths[a] > paths[b]
		}
		return a < b
	})
	return pf, nil
}

func addMetrics(t *Metrics, m Metrics) {
	t.IssuesOpen += m.IssuesOpen
	t.IssuesClosed += m.IssuesClosed
	t.IssuesClosedRecent += m.IssuesClosedRecent
	t.IssuesStuck += m.IssuesStuck
	t.IssuesNeedsHuman += m.IssuesNeedsHuman
	t.IssuesOverdue += m.IssuesOverdue
	t.IssuesBlocked += m.IssuesBlocked
	t.Sessions += m.Sessions
	t.SessionsRecent += m.SessionsRecent
	t.CostUSD += m.CostUSD
	t.CostRecentUSD += m.CostRecentUSD
	t.Tokens += m.Tokens
	t.TokensRecent += m.TokensRecent
	t.OutputTokens += m.OutputTokens
	t.Interventions += m.Interventions
	t.ToolErrors += m.ToolErrors
	t.AgentHours += m.AgentHours
	t.CommitsRecent += m.CommitsRecent
	t.GitDirty += m.GitDirty
	t.GitAhead += m.GitAhead
	if m.WikiDrift > t.WikiDrift {
		t.WikiDrift = m.WikiDrift
	}
}

// assignSessions maps each session (by index) to the component that owns
// it: a session component naming it, else the sessions component with the
// longest directory containing it, so a session in a nested repository
// belongs to the innermost project.
func assignSessions(comps []store.Component, sessions []sources.Session) map[int]int64 {
	owner := map[int]int64{}
	best := map[int]int{}
	pinned := map[string]int64{}
	for _, c := range comps {
		if c.Kind == store.KindSession {
			pinned[c.Ref] = c.ID
		}
	}
	for i, s := range sessions {
		if id, ok := pinned[s.Key]; ok {
			owner[i], best[i] = id, 1<<30
		}
	}
	for _, c := range comps {
		if c.Kind != store.KindSessions {
			continue
		}
		dir := filepath.Clean(c.Ref)
		agent := c.Attrs["agent"]
		for i, s := range sessions {
			if agent != "" && s.Agent != agent {
				continue
			}
			if !within(s.Project, dir) {
				continue
			}
			if len(dir) > best[i] {
				best[i], owner[i] = len(dir), c.ID
			}
		}
	}
	return owner
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

func describe(ctx context.Context, w *sources.World, c store.Component, owner map[int]int64, recentSince time.Time, m *Metrics) ComponentReport {
	cr := ComponentReport{Component: c, Links: []Link{}, Items: []Item{}}
	switch c.Kind {
	case store.KindGit:
		describeGit(ctx, w, &cr, m)
	case store.KindKata:
		describeKata(w, &cr, recentSince, m)
	case store.KindOwcliWiki:
		describeWiki(ctx, w, &cr, m)
	case store.KindOwcliWorkspace:
		describeWorkspace(ctx, w, &cr, m)
	case store.KindSessions, store.KindSession:
		describeSessions(w, &cr, owner, recentSince, m)
	case store.KindLink:
		cr.Summary = c.Label
		if cr.Summary == "" {
			cr.Summary = c.Ref
		}
		cr.Links = append(cr.Links, Link{Label: "open", URL: c.Ref})
	default:
		cr.Summary = "no adapter for kind " + c.Kind
		if c.Label != "" {
			cr.Summary = c.Label
		}
		if strings.HasPrefix(c.Ref, "http://") || strings.HasPrefix(c.Ref, "https://") {
			cr.Links = append(cr.Links, Link{Label: "open", URL: c.Ref})
		}
	}
	return cr
}

func describeGit(ctx context.Context, w *sources.World, cr *ComponentReport, m *Metrics) {
	g := w.Git(ctx, cr.Ref)
	cr.Data = g
	if g.Problem != "" {
		cr.Problem = g.Problem
		return
	}
	cr.LastActivity = g.LastCommit
	parts := []string{g.Branch}
	if g.Dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted", g.Dirty))
	}
	if g.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d unpushed", g.Ahead))
	}
	if g.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", g.Behind))
	}
	if g.Upstream == "" {
		parts = append(parts, "no upstream")
	}
	if g.Head == "" {
		parts = append(parts, "no commits yet")
	} else {
		parts = append(parts, fmt.Sprintf("%d commits recently", g.Recent))
	}
	cr.Summary = strings.Join(parts, " · ")
	if g.LastSubject != "" {
		cr.Items = append(cr.Items, Item{Title: g.LastSubject, Detail: "HEAD " + short(g.Head), At: g.LastCommit})
	}
	for _, b := range g.Unmerged {
		cr.Items = append(cr.Items, Item{Title: b, Detail: "branch not merged into " + g.Branch})
	}
	if u := remoteURL(g.Remote); u != "" {
		cr.Links = append(cr.Links, Link{Label: "remote", URL: u})
	}
	m.CommitsRecent += g.Recent
	m.GitDirty += g.Dirty
	m.GitAhead += g.Ahead
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// remoteURL turns a git remote into a browsable URL when it is one.
func remoteURL(r string) string {
	switch {
	case strings.HasPrefix(r, "https://"):
		return strings.TrimSuffix(r, ".git")
	case strings.HasPrefix(r, "git@"):
		host, path, ok := strings.Cut(strings.TrimPrefix(r, "git@"), ":")
		if ok {
			return "https://" + host + "/" + strings.TrimSuffix(path, ".git")
		}
	}
	return ""
}

func describeKata(w *sources.World, cr *ComponentReport, recentSince time.Time, m *Metrics) {
	if w.Kata.Problem != "" {
		cr.Problem = w.Kata.Problem
		return
	}
	kp := w.Kata.Project(cr.Attrs["uid"])
	if kp == nil {
		kp = w.Kata.Project(cr.Ref)
	}
	if kp == nil {
		cr.Problem = "kata knows no project " + cr.Ref
		return
	}
	if url := w.Kata.ProjectURL(kp.UID); url != "" {
		cr.Links = append(cr.Links, Link{Label: "kata", URL: url})
	}
	var open, closed, closedRecent, stuck, human, overdue, blocked int
	now := w.CollectedAt
	for _, is := range w.Kata.Issues {
		if is.ProjectUID != kp.UID {
			continue
		}
		if is.UpdatedAt.After(cr.LastActivity) {
			cr.LastActivity = is.UpdatedAt
		}
		if is.Status != "open" {
			closed++
			if is.ClosedAt != nil && is.ClosedAt.After(recentSince) {
				closedRecent++
			}
			continue
		}
		open++
		for _, b := range is.BlockedBy {
			if b.Status == "open" {
				blocked++
				break
			}
		}
		att := is.Meta("work.attention")
		msg := is.Meta("work.attention_msg")
		switch att {
		case "stuck":
			stuck++
			cr.Items = append(cr.Items, Item{Title: is.QualifiedID + " " + is.Title, Detail: "stuck: " + msg, URL: is.WebURL, At: is.UpdatedAt, Level: LevelAlert})
		case "needs-human":
			human++
			cr.Items = append(cr.Items, Item{Title: is.QualifiedID + " " + is.Title, Detail: "needs human: " + msg, URL: is.WebURL, At: is.UpdatedAt, Level: LevelAlert})
		}
		if d := parseDeadline(is.DeadlineOn); !d.IsZero() && d.Before(now) {
			overdue++
			cr.Items = append(cr.Items, Item{Title: is.QualifiedID + " " + is.Title, Detail: "deadline passed " + is.DeadlineOn, URL: is.WebURL, At: d, Level: LevelAlert})
		}
	}
	parts := []string{fmt.Sprintf("%d open", open), fmt.Sprintf("%d closed", closed)}
	if stuck > 0 {
		parts = append(parts, fmt.Sprintf("%d stuck", stuck))
	}
	if human > 0 {
		parts = append(parts, fmt.Sprintf("%d need a human", human))
	}
	if overdue > 0 {
		parts = append(parts, fmt.Sprintf("%d overdue", overdue))
	}
	cr.Summary = kp.Name + ": " + strings.Join(parts, " · ")
	cr.Data = map[string]any{"uid": kp.UID, "name": kp.Name, "paths": kp.Paths, "open": open, "closed": closed, "blocked": blocked}
	m.IssuesOpen += open
	m.IssuesClosed += closed
	m.IssuesClosedRecent += closedRecent
	m.IssuesStuck += stuck
	m.IssuesNeedsHuman += human
	m.IssuesOverdue += overdue
	m.IssuesBlocked += blocked
}

func parseDeadline(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.AddDate(0, 0, 1)
	}
	return time.Time{}
}

// wikiDrift reports a wiki's commits behind HEAD; -1 when unknown.
func wikiDrift(ctx context.Context, w *sources.World, wk *sources.OwcliWiki) int {
	if wk.LastUpdate == nil || wk.Problem != "" {
		return -1
	}
	return w.CommitsSince(ctx, wk.RepoRoot, wk.LastUpdate.GitHead)
}

func describeWiki(ctx context.Context, w *sources.World, cr *ComponentReport, m *Metrics) {
	if w.Owcli.Problem != "" {
		cr.Problem = w.Owcli.Problem
		return
	}
	wk := w.Owcli.Wiki(cr.Ref)
	if wk == nil {
		cr.Problem = "owcli knows no wiki " + cr.Ref
		return
	}
	cr.Links = append(cr.Links, Link{Label: "wiki", URL: sources.WikiURL(w.Config().Services.OwcliURL, "wiki", wk.ID)})
	if wk.Problem != "" {
		cr.Problem = wk.Problem
		cr.Summary = wk.Name + ": not generated"
		return
	}
	drift := wikiDrift(ctx, w, wk)
	if drift > m.WikiDrift {
		m.WikiDrift = drift
	}
	cr.Data = map[string]any{"name": wk.Name, "repo_root": wk.RepoRoot, "wiki_dir": wk.WikiDir, "drift": drift, "last_update": wk.LastUpdate}
	if wk.LastUpdate == nil {
		cr.Summary = wk.Name + ": never updated"
		return
	}
	cr.LastActivity = wk.LastUpdate.UpdatedAt
	status := wk.LastUpdate.Command + " " + wk.LastUpdate.Status
	switch {
	case drift < 0:
		cr.Summary = fmt.Sprintf("%s: %s; drift unknown", wk.Name, status)
	case drift == 0:
		cr.Summary = fmt.Sprintf("%s: %s; current with HEAD", wk.Name, status)
	default:
		cr.Summary = fmt.Sprintf("%s: %s; %d commits behind HEAD", wk.Name, status, drift)
	}
	for _, ws := range wk.Workspaces {
		cr.Links = append(cr.Links, Link{Label: "workspace " + ws.Name, URL: sources.WikiURL(w.Config().Services.OwcliURL, "ws", ws.ID)})
	}
}

func describeWorkspace(ctx context.Context, w *sources.World, cr *ComponentReport, m *Metrics) {
	if w.Owcli.Problem != "" {
		cr.Problem = w.Owcli.Problem
		return
	}
	ws := w.Owcli.Workspace(cr.Ref)
	if ws == nil {
		cr.Problem = "owcli knows no workspace " + cr.Ref
		return
	}
	cr.Links = append(cr.Links, Link{Label: "workspace", URL: sources.WikiURL(w.Config().Services.OwcliURL, "ws", ws.ID)})
	behind := 0
	for _, id := range ws.Wikis {
		wk := w.Owcli.Wiki(id)
		if wk == nil {
			cr.Items = append(cr.Items, Item{Title: id, Detail: "unknown wiki", Level: LevelWarn})
			continue
		}
		drift := wikiDrift(ctx, w, wk)
		detail := "drift unknown"
		if drift >= 0 {
			detail = fmt.Sprintf("%d commits behind", drift)
		}
		if drift > 0 {
			behind++
		}
		if wk.Problem != "" {
			detail = wk.Problem
		}
		if wk.LastUpdate != nil && wk.LastUpdate.UpdatedAt.After(cr.LastActivity) {
			cr.LastActivity = wk.LastUpdate.UpdatedAt
		}
		cr.Items = append(cr.Items, Item{Title: wk.Name, Detail: detail, URL: sources.WikiURL(w.Config().Services.OwcliURL, "wiki", wk.ID)})
	}
	cr.Summary = fmt.Sprintf("%s: %d wikis, %d behind HEAD", ws.Name, len(ws.Wikis), behind)
}

func describeSessions(w *sources.World, cr *ComponentReport, owner map[int]int64, recentSince time.Time, m *Metrics) {
	if w.Bossman.Problem != "" {
		cr.Problem = w.Bossman.Problem
		return
	}
	base := w.Config().Services.BossmanURL
	if cr.Kind == store.KindSession {
		cr.Links = append(cr.Links, Link{Label: "session", URL: sources.SessionURL(base, cr.Ref)})
	} else {
		cr.Links = append(cr.Links, Link{Label: "sessions", URL: sources.SessionsURL(base, cr.Ref)})
	}
	var mine []sources.Session
	for i, s := range w.Bossman.Sessions {
		if owner[i] == cr.ID {
			mine = append(mine, s)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].StartedAt.After(mine[j].StartedAt) })
	var cost, costRecent float64
	var tokens, tokensRecent int64
	recent, open := 0, 0
	agents := map[string]int{}
	for _, s := range mine {
		cost += s.CostUSD
		tokens += s.Tokens()
		agents[s.Agent]++
		if s.StartedAt.After(recentSince) {
			recent++
			costRecent += s.CostUSD
			tokensRecent += s.Tokens()
		}
		if !s.Concluded {
			open++
		}
		end := s.EndedAt
		if end.IsZero() {
			end = s.StartedAt
		}
		if end.After(cr.LastActivity) {
			cr.LastActivity = end
		}
		m.Interventions += s.Interventions
		m.ToolErrors += s.ToolErrors
		m.AgentHours += s.ActiveS / 3600
		m.OutputTokens += s.Output
	}
	for i, s := range mine {
		if i == 8 {
			break
		}
		cr.Items = append(cr.Items, Item{
			Title:  s.Name(),
			Detail: fmt.Sprintf("%s · $%.2f · %s tokens · %d interventions", s.Agent, s.CostUSD, Compact(s.Tokens()), s.Interventions),
			URL:    sources.SessionURL(base, s.Key),
			At:     s.StartedAt,
		})
	}
	var as []string
	for a, n := range agents {
		as = append(as, fmt.Sprintf("%d %s", n, a))
	}
	sort.Strings(as)
	if len(mine) == 0 && cr.Kind == store.KindSession {
		cr.Problem = "bossman knows no session " + cr.Ref
	} else if len(mine) == 0 {
		cr.Summary = "no sessions"
	} else {
		cr.Summary = fmt.Sprintf("%d sessions (%s) · $%.2f · %s tokens; %d recent, $%.2f", len(mine), strings.Join(as, ", "), cost, Compact(tokens), recent, costRecent)
	}
	cr.Data = map[string]any{"sessions": len(mine), "recent": recent, "unconcluded": open, "cost_usd": cost}
	m.Sessions += len(mine)
	m.SessionsRecent += recent
	m.CostUSD += cost
	m.CostRecentUSD += costRecent
	m.Tokens += tokens
	m.TokensRecent += tokensRecent
}

// Compact formats a count as 1.2k, 3.4M, 5.6B.
func Compact(n int64) string {
	f := float64(n)
	switch {
	case f >= 1e9:
		return fmt.Sprintf("%.1fB", f/1e9)
	case f >= 1e6:
		return fmt.Sprintf("%.1fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.1fk", f/1e3)
	}
	return fmt.Sprint(n)
}

func flags(r *Report, now time.Time, staleDays, driftLimit int) []Flag {
	out := []Flag{}
	add := func(code, level, format string, args ...any) {
		out = append(out, Flag{Code: code, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	m := r.Metrics
	state := r.Project.State
	for _, c := range r.Components {
		if c.Problem != "" {
			add("problem", LevelAlert, "%s %s: %s", c.Kind, c.Ref, c.Problem)
		}
	}
	if len(r.Components) == 0 {
		add("empty", LevelInfo, "no components attached")
	}
	if m.IssuesStuck > 0 {
		add("stuck", LevelAlert, "%d issue(s) stuck", m.IssuesStuck)
	}
	if m.IssuesNeedsHuman > 0 {
		add("needs-human", LevelAlert, "%d issue(s) need a human", m.IssuesNeedsHuman)
	}
	if m.IssuesOverdue > 0 {
		add("overdue", LevelAlert, "%d issue(s) past their deadline", m.IssuesOverdue)
	}
	idle := now.Sub(r.LastActivity)
	switch state {
	case store.StateActive:
		if !r.LastActivity.IsZero() && idle > time.Duration(staleDays)*24*time.Hour {
			add("stale", LevelWarn, "active but idle for %d days", int(idle.Hours()/24))
		}
	case store.StatePaused, store.StateDone, store.StateArchived:
		if !r.LastActivity.IsZero() && idle < 72*time.Hour {
			add("activity", LevelInfo, "%s but %s activity %s", state, r.LastActivityBy, Ago(now, r.LastActivity))
		}
		if state != store.StatePaused && m.IssuesOpen > 0 {
			add("open-issues", LevelWarn, "%s with %d open issue(s)", state, m.IssuesOpen)
		}
	}
	if m.WikiDrift >= driftLimit {
		add("wiki-behind", LevelWarn, "wiki %d commits behind HEAD", m.WikiDrift)
	}
	if m.GitDirty > 0 {
		add("dirty", LevelInfo, "%d uncommitted path(s)", m.GitDirty)
	}
	if m.GitAhead > 0 {
		add("unpushed", LevelInfo, "%d unpushed commit(s)", m.GitAhead)
	}
	return out
}

// Ago renders the time from t to now coarsely: "3h ago", "5d ago".
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("2006-01-02")
}

// SnapshotData is what a snapshot records for one project.
type SnapshotData struct {
	Metrics Metrics  `json:"metrics"`
	Health  string   `json:"health"`
	Flags   []string `json:"flags"`
	State   string   `json:"state"`
}

// Record stores a snapshot of every report in the portfolio.
func Record(st *store.Store, pf *Portfolio) error {
	for _, r := range pf.Reports {
		d := SnapshotData{Metrics: r.Metrics, Health: r.Health, State: r.Project.State, Flags: []string{}}
		for _, f := range r.Flags {
			d.Flags = append(d.Flags, f.Code)
		}
		if err := st.AddSnapshot(r.Project.ID, pf.CollectedAt, d); err != nil {
			return err
		}
	}
	return nil
}
