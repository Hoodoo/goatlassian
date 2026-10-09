---
type: Concept
title: Portfolio Analysis
description: How goatlassian turns projects and a World into per-component status, per-project metrics, last activity, session ownership, health flags, unassigned sessions, and snapshots.
tags: [analysis, metrics, flags, health, sessions]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T16:06:18.958Z"
sources:
  - id: openwiki-source-eebd91804ebf511b7b315be6
    resource: repo://internal/portfolio/portfolio.go
  - id: openwiki-source-127e8e798f871db6d31266f6
    resource: repo://internal/portfolio/portfolio_test.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T16:06:41.345Z" }
---

# Portfolio Analysis

`internal/portfolio` is where goatlassian's opinions live. `Analyze` takes
the store and a `World` (see [Tool Adapters](../architecture/tool-adapters.md))
and returns a `Portfolio`: one `Report` per project, totals, sinks, unassigned
sessions, and each tool's problem. `status`, `show`, the web API, and
snapshots all consume it.

## Reports and component status

For each project `Analyze` calls `describe` on every component, which
dispatches by kind and returns a `ComponentReport`: the component plus
`Problem`, a one-line `Summary`, `Links` into the owning tool's UI, `Items`
(notable things), `LastActivity`, and kind-specific `Data`. While describing,
it adds to the project's `Metrics`.

| Kind | Summary, items, and metrics |
| --- | --- |
| `git` | branch, uncommitted, unpushed, behind, no upstream, recent commits (or "no commits yet"); items: HEAD subject, unmerged branches; activity: HEAD committer date; link to the remote when it is browsable |
| `kata` | open/closed counts; items: issues whose `work.attention` is `stuck` or `needs-human` (with `work.attention_msg`), and open issues past `deadline_on`; counts closed-in-window and blocked (an open `blocked_by` predecessor); activity: latest issue update |
| `owcli-wiki` | last run and drift (commits from the wiki's `gitHead` to HEAD); a wiki `problem` such as a missing directory becomes the component's problem; activity: last update |
| `owcli-workspace` | member wikis with their drift |
| `sessions`, `session` | sessions owned by this component (below), agents, cost, tokens; items: the eight most recent sessions with links |
| `link` | its label or URL, linked |
| other | stored ref; linked when it is a URL |

A kata component is matched by `attrs.uid` first and its name second, so a
renamed kata project stays attached. An `owcli-wiki` component is matched by
its ID first and `attrs.root` (the wiki's repository root) second, because
owcli changes a wiki's ID when its repository joins a workspace; components
attached before the root was recorded must be re-attached once their ID
changes. Date-only deadlines count as passed at
the end of that day.

## Last activity

A project's `LastActivity` is the latest `LastActivity` of its components,
and `LastActivityBy` names the kind it came from. goatlassian's own log
(notes, state changes, attach) is reported separately as `LastTouched` and
does **not** count: bookkeeping must not hide a stale project.

## Session ownership

`assignSessions` decides, once across all projects, which component owns each
session, so a session is never counted twice:

1. A `session` component naming the session's key wins outright.
2. Otherwise the `sessions` component with the **longest** directory that
   contains the session's working directory wins, so a session in a nested
   repository belongs to the innermost project. An `attrs.agent` on a
   `sessions` component restricts it to that agent.

Sessions no component owns are summed into `Unassigned` (count, cost, and
directories ordered by session count); `status` prints the busiest three and
the UI shows a tile. Sink sessions (below) are left out of it.

## Sinks

A sink is a bucket for work that belongs to no project, such as a throwaway
prototype or an agent cleaning up a config. It is a bossman tag:
`sink:<name>` (`SinkPrefix`), set by hand with `bossman tag`. goatlassian
reads tags from `bossman --json ls`, so a sink needs no goatlassian state.

After ownership is decided, each session no component owns that has a sink
tag is summed into `Portfolio.Sinks` instead of `Unassigned`:

- A `Sink` holds the name (the tag without the prefix), the tag, sessions,
  cost, tokens, the latest session end (or start), and `URL`, bossman's list
  filtered by the tag (`sources.TagURL`).
- Ownership always wins: a sink tag never takes a session from a project
  that pins it or whose `sessions` directory contains it.
- A session with several sink tags goes to the lexicographically first
  (`sinkTag`). A bare `sink:` tag is ignored.
- Sinks are sorted by cost, highest first, then by name. They are not
  projects: they get no flags, health, or snapshots.
- Discovery still lists the directories of sink sessions, which keeps where
  the work happened visible.

`status` prints a `sinks:` line after the unassigned one, and the web UI
shows a Sinks tile and table (see [Web UI](../architecture/web-ui.md)).

## Metrics

`Metrics` sums issues (open, closed, closed recently, stuck, needs-human,
overdue, blocked), sessions (all and recent), cost and tokens (all and
recent), output tokens, interventions, tool errors, agent-hours (bossman's
active seconds), recent commits, dirty paths, unpushed commits, and the
largest wiki drift (-1 when unknown). "Recent" means within `recent_days`
of the collection time. `Totals` adds every reported project.

## Flags and health

`flags` derives observations; a project's `Health` is the most severe level
among them (`ok` < `info` < `warn` < `alert`).

| Code | Level | When |
| --- | --- | --- |
| `problem` | alert | a component's tool cannot resolve it |
| `stuck`, `needs-human` | alert | open kata issues with that `work.attention` |
| `overdue` | alert | open issues past their deadline |
| `stale` | warn | `active` and idle longer than `stale_days` |
| `activity` | info | `paused`, `done`, or `archived` with activity in the last 72 hours |
| `open-issues` | warn | `done` or `archived` with open issues |
| `wiki-behind` | warn | wiki drift ≥ `wiki_drift_commits` |
| `dirty`, `unpushed` | info | uncommitted paths / commits ahead of upstream |
| `empty` | info | no components |

## Snapshots

`Record` stores a `SnapshotData` per reported project: metrics, health, flag
codes, and state. `goatlassian snapshot`, `status --record`, and the serve
loop call it; `history` and the project page's charts read it back. See
[Projects, Components, and the Store](projects-and-components.md) for the
table.

## Tests

`internal/portfolio/portfolio_test.go` builds a fake world with nested
repositories, a kata project bound by git remote, stuck/needs-human/overdue
issues, wiki drift, a pinned session, and sink-tagged sessions, then checks
discovery, ownership, metrics, flags, sinks, unassigned sessions, and
snapshots. `TestWikiIDChange`
adopts a repository, renames its wiki's ID the way joining a workspace does,
and checks the component still resolves. `TestRelocate` adopts a repository,
relocates it, and checks that git and the wiki resolve at the new path while
sessions recorded under the old path still count. `TestMissingTools` checks
that tools that cannot be run surface as alert-level problems.
