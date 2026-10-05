package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hoodoo/goatlassian/internal/config"
	"github.com/Hoodoo/goatlassian/internal/store"
	"github.com/Hoodoo/goatlassian/internal/testutil"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	return newServerWith(t, Options{})
}

func newServerWith(t *testing.T, opts Options) *Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := testutil.Runner{
		"kata daemon status --json":                     `{"daemons":[]}`,
		"kata projects list --json":                     `{"projects":[]}`,
		"kata list --all --status all --limit 0 --json": `{"issues":[]}`,
		"owcli wikis --json":                            `{"wikis":[],"workspaces":[]}`,
		"bossman --json ls -n 0":                        `[]`,
	}
	return NewWithRunner(st, config.Default(), t.TempDir(), "127.0.0.1", opts, r)
}

func do(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:7799"
	if body != "" || method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestProjectAPI(t *testing.T) {
	s := newServer(t)
	if code, out := do(t, s, "POST", "/api/projects", `{"name":"My Shop","tags":["web"]}`); code != 201 || out["slug"] != "my-shop" {
		t.Fatalf("create: %d %v", code, out)
	}
	if code, _ := do(t, s, "POST", "/api/projects", `{"slug":"my-shop"}`); code != 400 {
		t.Fatalf("duplicate: %d", code)
	}
	if code, out := do(t, s, "POST", "/api/projects/my-shop/components", `{"kind":"link","ref":"https://x/pr/1","label":"PR"}`); code != 201 || out["kind"] != "link" {
		t.Fatalf("attach: %d %v", code, out)
	}
	if code, out := do(t, s, "POST", "/api/projects/my-shop/components", `{"kind":"kata","ref":"nope"}`); code != 400 {
		t.Fatalf("attach unknown kata: %d %v", code, out)
	}
	if code, _ := do(t, s, "POST", "/api/projects/my-shop/state", `{"state":"paused","why":"later"}`); code != 200 {
		t.Fatalf("state: %d", code)
	}
	if code, _ := do(t, s, "POST", "/api/projects/my-shop/notes", `{"text":"hi"}`); code != 201 {
		t.Fatalf("note: %d", code)
	}
	code, out := do(t, s, "GET", "/api/projects/my-shop", "")
	if code != 200 {
		t.Fatalf("get: %d %v", code, out)
	}
	rep := out["report"].(map[string]any)
	if rep["project"].(map[string]any)["state"] != "paused" || len(rep["components"].([]any)) != 1 || len(out["events"].([]any)) != 4 || rep["flags"] == nil {
		t.Fatalf("project: %v", out)
	}
	if code, out = do(t, s, "GET", "/api/portfolio", ""); code != 200 || len(out["reports"].([]any)) != 1 {
		t.Fatalf("portfolio: %d %v", code, out)
	}
	if code, _ = do(t, s, "PATCH", "/api/projects/my-shop", `{"slug":"shop"}`); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if code, _ = do(t, s, "DELETE", "/api/projects/shop", ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = do(t, s, "GET", "/api/projects/shop", ""); code != 404 {
		t.Fatalf("after delete: %d", code)
	}
}

func TestGuards(t *testing.T) {
	s := newServer(t)
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader("slug=x"))
	req.Host = "127.0.0.1:7799"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/api/meta", nil)
	req.Host = "evil.example:7799"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost:7799"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "goatlassian") {
		t.Fatalf("index: %d", rec.Code)
	}
}

// TestBehindProxy: behind Google IAP the public name is accepted, requests
// without the user header are refused, and the signed-in user is the actor
// of the changes they make.
func TestBehindProxy(t *testing.T) {
	s := newServerWith(t, Options{AllowHosts: []string{"Portfolio.Example.com"}, UserHeader: "X-Goog-Authenticated-User-Email"})
	req := func(method, host, path, body, user string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = host
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if user != "" {
			r.Header.Set("X-Goog-Authenticated-User-Email", "accounts.google.com:"+user)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := req("GET", "portfolio.example.com", "/api/meta", "", "alice@example.com"); code != 200 || out["actor"] != "alice@example.com" {
		t.Fatalf("meta: %d %v", code, out["actor"])
	}
	if code, _ := req("GET", "portfolio.example.com", "/api/meta", "", ""); code != http.StatusUnauthorized {
		t.Errorf("no user header: %d", code)
	}
	if code, _ := req("GET", "evil.example", "/api/meta", "", "alice@example.com"); code != http.StatusForbidden {
		t.Errorf("unknown host: %d", code)
	}
	if code, _ := req("POST", "portfolio.example.com", "/api/projects", `{"slug":"shop"}`, "alice@example.com"); code != 201 && code != 200 {
		t.Fatalf("create: %d", code)
	}
	if code, _ := req("POST", "portfolio.example.com", "/api/projects/shop/notes", `{"text":"hi"}`, "bob@example.com"); code >= 300 {
		t.Fatalf("note: %d", code)
	}
	p, err := s.st.Project("shop")
	if err != nil {
		t.Fatal(err)
	}
	evs, err := s.st.Events(p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	actors := map[string]string{}
	for _, e := range evs {
		actors[e.Kind] = e.Actor
	}
	if actors["created"] != "alice@example.com" || actors["note"] != "bob@example.com" {
		t.Errorf("event actors %v", actors)
	}
	if s.st.Actor == "alice@example.com" || s.st.Actor == "bob@example.com" {
		t.Error("a request changed the shared store's actor")
	}
}
