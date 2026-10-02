---
type: Reference
title: CLI Reference
description: Every goatlassian command and flag, grouped as the help groups them, with what each reads and writes.
tags: [cli, reference, commands]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T20:24:19.254Z"
sources:
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-bc7eae14d3b20f2f098061ac
    resource: repo://internal/cli/env.go
  - id: openwiki-source-f03b05428a9d9452871a8dd0
    resource: repo://internal/cli/projects.go
  - id: openwiki-source-664966ddac5a292f452c8dc3
    resource: repo://internal/cli/report.go
generated: { by: "owcli/ff31f70", at: "2026-10-02T20:25:09.016Z" }
---

# CLI Reference

The command tree is built in `internal/cli` with cobra. Commands are grouped
in `--help` as Reporting, Projects and components, and Environment. Two
persistent flags apply everywhere: `--home <dir>` (data directory; see
[Configuration](../operations/configuration-and-services.md)) and `--json`
(machine-readable output of the same data). Errors print
`goatlassian: <message>` and exit 1.

Commands marked *collects* run kata, owcli, bossman, and git once (see
[Tool Adapters](../architecture/tool-adapters.md)); the rest only touch the
local store. Tools that cannot be read produce a `warning:` line per tool on
stderr.

## Reporting

| Command | Flags | Does |
| --- | --- | --- |
| `status` *(collects)* | `--all` include archived · `--state S` · `-t/--tag T` · `-s/--sort` `health` (default), `activity`, `cost`, `recent-cost`, `tokens`, `open`, `idle`, `slug` · `--record` also snapshot | portfolio table: state, health, last activity, open, attention (stuck + needs-human + overdue), closed in window, sessions (recent/all), cost, recent cost, tokens, wiki drift, flag codes; then totals and unassigned sessions |
| `show <slug>` *(collects)* | | one project: state, health, flags with messages, every component with problem, summary, items, and links; metrics; last 10 log entries |
| `history <slug>` | `--days N` (90) | recorded snapshots as a table |
| `snapshot` *(collects)* | | records a snapshot of every project |
| `discover` *(collects)* | `--all` include covered · `--missing` include vanished directories · `--adopt` adopt every listed candidate | repositories the tools know about, who covers them, sessions and cost, which tools know them |

The `status`, `show`, and history logic is described in
[Portfolio Analysis](../concepts/portfolio-analysis.md).

## Projects and components

| Command | Flags | Does |
| --- | --- | --- |
| `adopt [dir]` *(collects)* | `--slug` · `--name` · `-d/--description` · `-t/--tag` · `-n/--dry-run` | creates a project from the repository containing `dir` (default `.`) with its git repo, kata projects, wikis, and sessions; with an existing `--slug`, attaches to it |
| `project create <slug>` | `--name` · `-d` · `-t` | empty project |
| `project list` (`ls`) | `--state` · `-t/--tag` | projects with component kinds; no tool calls |
| `project edit <slug>` | `--slug` · `--name` · `-d` · `-t/--tag` add · `--untag` remove | only flags given are changed |
| `project state <slug> <state>` | `-m/--why` | moves through `active`, `paused`, `done`, `archived`; the reason is logged |
| `project rm <slug>` | `--yes` required | deletes the project's record, components, log, and snapshots; artifacts untouched |
| `attach <slug> <kind> <ref>` *(collects unless --raw)* | `-l/--label` · `--attr k=v` (repeatable) · `--raw` | attaches a component after normalizing the ref (absolute paths, kata UID, wiki ID, full session key) |
| `detach <slug> <id>` or `detach <slug> <kind> <ref>` | | removes a component |
| `note <slug> <text…>` | `-` reads stdin | adds a note to the log |
| `log <slug>` | `-n/--limit N` (50; 0 = all) | the project log, newest first |

`project` also answers to `projects` and `p`. Component kinds are listed by
`goatlassian kinds` and explained in
[Projects, Components, and the Store](../concepts/projects-and-components.md).

## Environment

| Command | Flags | Does |
| --- | --- | --- |
| `serve` | `--addr` (`127.0.0.1:7799`) · `--open` · `--services` · `--snapshot-every` (6h; 0 disables) | the web UI; see [Web UI and JSON API](../architecture/web-ui.md) |
| `services [start]` | | reachability of the kata, owcli, and bossman UIs; `start` launches the missing ones |
| `paths` | | data directory, database, config, and logs paths |
| `kinds` | | component kinds and what their ref is |

## Examples

```sh
goatlassian discover
goatlassian adopt ~/src/shop -t client
goatlassian attach shop link https://github.com/me/shop/pull/12 -l "checkout PR"
goatlassian attach bossman session 6db7e574
goatlassian project state shop paused -m "waiting on design"
goatlassian status -s recent-cost
goatlassian show shop --json | jq '.report.flags'
```
