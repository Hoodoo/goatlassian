// Package discover proposes projects from what the sibling tools already
// know: kata workspace bindings, owcli wiki roots, and the directories
// agent sessions ran in.
package discover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Hoodoo/goatlassian/internal/sources"
	"github.com/Hoodoo/goatlassian/internal/store"
)

// Candidate is a directory that could become a project, with the
// components adopting it would attach.
type Candidate struct {
	Root       string            `json:"root"`
	Slug       string            `json:"slug"`
	Git        bool              `json:"git"`
	Missing    bool              `json:"missing,omitempty"`
	Components []store.Component `json:"components"`
	Evidence   []string          `json:"evidence"`
	Sessions   int               `json:"sessions"`
	CostUSD    float64           `json:"cost_usd"`
	ClaimedBy  string            `json:"claimed_by,omitempty"` // slug of a project that already covers Root
}

// root resolves the repository top level containing dir, else dir itself.
func root(ctx context.Context, w *sources.World, dir string) (string, bool) {
	dir = filepath.Clean(dir)
	if r, err := sources.GitRoot(ctx, w.Runner(), w.Config().Bin.Git, dir); err == nil && r != "" {
		return r, true
	}
	return dir, false
}

func within(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// ForDir builds the candidate for the repository containing dir.
func ForDir(ctx context.Context, w *sources.World, dir string) (*Candidate, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r, git := root(ctx, w, abs)
	c := &Candidate{Root: r, Git: git, Slug: store.Slugify(filepath.Base(r)), Components: []store.Component{}, Evidence: []string{}}
	if _, err := os.Stat(r); err != nil {
		c.Missing = true
		c.Evidence = append(c.Evidence, "directory missing")
	}
	if git {
		c.Components = append(c.Components, store.Component{Kind: store.KindGit, Ref: r})
		c.Evidence = append(c.Evidence, "git repository")
	}
	remote := ""
	if git {
		remote = sources.RemoteIdentity(w.Git(ctx, r).Remote)
	}
	for _, kp := range w.Kata.Projects {
		var bound string
		for _, p := range kp.Paths {
			if within(p, r) {
				bound = "at " + p
				break
			}
		}
		for _, rm := range kp.Remotes {
			if bound == "" && remote != "" && strings.ToLower(rm) == remote {
				bound = "to remote " + rm
			}
		}
		if bound != "" {
			c.Components = append(c.Components, store.Component{Kind: store.KindKata, Ref: kp.Name, Attrs: map[string]string{"uid": kp.UID}})
			c.Evidence = append(c.Evidence, "kata project "+kp.Name+" bound "+bound)
		}
	}
	for _, wk := range w.Owcli.Wikis {
		if wk.RepoRoot != "" && within(wk.RepoRoot, r) {
			c.Components = append(c.Components, store.Component{Kind: store.KindOwcliWiki, Ref: wk.ID, Label: wk.Name, Attrs: map[string]string{"root": wk.RepoRoot}})
			ev := "owcli wiki " + wk.ID
			if wk.Problem != "" {
				ev += " (" + wk.Problem + ")"
			}
			c.Evidence = append(c.Evidence, ev)
		}
	}
	for _, s := range w.Bossman.Sessions {
		if within(s.Project, r) {
			c.Sessions++
			c.CostUSD += s.CostUSD
		}
	}
	if c.Sessions > 0 {
		c.Evidence = append(c.Evidence, fmt.Sprintf("%d agent sessions ($%.2f)", c.Sessions, c.CostUSD))
	}
	if git || c.Sessions > 0 {
		c.Components = append(c.Components, store.Component{Kind: store.KindSessions, Ref: r})
	}
	return c, nil
}

// claimedBy returns the slug of a project whose git or sessions component
// covers dir.
func claimedBy(dir string, projects map[int64]string, comps []store.Component) string {
	for _, c := range comps {
		switch c.Kind {
		case store.KindGit:
			if filepath.Clean(c.Ref) == dir {
				return projects[c.ProjectID]
			}
		case store.KindSessions:
			if within(dir, c.Ref) {
				return projects[c.ProjectID]
			}
		}
	}
	return ""
}

// Discover lists every directory the sibling tools know about, grouped by
// repository, with the project that already covers it if any.
func Discover(ctx context.Context, st *store.Store, w *sources.World) ([]*Candidate, error) {
	ps, err := st.Projects()
	if err != nil {
		return nil, err
	}
	slugs := map[int64]string{}
	for _, p := range ps {
		slugs[p.ID] = p.Slug
	}
	comps, err := st.Components(0)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		d = filepath.Clean(d)
		if d != "" && d != "." && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, kp := range w.Kata.Projects {
		for _, p := range kp.Paths {
			add(p)
		}
	}
	for _, wk := range w.Owcli.Wikis {
		add(wk.RepoRoot)
	}
	for _, s := range w.Bossman.Sessions {
		add(s.Project)
	}
	roots := map[string]bool{}
	var out []*Candidate
	for _, d := range dirs {
		r, _ := root(ctx, w, d)
		if roots[r] {
			continue
		}
		roots[r] = true
		c, err := ForDir(ctx, w, r)
		if err != nil {
			return nil, err
		}
		c.ClaimedBy = claimedBy(c.Root, slugs, comps)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out, nil
}

// Adopt creates a project from a candidate, or attaches the candidate's
// components to an existing project when slug names one.
func Adopt(st *store.Store, c *Candidate, slug, name, description string, tags []string) (*store.Project, error) {
	if slug == "" {
		slug = c.Slug
	}
	p, err := st.Project(slug)
	if errors.Is(err, store.ErrNotFound) {
		p, err = st.CreateProject(slug, name, description, tags)
	}
	if err != nil {
		return nil, err
	}
	for _, comp := range c.Components {
		if _, err := st.Attach(p.Slug, comp); err != nil {
			return nil, err
		}
	}
	return st.Project(p.Slug)
}

// Normalize fills in what a hand-attached component leaves implicit: an
// absolute path (the repository top level for git), a kata project's UID,
// an owcli wiki's name and repository root.
func Normalize(ctx context.Context, w *sources.World, c store.Component) (store.Component, error) {
	if c.Attrs == nil {
		c.Attrs = map[string]string{}
	}
	switch c.Kind {
	case store.KindGit, store.KindSessions:
		abs, err := filepath.Abs(c.Ref)
		if err != nil {
			return c, err
		}
		c.Ref = abs
		if c.Kind == store.KindGit {
			r, ok := root(ctx, w, abs)
			if !ok {
				return c, fmt.Errorf("%s is not a git repository", abs)
			}
			c.Ref = r
		}
	case store.KindKata:
		if w.Kata.Problem == "" {
			kp := w.Kata.Project(c.Ref)
			if kp == nil {
				return c, fmt.Errorf("kata knows no project %q", c.Ref)
			}
			c.Ref, c.Attrs["uid"] = kp.Name, kp.UID
		}
	case store.KindSession:
		if w.Bossman.Problem == "" {
			var match []sources.Session
			for _, s := range w.Bossman.Sessions {
				if s.Key == c.Ref || s.ID == c.Ref {
					match = []sources.Session{s}
					break
				}
				if strings.HasPrefix(s.ID, c.Ref) || strings.HasPrefix(s.Key, c.Ref) {
					match = append(match, s)
				}
			}
			switch len(match) {
			case 0:
				return c, fmt.Errorf("bossman knows no session %q (bossman ls)", c.Ref)
			case 1:
				c.Ref = match[0].Key
				if c.Label == "" {
					c.Label = match[0].Name()
				}
			default:
				return c, fmt.Errorf("session %q is ambiguous: %d sessions match", c.Ref, len(match))
			}
		}
	case store.KindOwcliWiki:
		if w.Owcli.Problem == "" {
			wk := w.Owcli.Wiki(c.Ref)
			if wk == nil {
				for i := range w.Owcli.Wikis {
					if w.Owcli.Wikis[i].Name == c.Ref {
						wk = &w.Owcli.Wikis[i]
						break
					}
				}
			}
			if wk == nil {
				return c, fmt.Errorf("owcli knows no wiki %q (see owcli wikis)", c.Ref)
			}
			c.Ref, c.Attrs["root"] = wk.ID, wk.RepoRoot
			if c.Label == "" {
				c.Label = wk.Name
			}
		}
	case store.KindOwcliWorkspace:
		if w.Owcli.Problem == "" {
			ws := w.Owcli.Workspace(c.Ref)
			if ws == nil {
				return c, fmt.Errorf("owcli knows no workspace %q", c.Ref)
			}
			c.Ref = ws.ID
			if c.Label == "" {
				c.Label = ws.Name
			}
		}
	}
	return c, nil
}
