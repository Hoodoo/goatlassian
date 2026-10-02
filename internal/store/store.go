// Package store keeps goatlassian's own records in SQLite: projects, the
// components attached to them, an event log, and metric snapshots. It
// never stores what the sibling tools own (issues, wiki pages, sessions);
// components only point at those.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Lifecycle states of a project.
const (
	StateActive   = "active"
	StatePaused   = "paused"
	StateDone     = "done"
	StateArchived = "archived"
)

// States lists the lifecycle states in display order.
var States = []string{StateActive, StatePaused, StateDone, StateArchived}

// ValidState reports whether s is a lifecycle state.
func ValidState(s string) bool {
	for _, v := range States {
		if v == s {
			return true
		}
	}
	return false
}

// Component kinds goatlassian understands. Kind is free text, so other
// artifacts (a Slack log, an email thread, a PR) can be attached today and
// gain an adapter later.
const (
	KindGit            = "git"             // ref: absolute repository path
	KindKata           = "kata"            // ref: kata project name; attrs.uid pins it
	KindOwcliWiki      = "owcli-wiki"      // ref: owcli wiki ID
	KindOwcliWorkspace = "owcli-workspace" // ref: owcli workspace ID
	KindSessions       = "sessions"        // ref: directory; sessions started in or under it
	KindSession        = "session"         // ref: one bossman session key (agent:id); overrides directories
	KindLink           = "link"            // ref: URL
)

// KnownKinds are the kinds with an adapter.
var KnownKinds = []string{KindGit, KindKata, KindOwcliWiki, KindOwcliWorkspace, KindSessions, KindSession, KindLink}

// ErrNotFound is returned when a project or component does not exist.
var ErrNotFound = errors.New("not found")

// Project is a group of components with a lifecycle state.
type Project struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Component points at an artifact owned by another tool.
type Component struct {
	ID        int64             `json:"id"`
	ProjectID int64             `json:"project_id"`
	Kind      string            `json:"kind"`
	Ref       string            `json:"ref"`
	Label     string            `json:"label"`
	Attrs     map[string]string `json:"attrs"`
	CreatedAt time.Time         `json:"created_at"`
}

// Event is one entry of a project's log: lifecycle changes, notes, and
// attach/detach audit records.
type Event struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
}

// Event kinds.
const (
	EventCreated  = "created"
	EventEdited   = "edited"
	EventState    = "state"
	EventNote     = "note"
	EventAttach   = "attach"
	EventDetach   = "detach"
	EventSnapshot = "snapshot"
)

// Snapshot is a recorded set of a project's metrics.
type Snapshot struct {
	At   time.Time       `json:"at"`
	Data json.RawMessage `json:"data"`
}

// Store is an open goatlassian database.
type Store struct {
	db    *sql.DB
	Path  string
	Actor string
}

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id          INTEGER PRIMARY KEY,
	slug        TEXT NOT NULL UNIQUE,
	name        TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	state       TEXT NOT NULL DEFAULT 'active',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_tags (
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	tag        TEXT NOT NULL,
	PRIMARY KEY (project_id, tag)
);
CREATE TABLE IF NOT EXISTS components (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL,
	ref        TEXT NOT NULL,
	label      TEXT NOT NULL DEFAULT '',
	attrs      TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	UNIQUE (project_id, kind, ref)
);
CREATE INDEX IF NOT EXISTS components_kind_ref ON components(kind, ref);
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	at         TEXT NOT NULL,
	actor      TEXT NOT NULL DEFAULT '',
	kind       TEXT NOT NULL,
	message    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS events_project_at ON events(project_id, at);
CREATE TABLE IF NOT EXISTS snapshots (
	id         INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	at         TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS snapshots_project_at ON snapshots(project_id, at);
`

// Open opens (creating if needed) home/goatlassian.db.
func Open(home string) (*Store, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(home, "goatlassian.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{db: db, Path: path, Actor: DefaultActor()}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DefaultActor names who is making changes: $GOATLASSIAN_ACTOR,
// $KATA_AUTHOR, or $USER.
func DefaultActor() string {
	for _, k := range []string{"GOATLASSIAN_ACTOR", "KATA_AUTHOR", "USER"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "anonymous"
}

func now() time.Time { return time.Now().UTC() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Slugify turns a name into a slug: lower case, runs of other characters
// collapsed to "-".
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-.")
}

// ValidSlug reports whether s can be a project slug.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

func normTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "#")))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// CreateProject adds a project. Name defaults to the slug.
func (s *Store) CreateProject(slug, name, description string, tags []string) (*Project, error) {
	if !ValidSlug(slug) {
		return nil, fmt.Errorf("invalid slug %q: use lower-case letters, digits, '.', '_' and '-'", slug)
	}
	if name == "" {
		name = slug
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t := now()
	res, err := tx.Exec(`INSERT INTO projects(slug, name, description, state, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		slug, name, description, StateActive, ts(t), ts(t))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("project %q already exists", slug)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := setTags(tx, id, tags); err != nil {
		return nil, err
	}
	if err := addEvent(tx, id, s.Actor, EventCreated, name); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Project(slug)
}

