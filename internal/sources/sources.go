// Package sources reads the sibling tools through their command lines:
// kata (issues), owcli (wikis), bossman (agent sessions), and git. Each
// tool is collected in bulk once per refresh; a missing binary or daemon
// becomes a Problem on that tool, never a failure of the whole collection.
package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hoodoo/goatlassian/internal/config"
)

// Runner runs a command and returns its standard output.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// Exec runs real processes.
type Exec struct{}

// Run implements Runner. The error includes the command's stderr.
func (Exec) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// World is everything collected from the sibling tools at one moment.
type World struct {
	CollectedAt time.Time    `json:"collected_at"`
	Kata        KataWorld    `json:"kata"`
	Owcli       OwcliWorld   `json:"owcli"`
	Bossman     BossmanWorld `json:"bossman"`

	cfg    config.Config
	runner Runner
	mu     sync.Mutex
	git    map[string]*GitStatus
}

// Collect gathers kata, owcli, and bossman concurrently. Git is read
// lazily per repository with Git.
func Collect(ctx context.Context, cfg config.Config, r Runner) *World {
	w := &World{CollectedAt: time.Now().UTC(), cfg: cfg, runner: r, git: map[string]*GitStatus{}}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); w.Kata = collectKata(ctx, cfg.Bin.Kata, r) }()
	go func() { defer wg.Done(); w.Owcli = collectOwcli(ctx, cfg.Bin.Owcli, r) }()
	go func() { defer wg.Done(); w.Bossman = collectBossman(ctx, cfg.Bin.Bossman, r) }()
	wg.Wait()
	return w
}

