package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// EventRelocate records a path rewritten by Relocate in a project's log.
const EventRelocate = "relocate"

// PathChange is one component path Relocate rewrites, or one sessions
// component it adds.
type PathChange struct {
	Project     string `json:"project"`
	ComponentID int64  `json:"component_id"`
	Kind        string `json:"kind"`
	Field       string `json:"field"` // "ref", "attrs.root", or "added" (a new sessions component)
	From        string `json:"from"`
	To          string `json:"to"`
}

// underPrefix reports whether path is prefix or inside it, and returns path
// with prefix replaced by repl.
func underPrefix(path, prefix, repl string) (string, bool) {
	if path == prefix {
		return repl, true
	}
	if rest, ok := strings.CutPrefix(path, prefix+string(filepath.Separator)); ok {
		return filepath.Join(repl, rest), true
	}
	return "", false
}

// RelocatePrefixes makes both prefixes absolute and clean, resolving the new
// one through symlinks when it exists (git refs are canonical repository
// roots); the old one usually no longer exists.
func RelocatePrefixes(oldPrefix, newPrefix string) (string, string, error) {
	from, err := filepath.Abs(oldPrefix)
	if err != nil {
		return "", "", err
	}
	to, err := filepath.Abs(newPrefix)
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(to); err == nil {
		to = resolved
	}
	if from == to {
		return "", "", fmt.Errorf("old and new path are both %s", from)
	}
	return from, to, nil
}

// Relocate rewrites component paths at or under oldPrefix to the same path
// under newPrefix, in every project including archived ones, after
// repositories moved:
//
//   - git refs are rewritten;
//   - owcli-wiki components get their attrs.root rewritten (their ID is
//     owcli's to change, and they resolve through the root);
//   - a sessions component keeps its old directory, because bossman records
//     where each past session ran, and gains a sibling for the new directory
//     so future sessions count too.
//
// Each change is logged in its project. Nothing is written with dryRun, and
// nothing at all when a rewritten git ref would collide with one the project
// already has.
func (s *Store) Relocate(oldPrefix, newPrefix string, dryRun bool) ([]PathChange, error) {
	from, to, err := RelocatePrefixes(oldPrefix, newPrefix)
	if err != nil {
		return nil, err
	}
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}
	slugs := map[int64]string{}
	for _, p := range projects {
		slugs[p.ID] = p.Slug
	}
	comps, err := s.Components(0)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{} // project/kind/ref already attached
	key := func(project int64, kind, ref string) string { return fmt.Sprintf("%d\x00%s\x00%s", project, kind, ref) }
	for _, c := range comps {
		have[key(c.ProjectID, c.Kind, c.Ref)] = true
	}

	changes := []PathChange{}
	for _, c := range comps {
		slug := slugs[c.ProjectID]
		switch c.Kind {
		case KindGit:
			if moved, ok := underPrefix(c.Ref, from, to); ok {
				if have[key(c.ProjectID, c.Kind, moved)] {
					return nil, fmt.Errorf("project %s already has git %s; detach one of them first", slug, moved)
				}
				changes = append(changes, PathChange{slug, c.ID, c.Kind, "ref", c.Ref, moved})
			}
		case KindSessions:
			if moved, ok := underPrefix(c.Ref, from, to); ok && !have[key(c.ProjectID, c.Kind, moved)] {
				have[key(c.ProjectID, c.Kind, moved)] = true
				changes = append(changes, PathChange{slug, c.ID, c.Kind, "added", c.Ref, moved})
			}
		case KindOwcliWiki:
			if moved, ok := underPrefix(c.Attrs["root"], from, to); ok && c.Attrs["root"] != "" {
				changes = append(changes, PathChange{slug, c.ID, c.Kind, "attrs.root", c.Attrs["root"], moved})
			}
		}
	}
	if dryRun || len(changes) == 0 {
		return changes, nil
	}

	byID := map[int64]Component{}
	for _, c := range comps {
		byID[c.ID] = c
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	touched := map[int64]bool{}
	for _, ch := range changes {
		c := byID[ch.ComponentID]
		switch ch.Field {
		case "ref":
			_, err = tx.Exec(`UPDATE components SET ref = ? WHERE id = ?`, ch.To, c.ID)
		case "attrs.root":
			attrs := map[string]string{}
			for k, v := range c.Attrs {
				attrs[k] = v
			}
			attrs["root"] = ch.To
			data, _ := json.Marshal(attrs)
			_, err = tx.Exec(`UPDATE components SET attrs = ? WHERE id = ?`, string(data), c.ID)
		case "added":
			data, _ := json.Marshal(c.Attrs)
			_, err = tx.Exec(`INSERT INTO components(project_id, kind, ref, label, attrs, created_at) VALUES (?,?,?,?,?,?)`,
				c.ProjectID, c.Kind, ch.To, c.Label, string(data), ts(now()))
		}
		if err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("%s %s: %s -> %s", ch.Kind, ch.Field, ch.From, ch.To)
		if ch.Field == "added" {
			msg = fmt.Sprintf("sessions %s added next to %s", ch.To, ch.From)
		}
		if err := addEvent(tx, c.ProjectID, s.Actor, EventRelocate, msg); err != nil {
			return nil, err
		}
		touched[c.ProjectID] = true
	}
	for id := range touched {
		if _, err := tx.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, ts(now()), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changes, nil
}
