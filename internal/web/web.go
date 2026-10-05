// Package web serves goatlassian's local web UI and its JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hoodoo/goatlassian/internal/config"
	"github.com/Hoodoo/goatlassian/internal/discover"
	"github.com/Hoodoo/goatlassian/internal/portfolio"
	"github.com/Hoodoo/goatlassian/internal/services"
	"github.com/Hoodoo/goatlassian/internal/sources"
	"github.com/Hoodoo/goatlassian/internal/store"
	"github.com/Hoodoo/goatlassian/internal/version"
)

//go:embed static
var static embed.FS

// worldTTL is how long one collection of the tools serves requests.
const worldTTL = 20 * time.Second

// Server is the web UI.
type Server struct {
	st     *store.Store
	cfg    config.Config
	home   string
	host   string // the listen host, accepted in Host headers besides loopback
	opts   Options
	runner sources.Runner
	mux    *http.ServeMux

	mu    sync.Mutex
	world *sources.World
}

// Options let the server run behind a reverse proxy.
type Options struct {
	// AllowHosts are further names accepted in the Host header, such as the
	// public name a proxy forwards.
	AllowHosts []string
	// UserHeader names a request header the proxy sets to the signed-in
	// viewer (X-Goog-Authenticated-User-Email behind Google IAP). The viewer
	// becomes the actor of every change the request makes, and requests
	// without the header are refused, so traffic that bypasses the proxy
	// fails closed. Only set it when nothing but the proxy can reach the
	// server, since anyone else could send the header.
	UserHeader string
}

// New builds the server. host is the address it listens on.
func New(st *store.Store, cfg config.Config, home, host string, opts Options) *Server {
	return NewWithRunner(st, cfg, home, host, opts, sources.Exec{})
}

// NewWithRunner builds the server with a custom tool runner (for tests).
func NewWithRunner(st *store.Store, cfg config.Config, home, host string, opts Options, r sources.Runner) *Server {
	s := &Server{st: st, cfg: cfg, home: home, host: host, opts: opts, runner: r, mux: http.NewServeMux()}
	sub, _ := fs.Sub(static, "static")
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("GET /api/meta", s.meta)
	s.mux.HandleFunc("GET /api/portfolio", s.portfolio)
	s.mux.HandleFunc("POST /api/projects", s.createProject)
	s.mux.HandleFunc("GET /api/projects/{slug}", s.project)
	s.mux.HandleFunc("PATCH /api/projects/{slug}", s.editProject)
	s.mux.HandleFunc("DELETE /api/projects/{slug}", s.deleteProject)
	s.mux.HandleFunc("POST /api/projects/{slug}/state", s.setState)
	s.mux.HandleFunc("POST /api/projects/{slug}/notes", s.addNote)
	s.mux.HandleFunc("POST /api/projects/{slug}/components", s.attach)
	s.mux.HandleFunc("DELETE /api/projects/{slug}/components/{id}", s.detach)
	s.mux.HandleFunc("GET /api/discover", s.discover)
	s.mux.HandleFunc("POST /api/adopt", s.adopt)
	s.mux.HandleFunc("GET /api/services", s.services)
	s.mux.HandleFunc("POST /api/services/start", s.startServices)
	return s
}