func setTags(tx *sql.Tx, id int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM project_tags WHERE project_id = ?`, id); err != nil {
		return err
	}
	for _, t := range normTags(tags) {
		if _, err := tx.Exec(`INSERT INTO project_tags(project_id, tag) VALUES (?,?)`, id, t); err != nil {
			return err
		}
	}
	return nil
}

func addEvent(tx *sql.Tx, id int64, actor, kind, msg string) error {
	_, err := tx.Exec(`INSERT INTO events(project_id, at, actor, kind, message) VALUES (?,?,?,?,?)`,
		id, ts(now()), actor, kind, msg)
	return err
}

const projectCols = `id, slug, name, description, state, created_at, updated_at`

func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var c, u string
	if err := sc.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.State, &c, &u); err != nil {
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = parseTS(c), parseTS(u)
	p.Tags = []string{}
	return &p, nil
}

// Project looks a project up by slug.
func (s *Store) Project(slug string) (*Project, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("project %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	tags, err := s.tags()
	if err != nil {
		return nil, err
	}
	if t := tags[p.ID]; t != nil {
		p.Tags = t
	}
	return p, nil
}

// Projects lists every project ordered by slug.
func (s *Store) Projects() ([]*Project, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tags, err := s.tags()
	if err != nil {
		return nil, err
	}
	for _, p := range out {
		if t := tags[p.ID]; t != nil {
			p.Tags = t
		}
	}
	return out, nil
}

func (s *Store) tags() (map[int64][]string, error) {
	rows, err := s.db.Query(`SELECT project_id, tag FROM project_tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// ProjectEdit holds the fields to change; nil leaves a field alone. Tags,
// when non-nil, replaces the tag set before AddTags and RemoveTags apply.
type ProjectEdit struct {
	Slug        *string  `json:"slug,omitempty"`
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	AddTags     []string `json:"add_tags,omitempty"`
	RemoveTags  []string `json:"remove_tags,omitempty"`
}

// EditProject applies e and logs what changed.
func (s *Store) EditProject(slug string, e ProjectEdit) (*Project, error) {
	p, err := s.Project(slug)
	if err != nil {
		return nil, err
	}
	var changes []string
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	set := func(col string, v string) error {
		_, err := tx.Exec(`UPDATE projects SET `+col+` = ? WHERE id = ?`, v, p.ID)
		return err
	}
	if e.Slug != nil && *e.Slug != p.Slug {
		if !ValidSlug(*e.Slug) {
			return nil, fmt.Errorf("invalid slug %q", *e.Slug)
		}
		if err := set("slug", *e.Slug); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, fmt.Errorf("project %q already exists", *e.Slug)
			}
			return nil, err
		}
		changes = append(changes, fmt.Sprintf("slug %s → %s", p.Slug, *e.Slug))
		slug = *e.Slug
	}
	if e.Name != nil && *e.Name != p.Name && *e.Name != "" {
		if err := set("name", *e.Name); err != nil {
			return nil, err
		}
		changes = append(changes, fmt.Sprintf("name → %s", *e.Name))
	}
	if e.Description != nil && *e.Description != p.Description {
		if err := set("description", *e.Description); err != nil {
			return nil, err
		}
		changes = append(changes, "description")
	}
	tags := p.Tags
	if e.Tags != nil {
		tags = e.Tags
	}
	tags = append(append([]string{}, tags...), e.AddTags...)
	if len(e.RemoveTags) > 0 {
		rm := map[string]bool{}
		for _, t := range normTags(e.RemoveTags) {
			rm[t] = true
		}
		var keep []string
		for _, t := range normTags(tags) {
			if !rm[t] {
				keep = append(keep, t)
			}
		}
		tags = keep
	}
	if nt := normTags(tags); strings.Join(nt, ",") != strings.Join(p.Tags, ",") {
		if err := setTags(tx, p.ID, nt); err != nil {
			return nil, err
		}
		changes = append(changes, "tags → "+strings.Join(nt, ", "))
	}
	if len(changes) > 0 {
		if err := set("updated_at", ts(now())); err != nil {
			return nil, err
		}
		if err := addEvent(tx, p.ID, s.Actor, EventEdited, strings.Join(changes, "; ")); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Project(slug)
}

