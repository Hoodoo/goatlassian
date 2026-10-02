package store

import (
	"errors"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Actor = "tester"
	t.Cleanup(func() { st.Close() })
	return st
}

func TestProjectLifecycle(t *testing.T) {
	st := open(t)
	p, err := st.CreateProject("shop", "Shop", "a shop", []string{"Web", "#web", "go"})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != StateActive || p.Name != "Shop" || len(p.Tags) != 2 || p.Tags[0] != "go" || p.Tags[1] != "web" {
		t.Fatalf("created %+v", p)
	}
	if _, err := st.CreateProject("shop", "", "", nil); err == nil {
		t.Fatal("duplicate slug accepted")
	}
	if _, err := st.CreateProject("Bad Slug", "", "", nil); err == nil {
		t.Fatal("invalid slug accepted")
	}
	if _, err := st.SetState("shop", "frozen", ""); err == nil {
		t.Fatal("unknown state accepted")
	}
	if p, err = st.SetState("shop", StatePaused, "waiting on design"); err != nil || p.State != StatePaused {
		t.Fatalf("set state: %v %+v", err, p)
	}
	newSlug, name := "store", "Store"
	if p, err = st.EditProject("shop", ProjectEdit{Slug: &newSlug, Name: &name, AddTags: []string{"x"}, RemoveTags: []string{"go"}}); err != nil {
		t.Fatal(err)
	}
	if p.Slug != "store" || p.Name != "Store" || len(p.Tags) != 2 || p.Tags[0] != "web" || p.Tags[1] != "x" {
		t.Fatalf("edited %+v", p)
	}
	if err := st.AddNote("store", "hello"); err != nil {
		t.Fatal(err)
	}
	evs, err := st.Events(p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Actor != "tester" {
			t.Errorf("actor %q", e.Actor)
		}
	}
	if len(kinds) != 4 || kinds[0] != EventNote || kinds[3] != EventCreated {
		t.Fatalf("events %v", kinds)
	}
	if err := st.DeleteProject("store"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Project("store"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestComponents(t *testing.T) {
	st := open(t)
	if _, err := st.CreateProject("a", "", "", nil); err != nil {
		t.Fatal(err)
	}
	c, err := st.Attach("a", Component{Kind: KindLink, Ref: "https://x/pr/1", Label: "PR"})
	if err != nil {
		t.Fatal(err)
	}
	// Re-attaching updates rather than duplicating.
	if _, err := st.Attach("a", Component{Kind: KindLink, Ref: "https://x/pr/1", Label: "PR 1", Attrs: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	// Unknown kinds are allowed: future artifacts are not locked out.
	if _, err := st.Attach("a", Component{Kind: "slack", Ref: "https://slack/thread"}); err != nil {
		t.Fatal(err)
	}
	cs, err := st.Components(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Label != "PR 1" || cs[0].Attrs["k"] != "v" {
		t.Fatalf("components %+v", cs)
	}
	if err := st.Detach("a", c.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Detach("a", 0, "slack", "https://slack/thread"); err != nil {
		t.Fatal(err)
	}
	if err := st.Detach("a", 0, "slack", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detach missing: %v", err)
	}
	if cs, _ = st.Components(0); len(cs) != 0 {
		t.Fatalf("left %+v", cs)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"My Project!": "my-project", "habitron-9000": "habitron-9000", ".emacs.d": "emacs.d", "a__b": "a__b"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
