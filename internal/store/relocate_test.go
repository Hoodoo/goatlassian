package store

import (
	"strings"
	"testing"
)

func TestRelocate(t *testing.T) {
	st := open(t)
	for _, slug := range []string{"shop", "old", "other"} {
		if _, err := st.CreateProject(slug, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	attach := func(slug string, c Component) {
		t.Helper()
		if _, err := st.Attach(slug, c); err != nil {
			t.Fatal(err)
		}
	}
	attach("shop", Component{Kind: KindGit, Ref: "/src/shop"})
	attach("shop", Component{Kind: KindSessions, Ref: "/src/shop", Attrs: map[string]string{"agent": "codex"}})
	attach("shop", Component{Kind: KindOwcliWiki, Ref: "shop-1a2b", Attrs: map[string]string{"root": "/src/shop"}})
	attach("shop", Component{Kind: KindLink, Ref: "/src/shop/notes"}) // not a path kind
	attach("old", Component{Kind: KindGit, Ref: "/src"})               // the prefix itself
	attach("other", Component{Kind: KindGit, Ref: "/srcx/other"})      // shares a string prefix only
	if _, err := st.SetState("old", StateArchived, ""); err != nil {
		t.Fatal(err)
	}

	changes, err := st.Relocate("/src", "/work", true)
	if err != nil || len(changes) != 4 {
		t.Fatalf("dry run: %+v, %v", changes, err)
	}
	for _, c := range mustComponents(t, st) {
		if strings.HasPrefix(c.Ref, "/work") || c.Attrs["root"] == "/work/shop" {
			t.Fatalf("dry run wrote %+v", c)
		}
	}

	changes, err = st.Relocate("/src", "/work", false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.Project+" "+c.Kind+" "+c.Field+" "+c.To)
	}
	want := "shop git ref /work/shop,shop owcli-wiki attrs.root /work/shop,shop sessions added /work/shop,old git ref /work"
	if strings.Join(got, ",") != want {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, ","), want)
	}

	refs := map[string]Component{}
	for _, c := range mustComponents(t, st) {
		refs[c.Kind+" "+c.Ref] = c
	}
	for _, k := range []string{"git /work/shop", "git /work", "sessions /src/shop", "sessions /work/shop", "link /src/shop/notes", "git /srcx/other"} {
		if _, ok := refs[k]; !ok {
			t.Errorf("missing component %s", k)
		}
	}
	if a := refs["sessions /work/shop"].Attrs["agent"]; a != "codex" {
		t.Errorf("new sessions component lost attrs: %q", a)
	}
	if r := refs["owcli-wiki shop-1a2b"].Attrs["root"]; r != "/work/shop" {
		t.Errorf("wiki root %q", r)
	}
	p, _ := st.Project("shop")
	evs, _ := st.Events(p.ID, 0)
	n := 0
	for _, e := range evs {
		if e.Kind == EventRelocate {
			n++
		}
	}
	if n != 3 {
		t.Errorf("shop has %d relocate events, want 3", n)
	}

	if changes, err := st.Relocate("/src", "/work", false); err != nil || len(changes) != 0 {
		t.Errorf("second relocate: %+v, %v", changes, err)
	}
}

func mustComponents(t *testing.T, st *Store) []Component {
	t.Helper()
	cs, err := st.Components(0)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestRelocateRefusesGitCollision(t *testing.T) {
	st := open(t)
	if _, err := st.CreateProject("a", "", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"/old/a", "/new/a"} {
		if _, err := st.Attach("a", Component{Kind: KindGit, Ref: r}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Relocate("/old", "/new", false); err == nil || !strings.Contains(err.Error(), "already has git") {
		t.Fatalf("want collision, got %v", err)
	}
	if _, err := st.Relocate("/x", "/x", false); err == nil {
		t.Fatal("same prefix accepted")
	}
}
