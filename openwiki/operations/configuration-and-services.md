---
type: Guide
title: Configuration and Services
description: Where goatlassian keeps its data, every config.toml key and default, how changes are attributed, and how the kata, owcli, and bossman web UIs are checked and started.
tags: [configuration, operations, services, data-directory]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T20:23:49.342Z"
sources:
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-21a56aca001363e88303d2bb
    resource: repo://internal/services/services.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
generated: { by: "owcli/ff31f70", at: "2026-10-02T20:25:09.016Z" }
---

# Configuration and Services

## Data directory

`config.Home` resolves the data directory in this order: the `--home` flag,
`$GOATLASSIAN_HOME`, `$XDG_DATA_HOME/goatlassian`, then
`~/.local/share/goatlassian`. The root command's `PersistentPreRunE`
resolves it and loads the config before every command. The directory holds:

| Path | What |
| --- | --- |
| `goatlassian.db` | the SQLite store (WAL mode); see [the store](../concepts/projects-and-components.md) |
| `config.toml` | optional configuration |
| `logs/<tool>.log` | output of sibling UIs started by `services start` |

`goatlassian paths` prints these (`--json` adds the effective config).
Nothing is ever written into a tracked repository.

## config.toml

Every key is optional; `config.Load` decodes the file over `config.Default()`
and restores defaults for zero or empty values, so a partial file is fine
and a missing file is not an error.

| Key | Default | Effect |
| --- | --- | --- |
| `stale_days` | 14 | an `active` project idle this long is flagged `stale` |
| `wiki_drift_commits` | 10 | a wiki this many commits behind HEAD is flagged `wiki-behind` |
| `recent_days` | 30 | window for recent cost, tokens, sessions, closed issues, commits |
| `[services] owcli_url` | `http://127.0.0.1:4321` | base for owcli wiki links and health checks |
| `[services] bossman_url` | `http://127.0.0.1:7788` | base for bossman session links and health checks |
| `[bin] git`, `kata`, `owcli`, `bossman` | the bare names | executables to run (names on `PATH` or absolute paths) |

The service URLs default to the ports `owcli serve` and `bossman serve` use
by default. kata needs no setting: its web address comes from
`kata daemon status --json`.

## Attribution

Log entries record an actor: `$GOATLASSIAN_ACTOR`, else `$KATA_AUTHOR`, else
`$USER`. Agents that already set `KATA_AUTHOR` for kata are therefore
attributed the same way in goatlassian.

## Services

goatlassian links into three web UIs but does not require them to be
running. `services.Status` checks each:

| Service | Check | Start command |
| --- | --- | --- |
| kata | `kata daemon status --json` gives `web_url`, then an HTTP GET | `kata daemon start` |
| owcli | GET `<owcli_url>/api/wikis` | `owcli serve --no-open --port <port>` |
| bossman | GET `<bossman_url>/api/facets` | `bossman serve --addr <host:port>` |

Checks use a 1.5-second HTTP timeout; any status below 500 counts as up.

`services.StartMissing` (`goatlassian services start`, the UI's "start" link,
or `goatlassian serve --services`) starts each service that is down with
`setsid`, so it outlives goatlassian, appending its output to
`logs/<tool>.log` with a header line, then polls for up to five seconds and
returns the new status. Note that `bossman serve` syncs its archive at
start-up, and that `owcli serve` moves to the next free port when its port
is taken, in which case the configured URL will not match until you fix
`owcli_url`.

## Operating notes

- Run `goatlassian serve` long-lived to accumulate snapshots (every 6h by
  default); `goatlassian snapshot` from cron works as well.
- A tool that cannot be read never breaks a report: its components show a
  `problem` flag and the CLI prints a warning per tool on stderr.