func runJSON(ctx context.Context, r Runner, v any, name string, args ...string) error {
	out, err := r.Run(ctx, "", name, args...)
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return fmt.Errorf("%s is not installed (%v)", name, ee.Err)
		}
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("%s %s: unexpected output: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// ---- kata --------------------------------------------------------------

// KataWorld is every kata project and issue the local daemon knows.
type KataWorld struct {
	Problem  string        `json:"problem,omitempty"`
	WebURL   string        `json:"web_url,omitempty"`
	Projects []KataProject `json:"projects"`
	Issues   []KataIssue   `json:"-"`
}

// KataProject is a kata project with the workspace paths bound to it.
type KataProject struct {
	ID      int64    `json:"id"`
	UID     string   `json:"uid"`
	Name    string   `json:"name"`
	Active  bool     `json:"active"`
	Paths   []string `json:"paths"`   // local workspace bindings
	Remotes []string `json:"remotes"` // git remote bindings, e.g. github.com/me/repo
}

// KataIssue is the part of a kata issue goatlassian reports on.
type KataIssue struct {
	UID         string         `json:"uid"`
	ShortID     string         `json:"short_id"`
	QualifiedID string         `json:"qualified_id"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	Priority    *int           `json:"priority"`
	Owner       string         `json:"owner"`
	Labels      []string       `json:"labels"`
	ProjectUID  string         `json:"project_uid"`
	ProjectName string         `json:"project_name"`
	Metadata    map[string]any `json:"metadata"`
	WebURL      string         `json:"web_url"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ClosedAt    *time.Time     `json:"closed_at"`
	DeadlineOn  string         `json:"deadline_on"`
	BlockedBy   []struct {
		Status string `json:"status"`
	} `json:"blocked_by"`
}

// Meta returns a metadata value as a string.
func (i KataIssue) Meta(key string) string {
	switch v := i.Metadata[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ProjectURL links to a kata project in the daemon's web UI.
func (k KataWorld) ProjectURL(uid string) string {
	if k.WebURL == "" || uid == "" {
		return ""
	}
	return strings.TrimRight(k.WebURL, "/") + "/kata?scope=" + uid
}

// Project finds a kata project by UID or name.
func (k KataWorld) Project(uidOrName string) *KataProject {
	for i := range k.Projects {
		if k.Projects[i].UID == uidOrName {
			return &k.Projects[i]
		}
	}
	for i := range k.Projects {
		if k.Projects[i].Name == uidOrName {
			return &k.Projects[i]
		}
	}
	return nil
}

func collectKata(ctx context.Context, bin string, r Runner) KataWorld {
	k := KataWorld{Projects: []KataProject{}}
	var status struct {
		Daemons []struct {
			WebURL string `json:"web_url"`
		} `json:"daemons"`
	}
	if err := runJSON(ctx, r, &status, bin, "daemon", "status", "--json"); err != nil {
		k.Problem = err.Error()
		return k
	}
	if len(status.Daemons) > 0 {
		k.WebURL = status.Daemons[0].WebURL
	}
	var projects struct {
		Projects []KataProject `json:"projects"`
	}
	if err := runJSON(ctx, r, &projects, bin, "projects", "list", "--json"); err != nil {
		k.Problem = err.Error()
		return k
	}
	k.Projects = projects.Projects
	var wg sync.WaitGroup
	for i := range k.Projects {
		wg.Add(1)
		go func(p *KataProject) {
			defer wg.Done()
			var show struct {
				Aliases []struct {
					Identity string `json:"alias_identity"`
					Kind     string `json:"alias_kind"`
				} `json:"aliases"`
			}
			if runJSON(ctx, r, &show, bin, "projects", "show", p.Name, "--json") != nil {
				return
			}
			p.Paths, p.Remotes = []string{}, []string{}
			for _, a := range show.Aliases {
				if path, ok := strings.CutPrefix(a.Identity, "local://"); ok {
					p.Paths = append(p.Paths, filepath.Clean(path))
				} else if a.Kind == "git" {
					p.Remotes = append(p.Remotes, a.Identity)
				}
			}
		}(&k.Projects[i])
	}
	var issues struct {
		Issues []KataIssue `json:"issues"`
	}
	err := runJSON(ctx, r, &issues, bin, "list", "--all", "--status", "all", "--limit", "0", "--json")
	wg.Wait()
	if err != nil {
		k.Problem = err.Error()
		return k
	}
	k.Issues = issues.Issues
	return k
}

// ---- owcli -------------------------------------------------------------

// OwcliWorld is every wiki and workspace owcli knows.
type OwcliWorld struct {
	Problem    string           `json:"problem,omitempty"`
	Wikis      []OwcliWiki      `json:"wikis"`
	Workspaces []OwcliWorkspace `json:"workspaces"`
}

// OwcliWiki is one repository wiki.
type OwcliWiki struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RepoRoot   string `json:"repoRoot"`
	WikiDir    string `json:"wikiDir"`
	Layout     string `json:"layout"`
	Bound      bool   `json:"bound"`
	Workspaces []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
	} `json:"workspaces"`
	LastUpdate *struct {
		UpdatedAt time.Time `json:"updatedAt"`
		Command   string    `json:"command"`
		GitHead   string    `json:"gitHead"`
		Status    string    `json:"status"`
	} `json:"lastUpdate"`
	Problem string `json:"problem"`
}

// OwcliWorkspace groups wikis for shared search.
type OwcliWorkspace struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Wikis []string `json:"wikis"`
}

// Wiki finds a wiki by ID.
func (o OwcliWorld) Wiki(id string) *OwcliWiki {
	for i := range o.Wikis {
		if o.Wikis[i].ID == id {
			return &o.Wikis[i]
		}
	}
	return nil
}

// WikiFor finds the wiki a component refers to: by ID, else by repository
// root. owcli's ID for a wiki is not stable (it switches from the hash form
// to the workspace registry's slug when the repository joins a workspace),
// so components also record the root they were attached with.
func (o OwcliWorld) WikiFor(id, repoRoot string) *OwcliWiki {
	if wk := o.Wiki(id); wk != nil {
		return wk
	}
	if repoRoot == "" {
		return nil
	}
	for i := range o.Wikis {
		if o.Wikis[i].RepoRoot == repoRoot {
			return &o.Wikis[i]
		}
	}
	return nil
}

