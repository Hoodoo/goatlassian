---
type: Overview
title: Quickstart
description: What goatlassian is, how to build and run it, and which wiki page answers each common task.
tags: [quickstart, overview, onboarding]
verified:
  - by: owcli/2d956c2
    at: "2026-10-03T15:43:14.247Z"
sources:
  - id: openwiki-source-7bd911fdd3026b7b031a01e3
    resource: repo://go.mod
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-bc7eae14d3b20f2f098061ac
    resource: repo://internal/cli/env.go
  - id: openwiki-source-042bf2107552a11ea809d8c6
    resource: repo://internal/testutil/fake.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
  - id: openwiki-source-23775c3de52f3ab95a13cb8b
    resource: repo://README.md
generated: { by: "owcli/2d956c2", at: "2026-10-03T15:43:14.386Z" }
---

# Quickstart

goatlassian is a thin project layer over the tools of agent-driven
development. A **project** groups **components** owned by other tools: a git
repository, kata issue trackers, owcli wikis and workspaces, the Claude and
Codex sessions bossman indexes, and links to anything else. goatlassian
stores only the grouping, a lifecycle state, a log, and metric snapshots; it
reads the tools through their CLIs and links to their web UIs, and never
writes into a repository. The result is a portfolio view: what is in flight,
what is stale, what needs a human, and what it costs.

## Build and run

```sh
make install                   # bin/goatlassian → ~/.local/bin
# or, without a clone: go install github.com/Hoodoo/goatlassian/cmd/goatlassian@latest
goatlassian discover           # repos kata/owcli/bossman know, and who covers them
goatlassian discover --adopt   # one project per uncovered repo
goatlassian status             # the portfolio
goatlassian show <slug>        # one project with links into kata, owcli, bossman
goatlassian serve --open       # web UI on http://127.0.0.1:7799
```

goatlassian needs Go 1.22 to build and the `kata`, `owcli`, `bossman`, and
`git` executables at run time; any that are missing show up as component
problems rather than errors.

## Where to look

| Task | Page |
| --- | --- |
| Understand the packages and data flow | [Architecture Overview](architecture/overview.md) |
| Understand projects, kinds, lifecycle, the database | [Projects, Components, and the Store](concepts/projects-and-components.md) |
| Know which commands goatlassian runs against each tool, or why a component shows a problem | [Tool Adapters and Discovery](architecture/tool-adapters.md) |
| Understand a number, a flag, health, or which project a session counted toward | [Portfolio Analysis](concepts/portfolio-analysis.md) |
| Change the web UI or call the API | [Web UI and JSON API](architecture/web-ui.md) |
| Configure thresholds, URLs, binaries; start sibling UIs | [Configuration and Services](operations/configuration-and-services.md) |
| Look up a command or flag | [CLI Reference](reference/cli.md) |
| Support a new artifact (Slack, PRs, mail) or write a new adapter; run tests | [Adding a Component Kind or Adapter](workflows/adding-a-component-kind.md) |

## Source map

```
cmd/goatlassian/        main
internal/cli/           cobra commands (cli.go root, projects.go, report.go, env.go)
internal/config/        data directory and config.toml
internal/store/         SQLite store
internal/sources/       kata, owcli, bossman, git adapters → World
internal/discover/      discover, adopt, normalize
internal/portfolio/     reports, metrics, flags, snapshots
internal/services/      sibling web UIs
internal/web/           HTTP API + embedded UI (static/)
internal/testutil/      fake Runner for tests
```

Run `make check` before committing; tests never need the real tools.