// SetState moves a project to a lifecycle state, logging why.
func (s *Store) SetState(slug, state, why string) (*Project, error) {
	if !ValidState(state) {
		return nil, fmt.Errorf("unknown state %q (want one of %s)", state, strings.Join(States, ", "))
	}
	p, err := s.Project(slug)
	if err != nil {
		return nil, err
	}
	if p.State == state {
		return p, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE projects SET state = ?, updated_at = ? WHERE id = ?`, state, ts(now()), p.ID); err != nil {
		return nil, err
	}
	msg := p.State + " → " + state
	if why != "" {
		msg += ": " + why
	}
	if err := addEvent(tx, p.ID, s.Actor, EventState, msg); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Project(slug)
}

// DeleteProject removes a project with its components, events, and
// snapshots. The artifacts the components point at are untouched.
func (s *Store) DeleteProject(slug string) error {
	res, err := s.db.Exec(`DELETE FROM projects WHERE slug = ?`, slug)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q: %w", slug, ErrNotFound)
	}
	return nil
}

// AddNote appends a note to a project's log.
func (s *Store) AddNote(slug, text string) error {
	p, err := s.Project(slug)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("empty note")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addEvent(tx, p.ID, s.Actor, EventNote, text); err != nil {
		return err
	}
	return tx.Commit()
}

// Events returns a project's log, newest first; limit 0 means all.
func (s *Store) Events(projectID int64, limit int) ([]Event, error) {
	q := `SELECT id, project_id, at, actor, kind, message FROM events WHERE project_id = ? ORDER BY at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Query(q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.ID, &e.ProjectID, &at, &e.Actor, &e.Kind, &e.Message); err != nil {
			return nil, err
		}
		e.At = parseTS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEvents returns each project's latest event time, keyed by project ID.
func (s *Store) LastEvents() (map[int64]time.Time, error) {
	rows, err := s.db.Query(`SELECT project_id, MAX(at) FROM events WHERE kind != ? GROUP BY project_id`, EventSnapshot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = parseTS(at)
	}
	return out, rows.Err()
}

// Attach adds a component to a project. Attaching the same kind and ref
// twice updates the label and attributes.
func (s *Store) Attach(slug string, c Component) (*Component, error) {
	p, err := s.Project(slug)
	if err != nil {
		return nil, err
	}
	c.Kind = strings.TrimSpace(c.Kind)
	c.Ref = strings.TrimSpace(c.Ref)
	if c.Kind == "" || c.Ref == "" {
		return nil, errors.New("a component needs a kind and a ref")
	}
	if c.Attrs == nil {
		c.Attrs = map[string]string{}
	}
	attrs, _ := json.Marshal(c.Attrs)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO components(project_id, kind, ref, label, attrs, created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(project_id, kind, ref) DO UPDATE SET label = excluded.label, attrs = excluded.attrs`,
		p.ID, c.Kind, c.Ref, c.Label, string(attrs), ts(now()))
	if err != nil {
		return nil, err
	}
	if err := addEvent(tx, p.ID, s.Actor, EventAttach, c.Kind+" "+c.Ref); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, ts(now()), p.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	cs, err := s.Components(p.ID)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		if cs[i].Kind == c.Kind && cs[i].Ref == c.Ref {
			return &cs[i], nil
		}
	}
	return nil, ErrNotFound
}

// Detach removes a component by ID, or by kind and ref when id is 0.
func (s *Store) Detach(slug string, id int64, kind, ref string) error {
	p, err := s.Project(slug)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var k, r string
	if id != 0 {
		err = tx.QueryRow(`SELECT kind, ref FROM components WHERE id = ? AND project_id = ?`, id, p.ID).Scan(&k, &r)
	} else {
		err = tx.QueryRow(`SELECT id, kind, ref FROM components WHERE project_id = ? AND kind = ? AND ref = ?`, p.ID, kind, ref).Scan(&id, &k, &r)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("component: %w", ErrNotFound)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM components WHERE id = ?`, id); err != nil {
		return err
	}
	if err := addEvent(tx, p.ID, s.Actor, EventDetach, k+" "+r); err != nil {
		return err
	}
	return tx.Commit()
}

// Components lists a project's components; projectID 0 lists all.
func (s *Store) Components(projectID int64) ([]Component, error) {
	q := `SELECT id, project_id, kind, ref, label, attrs, created_at FROM components`
	var args []any
	if projectID != 0 {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY project_id, kind, ref`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Component{}
	for rows.Next() {
		var c Component
		var attrs, at string
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Kind, &c.Ref, &c.Label, &attrs, &at); err != nil {
			return nil, err
		}
		c.Attrs = map[string]string{}
		_ = json.Unmarshal([]byte(attrs), &c.Attrs)
		c.CreatedAt = parseTS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddSnapshot records a project's metrics.
func (s *Store) AddSnapshot(projectID int64, at time.Time, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO snapshots(project_id, at, data) VALUES (?,?,?)`, projectID, ts(at), string(b))
	return err
}

// LastSnapshotAt returns when any snapshot was last recorded.
func (s *Store) LastSnapshotAt() (time.Time, error) {
	var at sql.NullString
	if err := s.db.QueryRow(`SELECT MAX(at) FROM snapshots`).Scan(&at); err != nil {
		return time.Time{}, err
	}
	return parseTS(at.String), nil
}

// Snapshots returns a project's snapshots since a time, oldest first.
func (s *Store) Snapshots(projectID int64, since time.Time) ([]Snapshot, error) {
	rows, err := s.db.Query(`SELECT at, data FROM snapshots WHERE project_id = ? AND at >= ? ORDER BY at`, projectID, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var at, data string
		if err := rows.Scan(&at, &data); err != nil {
			return nil, err
		}
		out = append(out, Snapshot{At: parseTS(at), Data: json.RawMessage(data)})
	}
	return out, rows.Err()
}