// Workspace finds a workspace by ID or name.
func (o OwcliWorld) Workspace(id string) *OwcliWorkspace {
	for i := range o.Workspaces {
		if o.Workspaces[i].ID == id || o.Workspaces[i].Name == id {
			return &o.Workspaces[i]
		}
	}
	return nil
}

func collectOwcli(ctx context.Context, bin string, r Runner) OwcliWorld {
	o := OwcliWorld{Wikis: []OwcliWiki{}, Workspaces: []OwcliWorkspace{}}
	if err := runJSON(ctx, r, &o, bin, "wikis", "--json"); err != nil {
		o.Problem = err.Error()
	}
	return o
}

// ---- bossman -----------------------------------------------------------

// BossmanWorld is every coding-agent session bossman has indexed.
type BossmanWorld struct {
	Problem  string    `json:"problem,omitempty"`
	Sessions []Session `json:"-"`
}

// Session is the part of a bossman session goatlassian reports on.
type Session struct {
	Key           string    `json:"key"`
	Agent         string    `json:"agent"`
	ID            string    `json:"id"`
	Project       string    `json:"project"`
	GitBranch     string    `json:"git_branch"`
	Title         string    `json:"title"`
	DisplayName   string    `json:"display_name"`
	Summary       string    `json:"summary"`
	Concluded     bool      `json:"concluded"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	WallS         float64   `json:"wall_s"`
	ActiveS       float64   `json:"active_s"`
	Prompts       int       `json:"prompts"`
	Interventions int       `json:"interventions"`
	ToolCalls     int       `json:"tool_calls"`
	ToolErrors    int       `json:"tool_errors"`
	APIErrors     int       `json:"api_errors"`
	Input         int64     `json:"input"`
	CacheWrite    int64     `json:"cache_write"`
	CacheRead     int64     `json:"cache_read"`
	Output        int64     `json:"output"`
	Reasoning     int64     `json:"reasoning"`
	CostUSD       float64   `json:"cost_usd"`
	Models        string    `json:"models"`
	Tags          []string  `json:"tags"`
}

// Tokens is every token the session moved: input, cache, and output.
func (s Session) Tokens() int64 { return s.Input + s.CacheWrite + s.CacheRead + s.Output }

// Name is the display name, else the title, else the ID.
func (s Session) Name() string {
	switch {
	case s.DisplayName != "":
		return s.DisplayName
	case s.Title != "":
		return s.Title
	}
	return s.ID
}

func collectBossman(ctx context.Context, bin string, r Runner) BossmanWorld {
	b := BossmanWorld{}
	if err := runJSON(ctx, r, &b.Sessions, bin, "--json", "ls", "-n", "0"); err != nil {
		b.Problem = err.Error()
	}
	return b
}

// SessionURL links to a session in bossman's web UI.
func SessionURL(base, key string) string {
	return strings.TrimRight(base, "/") + "/#/s/" + urlEscape(key)
}

// SessionsURL links to bossman's session list filtered by project path.
func SessionsURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/#/?project=" + urlEscape(path)
}

// TagURL links to bossman's session list filtered by a tag.
func TagURL(base, tag string) string {
	return strings.TrimRight(base, "/") + "/#/?tag=" + urlEscape(tag)
}

// WikiURL links to a wiki or workspace ("wiki" or "ws") in owcli's viewer.
func WikiURL(base, kind, id string) string {
	return strings.TrimRight(base, "/") + "/#scope=" + urlEscape(kind+":"+id)
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~/:", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// RemoteIdentity reduces a git remote URL to host/path, the form kata uses
// for git bindings: git@github.com:me/repo.git → github.com/me/repo.
func RemoteIdentity(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" {
		return ""
	}
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
		if at := strings.LastIndex(r, "@"); at >= 0 && at < strings.Index(r+"/", "/") {
			r = r[at+1:]
		}
	} else if at := strings.Index(r, "@"); at >= 0 {
		r = strings.Replace(r[at+1:], ":", "/", 1)
	}
	r = strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git")
	return strings.ToLower(r)
}

// ---- git ---------------------------------------------------------------

// GitStatus describes a working tree.
type GitStatus struct {
	Problem     string    `json:"problem,omitempty"`
	Root        string    `json:"root"`
	Branch      string    `json:"branch"`
	Upstream    string    `json:"upstream,omitempty"`
	Remote      string    `json:"remote,omitempty"`
	Dirty       int       `json:"dirty"`              // changed or untracked paths
	Ahead       int       `json:"ahead"`              // commits not on the upstream
	Behind      int       `json:"behind"`             // upstream commits not here
	Branches    int       `json:"branches"`           // local branches
	LastCommit  time.Time `json:"last_commit"`        // committer date of HEAD
	LastSubject string    `json:"last_subject"`       // subject of HEAD
	Head        string    `json:"head"`               // HEAD commit
	Recent      int       `json:"recent"`             // commits in the recent window
	Unmerged    []string  `json:"unmerged,omitempty"` // local branches not merged into HEAD
}

// Git reads a repository's status, cached for the life of the World.
func (w *World) Git(ctx context.Context, path string) *GitStatus {
	w.mu.Lock()
	if g, ok := w.git[path]; ok {
		w.mu.Unlock()
		return g
	}
	w.mu.Unlock()
	g := readGit(ctx, w.cfg.Bin.Git, w.runner, path, w.cfg.RecentDays)
	w.mu.Lock()
	w.git[path] = g
	w.mu.Unlock()
	return g
}

// GitRoot resolves the top level of the repository containing path.
func GitRoot(ctx context.Context, r Runner, bin, path string) (string, error) {
	out, err := r.Run(ctx, "", bin, "-C", path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func readGit(ctx context.Context, bin string, r Runner, path string, recentDays int) *GitStatus {
	g := &GitStatus{Root: path}
	git := func(args ...string) (string, error) {
		out, err := r.Run(ctx, "", bin, append([]string{"-C", path}, args...)...)
		return strings.TrimSpace(string(out)), err
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		g.Problem = "not a git repository: " + path
		return g
	}
	g.Root = root
	g.Branch, _ = git("branch", "--show-current")
	if g.Branch == "" {
		g.Branch = "(detached)"
	}
	if st, err := git("status", "--porcelain"); err == nil && st != "" {
		g.Dirty = len(strings.Split(st, "\n"))
	}
	if log, err := git("log", "-1", "--format=%H%x00%cI%x00%s"); err == nil && log != "" {
		parts := strings.SplitN(log, "\x00", 3)
		if len(parts) == 3 {
			g.Head = parts[0]
			g.LastCommit, _ = time.Parse(time.RFC3339, parts[1])
			g.LastSubject = parts[2]
		}
	}
	if up, err := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		g.Upstream = up
		if ab, err := git("rev-list", "--left-right", "--count", "HEAD...@{u}"); err == nil {
			fmt.Sscanf(ab, "%d %d", &g.Ahead, &g.Behind)
		}
	}
	g.Remote, _ = git("remote", "get-url", "origin")
	if n, err := git("rev-list", "--count", fmt.Sprintf("--since=%d.days", recentDays), "HEAD"); err == nil {
		fmt.Sscanf(n, "%d", &g.Recent)
	}
	if bs, err := git("for-each-ref", "--format=%(refname:short)", "refs/heads"); err == nil && bs != "" {
		g.Branches = len(strings.Split(bs, "\n"))
	}
	if bs, err := git("branch", "--no-merged", "HEAD", "--format=%(refname:short)"); err == nil && bs != "" {
		g.Unmerged = strings.Split(bs, "\n")
	}
	return g
}

// CommitsSince counts commits from rev to HEAD in a repository; -1 when
// rev is unknown (for example, rewritten history).
func (w *World) CommitsSince(ctx context.Context, path, rev string) int {
	if rev == "" {
		return -1
	}
	out, err := w.runner.Run(ctx, "", w.cfg.Bin.Git, "-C", path, "rev-list", "--count", rev+"..HEAD")
	if err != nil {
		return -1
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

// Config returns the configuration the World was collected with.
func (w *World) Config() config.Config { return w.cfg }

// Runner returns the runner the World was collected with.
func (w *World) Runner() Runner { return w.runner }
