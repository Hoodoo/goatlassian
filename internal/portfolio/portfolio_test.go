package portfolio_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hoodoo/goatlassian/internal/config"
	"github.com/Hoodoo/goatlassian/internal/discover"
	"github.com/Hoodoo/goatlassian/internal/portfolio"
	"github.com/Hoodoo/goatlassian/internal/sources"
	"github.com/Hoodoo/goatlassian/internal/store"
	"github.com/Hoodoo/goatlassian/internal/testutil"
)

func fakeWorld(t *testing.T) *sources.World {
	t.Helper()
	return sources.Collect(context.Background(), config.Default(), fakeRunner())
}

func fakeRunner() testutil.Runner {
	now := time.Now().UTC()
	day := func(n int) string { return now.AddDate(0, 0, -n).Format(time.RFC3339) }
	r := testutil.Runner{
		"kata daemon status --json":       `{"daemons":[{"web_url":"http://kata.test"}]}`,
		"kata projects list --json":       `{"projects":[{"id":1,"uid":"U1","name":"shop","active":true},{"id":2,"uid":"U2","name":"other","active":true}]}`,
		"kata projects show shop --json":  `{"aliases":[{"alias_identity":"local:///src/shop","alias_kind":"local"}]}`,
		"kata projects show other --json": `{"aliases":[{"alias_identity":"github.com/me/other","alias_kind":"git"}]}`,
		"kata list --all --status all --limit 0 --json": `{"issues":[
			{"uid":"I1","qualified_id":"shop#a","title":"stuck one","status":"open","project_uid":"U1","metadata":{"work.attention":"stuck","work.attention_msg":"no creds"},"updated_at":"` + day(1) + `","created_at":"` + day(9) + `","web_url":"http://kata.test/kata?issue=I1"},
			{"uid":"I2","qualified_id":"shop#b","title":"ask","status":"open","project_uid":"U1","metadata":{"work.attention":"needs-human"},"updated_at":"` + day(2) + `","created_at":"` + day(9) + `"},
			{"uid":"I3","qualified_id":"shop#c","title":"late","status":"open","project_uid":"U1","deadline_on":"2020-01-01","updated_at":"` + day(3) + `","created_at":"` + day(9) + `"},
			{"uid":"I4","qualified_id":"shop#d","title":"done","status":"closed","project_uid":"U1","closed_at":"` + day(1) + `","updated_at":"` + day(1) + `","created_at":"` + day(9) + `"},
			{"uid":"I5","qualified_id":"other#e","title":"old","status":"open","project_uid":"U2","updated_at":"` + day(40) + `","created_at":"` + day(60) + `"}]}`,
		"owcli wikis --json": `{"wikis":[{"id":"shop-1","name":"shop","repoRoot":"/src/shop","wikiDir":"/src/shop/openwiki","bound":true,"workspaces":[],"lastUpdate":{"updatedAt":"` + day(5) + `","command":"update","gitHead":"abc","status":"complete"}}],"workspaces":[]}`,
		"bossman --json ls -n 0": `[
			{"key":"claude:s1","agent":"claude","id":"s1","project":"/src/shop","started_at":"` + day(2) + `","ended_at":"` + day(2) + `","cost_usd":10,"input":100,"output":50},
			{"key":"codex:s2","agent":"codex","id":"s2","project":"/src/shop/nested/x","started_at":"` + day(3) + `","ended_at":"` + day(3) + `","cost_usd":5},
			{"key":"claude:s3","agent":"claude","id":"s3","project":"/src/other","started_at":"` + day(50) + `","ended_at":"` + day(50) + `","cost_usd":2},
			{"key":"claude:s4","agent":"claude","id":"s4","project":"/src/shop","started_at":"` + day(1) + `","ended_at":"` + day(1) + `","cost_usd":7},
			{"key":"claude:s5","agent":"claude","id":"s5","project":"/elsewhere","started_at":"` + day(1) + `","cost_usd":1}]`,
	}
	for _, repo := range []string{"/src/shop", "/src/shop/nested", "/src/other"} {
		r["git -C "+repo+" rev-parse --show-toplevel"] = repo
		r["git -C "+repo+" *"] = ""
		r["git -C "+repo+" branch --show-current"] = "main"
		r["git -C "+repo+" log -1 --format=%H%x00%cI%x00%s"] = "deadbeef\x00" + day(40) + "\x00old commit"
	}
	r["git -C /src/shop/nested/x rev-parse --show-toplevel"] = "/src/shop/nested"
	r["git -C /src/other remote get-url origin"] = "git@github.com:me/other.git"
	r["git -C /src/shop status --porcelain"] = " M a.go\n?? b.go"
	r["git -C /src/shop rev-list --count abc..HEAD"] = "12"
	return r
}

