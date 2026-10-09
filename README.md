# goatlassian

A thin project layer over the tools of agent-driven development: one place
to see what is in flight, what is stale, what needs a human, and what it
costs, across every repository your agents work in.

A **project** groups **components** that other tools own:

| kind              | ref                                   | read through                         |
| ----------------- | ------------------------------------- | ------------------------------------ |
| `git`             | repository path                       | `git`                                |
| `kata`            | kata project name (pinned by UID)     | `kata … --json`, daemon web UI       |
| `owcli-wiki`      | owcli wiki ID or name (pinned by repo root) | `owcli wikis --json`, `owcli serve`  |
| `owcli-workspace` | owcli workspace ID or name            | `owcli wikis --json`, `owcli serve`  |
| `sessions`        | directory sessions ran in or under    | `bossman --json ls`, `bossman serve` |
| `session`         | one bossman session key               | same; pins it, overriding directories|
| `link`            | URL (PR, Slack thread, doc…)          | linked                               |
| anything else     | free text; URLs are linked            | kept and shown; no adapter yet       |

goatlassian stores only the grouping, a lifecycle state (`active`, `paused`,
`done`, `archived`), tags, a log (notes, state changes, attach/detach), and
metric snapshots, in `~/.local/share/goatlassian/goatlassian.db`. It reads
the other tools through their command lines and links to their web UIs. It
never writes into a repository, so it works on projects where you cannot add
files.

## Install

```sh
go install github.com/Hoodoo/goatlassian/cmd/goatlassian@latest
# or, from a clone:
make install        # builds bin/goatlassian and installs it to ~/.local/bin
```

## Use

```sh
goatlassian discover                 # every repo kata/owcli/bossman know, and which project covers it
goatlassian adopt ~/src/shop         # project from a repo: git, kata projects, wikis, sessions
goatlassian discover --adopt         # adopt every uncovered repo at once
goatlassian status                   # the portfolio (sort with -s activity|cost|recent-cost|idle|…)
goatlassian show shop                # components, attention items, links, log
goatlassian serve --open             # the same in a web UI at http://127.0.0.1:7799
```

Curate:

```sh
goatlassian attach shop link https://github.com/me/shop/pull/12 -l "checkout PR"
goatlassian attach shop slack https://team.slack.com/archives/C1/p2 -l "launch thread"
goatlassian attach bossman session 6db7e574     # a session started elsewhere belongs here
goatlassian project state shop paused -m "waiting on design"
goatlassian project edit shop -t client --untag web
goatlassian note shop "agreed scope with Ann"
goatlassian log shop
```

After moving repositories (or your home directory to a new machine), rewrite
the stored paths: git refs and wiki roots move, and each sessions directory
gains the new path while keeping the old one, where past sessions ran.

```sh
goatlassian relocate --dry-run ~/src ~/work
goatlassian relocate ~/src ~/work
```

Every command takes `--json`.

### How projects are read

- **Activity** is the latest of: commit date, kata issue update, wiki
  update, session end. goatlassian's own log does not count.
- **Sessions** belong to the innermost project whose `sessions` directory
  contains them, unless a `session` component pins them elsewhere. A session
  is never counted twice. Of the rest, sessions tagged `sink:<name>` in
  bossman are listed per sink (sessions, cost, tokens, last activity, a
  link to bossman); a sink tag never takes a session from a project. What
  remains is reported as unassigned.
- **Flags** set a project's health (worst wins):
  `problem` (a component its tool cannot resolve), `stuck` / `needs-human`
  (kata `work.attention`), `overdue` (kata deadline), `stale` (active but
  idle past `stale_days`), `activity` (paused/done yet moving), `open-issues`
  (done/archived with open issues), `wiki-behind` (wiki `wiki_drift_commits`
  or more commits behind HEAD), `dirty`, `unpushed`, `empty`.
- **Snapshots** of each project's metrics are recorded by `goatlassian
  snapshot`, `status --record`, and every 6h while `serve` runs; `history`
  and the project page show the trend.

### Sibling web UIs

`goatlassian services` reports whether the kata daemon, `owcli serve`, and
`bossman serve` are reachable; `goatlassian services start` (or `serve
--services`) starts the missing ones detached, logging to
`~/.local/share/goatlassian/logs`.

### Behind a reverse proxy

`serve` stays on loopback by default. To publish it through a proxy that
signs people in, such as Google IAP, let it listen where the proxy reaches
it, accept the public name, and trust the proxy's user header:

```sh
goatlassian serve --addr 0.0.0.0:7799 --allow-host portfolio.example.com \
  --user-header X-Goog-Authenticated-User-Email
```

Requests without the header are refused, and every change made in the UI is
logged with the signed-in user as its actor. Only trust the header when
nothing but the proxy can reach the address (on GCP, a firewall that admits
only the load balancer). Point `[services]` in config.toml at the sibling
UIs' public addresses so its links work for viewers.

## Configuration

`~/.local/share/goatlassian/config.toml` (or `$GOATLASSIAN_HOME`), all optional:

```toml
stale_days = 14          # active and idle this long → stale
wiki_drift_commits = 10  # wiki this far behind HEAD → wiki-behind
recent_days = 30         # window for "recent" cost, sessions, closes

[services]
owcli_url = "http://127.0.0.1:4321"
bossman_url = "http://127.0.0.1:7788"

[bin]                    # names or paths of the tools
git = "git"
kata = "kata"
owcli = "owcli"
bossman = "bossman"
```

Log entries are attributed to `$GOATLASSIAN_ACTOR`, else `$KATA_AUTHOR`, else `$USER`.

## Development

```sh
make check   # go vet + go test
```

Layout: `internal/store` (SQLite), `internal/sources` (tool adapters),
`internal/discover` (adopt/discover/normalize), `internal/portfolio`
(analysis and flags), `internal/services`, `internal/web` (API + embedded
UI), `internal/cli`.