// ServeHTTP rejects requests addressed to a foreign host (DNS rebinding)
// and state changes that are not JSON (cross-site form posts), then routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if s.opts.UserHeader != "" && s.Viewer(r) == "" {
		http.Error(w, "no signed-in user", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
			http.Error(w, "send application/json", http.StatusUnsupportedMediaType)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || host == s.host {
		return true
	}
	for _, h := range s.opts.AllowHosts {
		if host == strings.ToLower(h) {
			return true
		}
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Viewer returns who the proxy says is signed in, or "" without a
// UserHeader. Google IAP prefixes the address with "accounts.google.com:".
func (s *Server) Viewer(r *http.Request) string {
	if s.opts.UserHeader == "" {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get(s.opts.UserHeader))
	if i := strings.LastIndex(v, ":"); i >= 0 {
		v = v[i+1:]
	}
	return v
}

// storeFor is the store a request writes through: changes are attributed
// to the signed-in viewer when there is one.
func (s *Server) storeFor(r *http.Request) *store.Store { return s.st.As(s.Viewer(r)) }

// World returns the cached collection of the tools, refreshing it when
// older than worldTTL or when fresh is set.
func (s *Server) World(fresh bool) *sources.World {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !fresh && s.world != nil && time.Since(s.world.CollectedAt) < worldTTL {
		return s.world
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.world = sources.Collect(ctx, s.cfg, s.runner)
	return s.world
}

// SnapshotLoop records a snapshot now if the last one is older than
// every, then every interval.
func (s *Server) SnapshotLoop(every time.Duration) {
	record := func() {
		pf, err := portfolio.Analyze(context.Background(), s.st, s.World(true), nil)
		if err == nil {
			err = portfolio.Record(s.st, pf)
		}
		if err != nil {
			log.Printf("snapshot: %v", err)
		}
	}
	if last, err := s.st.LastSnapshotAt(); err == nil && time.Since(last) >= every {
		record()
	}
	for range time.Tick(every) {
		record()
	}
}

// ---- helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var he httpError
	switch {
	case errors.As(err, &he):
		code = he.code
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	default:
		// Validation errors from the store and discover are user errors.
		msg := err.Error()
		for _, p := range []string{"invalid", "already exists", "unknown state", "needs a kind", "empty note", "knows no", "not a git", "ambiguous"} {
			if strings.Contains(msg, p) {
				code = http.StatusBadRequest
			}
		}
	}
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("bad JSON: %v", err)
	}
	return nil
}

// ---- handlers ----------------------------------------------------------

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"states":  store.States,
		"kinds":   store.KnownKinds,
		"config":  s.cfg,
		"actor":   s.storeFor(r).Actor,
	})
}

func (s *Server) portfolio(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1"
	keep := func(p *store.Project) bool { return all || p.State != store.StateArchived }
	pf, err := portfolio.Analyze(r.Context(), s.st, s.World(r.URL.Query().Get("refresh") == "1"), keep)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pf)
}

func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.Project(r.PathValue("slug"))
	if err != nil {
		writeErr(w, err)
		return
	}
	pf, err := portfolio.Analyze(r.Context(), s.st, s.World(r.URL.Query().Get("refresh") == "1"), func(x *store.Project) bool { return x.ID == p.ID })
	if err != nil {
		writeErr(w, err)
		return
	}
	evs, err := s.st.Events(p.ID, 200)
	if err != nil {
		writeErr(w, err)
		return
	}
	snaps, err := s.st.Snapshots(p.ID, time.Now().AddDate(0, 0, -180))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report": pf.Reports[0], "events": evs, "snapshots": snaps,
		"recent_days": pf.RecentDays, "collected_at": pf.CollectedAt, "tools": pf.Tools,
	})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.Slug == "" {
		in.Slug = store.Slugify(in.Name)
	}
	p, err := s.storeFor(r).CreateProject(in.Slug, in.Name, in.Description, in.Tags)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) editProject(w http.ResponseWriter, r *http.Request) {
	var e store.ProjectEdit
	if err := decode(r, &e); err != nil {
		writeErr(w, err)
		return
	}
	p, err := s.storeFor(r).EditProject(r.PathValue("slug"), e)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.storeFor(r).DeleteProject(r.PathValue("slug")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		State string `json:"state"`
		Why   string `json:"why"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	p, err := s.storeFor(r).SetState(r.PathValue("slug"), in.State, in.Why)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) addNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.storeFor(r).AddNote(r.PathValue("slug"), in.Text); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.Component
		Raw bool `json:"raw"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	c := in.Component
	if !in.Raw {
		var err error
		if c, err = discover.Normalize(r.Context(), s.World(false), c); err != nil {
			writeErr(w, err)
			return
		}
	}
	got, err := s.storeFor(r).Attach(r.PathValue("slug"), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) detach(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, badRequest("bad component id"))
		return
	}
	if err := s.storeFor(r).Detach(r.PathValue("slug"), id, "", ""); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	cs, err := discover.Discover(r.Context(), s.st, s.World(r.URL.Query().Get("refresh") == "1"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if cs == nil {
		cs = []*discover.Candidate{}
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) adopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root        string   `json:"root"`
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if in.Root == "" {
		writeErr(w, badRequest("root is required"))
		return
	}
	c, err := discover.ForDir(r.Context(), s.World(false), in.Root)
	if err != nil {
		writeErr(w, err)
		return
	}
	p, err := discover.Adopt(s.storeFor(r), c, in.Slug, in.Name, in.Description, in.Tags)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, services.Status(r.Context(), s.cfg))
}

func (s *Server) startServices(w http.ResponseWriter, r *http.Request) {
	st, err := services.StartMissing(r.Context(), s.cfg, s.home)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
