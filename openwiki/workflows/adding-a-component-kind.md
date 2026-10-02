---
type: Workflow
title: Adding a Component Kind or Adapter
description: How to give goatlassian a new artifact kind (for example Slack threads or GitHub PRs) or a new tool adapter, and how to develop and test changes without the real tools.
tags: [extending, adapters, testing, development]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T20:24:52.432Z"
sources:
  - id: openwiki-source-eebd91804ebf511b7b315be6
    resource: repo://internal/portfolio/portfolio.go
  - id: openwiki-source-af817f57994defe12ac57cfb
    resource: repo://internal/sources/sources.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
  - id: openwiki-source-042bf2107552a11ea809d8c6
    resource: repo://internal/testutil/fake.go
  - id: openwiki-source-213e0022dfc208535b4c26a9
    resource: repo://internal/web/static/app.js
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "owcli/ff31f70", at: "2026-10-02T20:25:09.016Z" }
---

# Adding a Component Kind or Adapter

goatlassian is designed so that new kinds of artifacts are never locked out.
There are three levels of support, and each builds on the previous one.

## Level 0: attach it today

Kinds are free text (see [Projects, Components, and the Store](../concepts/projects-and-components.md)),
so any artifact can be attached without code changes:

```sh
goatlassian attach shop slack https://team.slack.com/archives/C1/p2 -l "launch thread"
goatlassian attach shop pr https://github.com/me/shop/pull/12 --attr state=open
```

`describe` in `internal/portfolio/portfolio.go` falls through to its
default case: the label (or "no adapter for kind …") becomes the summary,
and a ref starting with `http://` or `https://` becomes an "open" link. The
component appears in `show`, the project page, and `--json` output with its
attributes.

## Level 1: a known kind

To make a kind first-class (listed in `goatlassian kinds`, offered in the UI's
attach form with a ref hint):

1. Add a constant next to `KindGit` … `KindLink` in `internal/store/store.go`,
   with a comment saying what its ref is, and add it to `KnownKinds`.
2. Add a line to the `kinds` command in `internal/cli/cli.go`.
3. Add a placeholder to `REF_HINT` in `internal/web/static/app.js`.
4. If the ref needs canonicalizing (paths, names to IDs), add a case to
   `discover.Normalize`; return an error for references that do not exist so
   `attach` can refuse them (the user can still `--raw`).

## Level 2: an adapter

An adapter reads the artifact's tool and turns it into status, metrics, and
links.

1. **Collect** in `internal/sources`. Add a section to `World` with a
   `Problem string`, a `collectX` function that runs the tool through the
   `Runner` (use `runJSON` for JSON output), and call it from `Collect`
   alongside the others (bump the `WaitGroup`). Collect in bulk, one command
   for every project, not one per component. Never return an error from a
   collector: put it in `Problem`.
2. **Describe** in `internal/portfolio`. Add a `describeX` and a case in
   `describe`. Set `cr.Problem` when the tool failed or does not know the
   ref; fill `Summary`, `Links` (deep links into the tool's UI), `Items` for
   things a human should look at (use `Level: LevelAlert` for those that
   need attention), and `LastActivity` so the project's staleness reflects it.
3. **Metrics and flags.** If the kind contributes numbers, add fields to
   `Metrics`, sum them in `addMetrics`, and derive any new flag in `flags`.
   New flag codes should also appear in the `status` help text in
   `internal/cli/report.go`. Snapshots pick up new metrics automatically.
4. **Discovery** (optional). If the tool knows which repository an artifact
   belongs to, propose it in `discover.ForDir` and add its directories to
   `discover.Discover`.
5. **UI.** Component cards render summary, problem, items, and links
   generically; nothing is needed unless the kind adds new metrics worth a
   column or tile.

The existing adapters in [Tool Adapters](../architecture/tool-adapters.md)
and [Portfolio Analysis](../concepts/portfolio-analysis.md) are the models
to copy: kata (bulk issues plus per-project lookups) and bossman (one bulk
list, then ownership rules) cover most shapes.

## Developing without the tools

Everything that runs a process goes through `sources.Runner`.
`testutil.Runner` is a map from the full command line (`"kata daemon status
--json"`, `"git -C /repo log -1 --format=…"`) to its output; a key ending in
`*` matches any command with that prefix (the longest prefix wins), and
unknown commands fail like a missing tool. `fakeWorld` in
`internal/portfolio/portfolio_test.go` is a complete example: a kata, owcli,
and bossman world plus three fake repositories, used to test discovery,
ownership, metrics, and flags end to end. `internal/web/web_test.go` uses
the same runner to drive the HTTP API.

Build and test with the Makefile:

```sh
make check     # go vet ./... && go test ./...
make build     # bin/goatlassian with the git-describe version
make install   # to ~/.local/bin
```

Try a change against the real tools without touching your real database by
pointing `--home` (or `GOATLASSIAN_HOME`) at a scratch directory:

```sh
bin/goatlassian --home /tmp/goat discover --adopt
bin/goatlassian --home /tmp/goat serve --addr 127.0.0.1:7798
```