// TestWikiIDChange: owcli renames a wiki's ID when its repository joins a
// workspace (hash form to registry slug); the component still resolves
// through the repository root recorded at adopt time.
func TestWikiIDChange(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := discover.ForDir(ctx, fakeWorld(t), "/src/shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discover.Adopt(st, c, "", "", "", nil); err != nil {
		t.Fatal(err)
	}

	r := fakeRunner()
	r["owcli wikis --json"] = strings.Replace(r["owcli wikis --json"], `"id":"shop-1"`, `"id":"shop"`, 1)
	w := sources.Collect(ctx, config.Default(), r)
	pf, err := portfolio.Analyze(ctx, st, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cr := range pf.Reports[0].Components {
		if cr.Kind == store.KindOwcliWiki && cr.Problem != "" {
			t.Fatalf("wiki component after ID change: %s", cr.Problem)
		}
	}
	if pf.Reports[0].Metrics.WikiDrift != 12 {
		t.Errorf("wiki drift %d", pf.Reports[0].Metrics.WikiDrift)
	}

	n, err := discover.Normalize(ctx, w, store.Component{Kind: store.KindOwcliWiki, Ref: "shop"})
	if err != nil || n.Attrs["root"] != "/src/shop" {
		t.Errorf("Normalize must pin the root: %+v, %v", n, err)
	}
}

func TestDiscoverAdoptAnalyze(t *testing.T) {
	ctx := context.Background()
	w := fakeWorld(t)
	for name, p := range map[string]string{"kata": w.Kata.Problem, "owcli": w.Owcli.Problem, "bossman": w.Bossman.Problem} {
		if p != "" {
			t.Fatalf("%s: %s", name, p)
		}
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cs, err := discover.Discover(ctx, st, w)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*discover.Candidate{}
	for _, c := range cs {
		got[c.Root] = c
	}
	shop := got["/src/shop"]
	if shop == nil || !shop.Git || shop.Sessions != 3 || len(shop.Components) != 4 {
		t.Fatalf("shop candidate %+v", shop)
	}
	if other := got["/src/other"]; other == nil || !hasKind(other.Components, store.KindKata) {
		t.Fatalf("other should match kata by remote: %+v", other)
	}
	if e := got["/elsewhere"]; e == nil || e.Git || !e.Missing {
		t.Fatalf("elsewhere %+v", e)
	}

	for _, root := range []string{"/src/shop", "/src/other", "/src/shop/nested"} {
		c, err := discover.ForDir(ctx, w, root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := discover.Adopt(st, c, "", "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	// A pinned session overrides directory ownership.
	if _, err := st.Attach("other", store.Component{Kind: store.KindSession, Ref: "claude:s4"}); err != nil {
		t.Fatal(err)
	}
	if cs, _ = discover.Discover(ctx, st, w); got["/src/shop"] == nil {
		t.Fatal("rediscover")
	}
	for _, c := range cs {
		if c.Root == "/src/shop" && c.ClaimedBy != "shop" {
			t.Fatalf("shop claimed by %q", c.ClaimedBy)
		}
	}

	pf, err := portfolio.Analyze(ctx, st, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	reps := map[string]*portfolio.Report{}
	for _, r := range pf.Reports {
		reps[r.Project.Slug] = r
	}
	s := reps["shop"]
	if s.Metrics.Sessions != 1 || s.Metrics.CostUSD != 10 {
		t.Errorf("shop sessions %d cost %v: nested and pinned sessions must go elsewhere", s.Metrics.Sessions, s.Metrics.CostUSD)
	}
	if s.Metrics.IssuesOpen != 3 || s.Metrics.IssuesStuck != 1 || s.Metrics.IssuesNeedsHuman != 1 || s.Metrics.IssuesOverdue != 1 || s.Metrics.IssuesClosedRecent != 1 {
		t.Errorf("shop issues %+v", s.Metrics)
	}
	if s.Metrics.WikiDrift != 12 || s.Metrics.GitDirty != 2 {
		t.Errorf("shop wiki drift %d dirty %d", s.Metrics.WikiDrift, s.Metrics.GitDirty)
	}
	wantFlags(t, s, "stuck", "needs-human", "overdue", "wiki-behind", "dirty")
	if s.Health != portfolio.LevelAlert {
		t.Errorf("shop health %s", s.Health)
	}
	if n := reps["nested"]; n.Metrics.Sessions != 1 || n.Metrics.CostUSD != 5 {
		t.Errorf("nested %+v", n.Metrics)
	}
	o := reps["other"]
	if o.Metrics.Sessions != 2 || o.Metrics.CostUSD != 9 || o.Metrics.IssuesOpen != 1 {
		t.Errorf("other %+v", o.Metrics)
	}
	// other's latest activity is the pinned session a day ago: not stale.
	for _, f := range o.Flags {
		if f.Code == "stale" {
			t.Errorf("other flagged stale: %s", f.Message)
		}
	}
	if pf.Unassigned.Sessions != 1 || pf.Unassigned.Paths[0] != "/elsewhere" {
		t.Errorf("unassigned %+v", pf.Unassigned)
	}

	// Done with open issues and paused-but-moving are flagged.
	if _, err := st.SetState("other", store.StateDone, ""); err != nil {
		t.Fatal(err)
	}
	pf, _ = portfolio.Analyze(ctx, st, w, func(p *store.Project) bool { return p.Slug == "other" })
	wantFlags(t, pf.Reports[0], "open-issues", "activity")

	if err := portfolio.Record(st, pf); err != nil {
		t.Fatal(err)
	}
	snaps, _ := st.Snapshots(pf.Reports[0].Project.ID, time.Time{})
	if len(snaps) != 1 || !strings.Contains(string(snaps[0].Data), `"open-issues"`) {
		t.Errorf("snapshots %v", snaps)
	}
}

func TestMissingTools(t *testing.T) {
	w := sources.Collect(context.Background(), config.Default(), testutil.Runner{})
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	st.CreateProject("p", "", "", nil)
	st.Attach("p", store.Component{Kind: store.KindKata, Ref: "x"})
	st.Attach("p", store.Component{Kind: "email", Ref: "https://mail/thread"})
	pf, err := portfolio.Analyze(context.Background(), st, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := pf.Reports[0]
	if r.Health != portfolio.LevelAlert || r.Components[1].Problem == "" {
		t.Fatalf("kata failure should surface as a problem: %+v", r)
	}
	if len(r.Components[0].Links) != 1 {
		t.Fatalf("unknown kind with URL ref should link: %+v", r.Components[0])
	}
}

func hasKind(cs []store.Component, kind string) bool {
	for _, c := range cs {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

func wantFlags(t *testing.T, r *portfolio.Report, codes ...string) {
	t.Helper()
	have := map[string]bool{}
	var all []string
	for _, f := range r.Flags {
		have[f.Code] = true
		all = append(all, f.Code)
	}
	for _, c := range codes {
		if !have[c] {
			t.Errorf("%s: missing flag %s (have %s)", r.Project.Slug, c, fmt.Sprint(all))
		}
	}
}
