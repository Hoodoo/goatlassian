---
type: Reference
title: Tool Adapters and Discovery
description: The commands goatlassian runs against kata, owcli, bossman, and git, what it reads from each, how failures surface, the deep-link formats, and how discovery maps tool records onto projects.
tags: [integration, kata, owcli, bossman, git, discovery]
verified:
  - by: owcli/v0.2.0
    at: "2026-10-05T08:22:21.998Z"
sources:
  - id: openwiki-source-60aa3cff97fd2a958231fb58
    resource: repo://internal/discover/discover.go
  - id: openwiki-source-af817f57994defe12ac57cfb
    resource: repo://internal/sources/sources.go
  - id: openwiki-source-8b7b5c9a0a9190a6b5b944cb
    resource: repo://internal/sources/sources_test.go
generated: { by: "owcli/v0.2.0", at: "2026-10-05T08:23:11.755Z" }
---

# Tool Adapters and Discovery

`internal/sources` is the only package that talks to other tools, and it does
so only by running their command lines through the `Runner` interface
(`Exec` in production, `testutil.Runner` in tests). `internal/discover` uses
what sources collected to propose and adopt projects. See the
[Architecture Overview](overview.md) for where this sits in the data flow.

## The World

`sources.Collect` returns a `World`: `Kata`, `Owcli`, and `Bossman` sections,
each with a `Problem` string that is empty when the tool answered. The three
collectors run concurrently. `runJSON` runs a command and decodes its
standard output; when the binary is not on `PATH` it reports
`<tool> is not installed`, and `Exec.Run` folds the command's stderr (trimmed
to 300 characters) into other errors so the problem text is actionable.

## kata

| Command | Used for |
| --- | --- |
| `kata daemon status --json` | the daemon's `web_url`, the base for kata links |
| `kata projects list --json` | every project: id, UID, name |
| `kata projects show <name> --json` | each project's aliases, run concurrently per project |
| `kata list --all --status all --limit 0 --json` | every issue in every project |

Aliases become bindings: `local:///path` identities become `Paths`, and aliases
of kind `git` (for example `github.com/me/repo`) become `Remotes`. Issues keep
status, priority, owner, labels, metadata (`work.attention`,
`work.attention_msg`), timestamps, `deadline_on` (kept as a string, since kata
may store a date or a time), `blocked_by`, and the issue's own `web_url`.
A kata component's link is `<web_url>/kata?scope=<project UID>`.

## owcli

`owcli wikis --json` lists every wiki (ID, name, `repoRoot`, `wikiDir`,
layout, workspaces, `lastUpdate` with its `gitHead`, and a `problem` such as a
missing wiki directory) and every workspace with its member wiki IDs.
`--health` is deliberately not used: it runs the Claims preflight on every
wiki and is slow. Links go to `owcli serve`:
`<owcli_url>/#scope=wiki:<id>` or `#scope=ws:<id>`.

## bossman

`bossman --json ls -n 0` returns every indexed session with project path,
agent, timing, prompts, interventions, tool errors, token counts, and cost.
`Session.Tokens()` is input + cache write + cache read + output. Links go to
`bossman serve`: one session is `<bossman_url>/#/s/<key>`, a directory's
sessions are `#/?project=<path>`.

## git

Git is not collected in bulk. `World.Git(path)` runs, in that repository:
`rev-parse --show-toplevel`, `branch --show-current`, `status --porcelain`
(each line is one dirty path), `log -1` (HEAD hash, committer date, subject),
the upstream and `rev-list --left-right --count HEAD...@{u}` (ahead/behind),
`remote get-url origin`, a commit count over `recent_days`, the local branch
count, and `branch --no-merged HEAD`. Results are cached in the `World`.
`World.CommitsSince(path, rev)` counts `rev..HEAD`, returning -1 when the
revision is unknown; wiki drift uses it.

`RemoteIdentity` reduces a remote URL to kata's binding form, lower-cased:
`git@github.com:Me/Repo.git` and `https://user:tok@github.com/me/repo` both
become `github.com/me/repo`.

## Discovery and adopt

`discover.ForDir(dir)` resolves the repository top level (or keeps the
directory when it is not a repository) and proposes components:

- `git` for the repository;
- `kata` for every kata project bound to a path inside the root, or bound to
  a git remote equal to the repository's `origin` identity (the UID is stored
  in `attrs.uid`);
- `owcli-wiki` for every wiki whose `repoRoot` is inside the root (the root
  is stored in `attrs.root`, because owcli changes a wiki's ID when its
  repository joins a workspace; `OwcliWorld.WikiFor` looks a wiki up by ID,
  then by that root);
- `sessions` for the root when it is a repository or has sessions, so future
  sessions are counted too.

`discover.Discover` gathers every directory the tools know (kata paths, wiki
roots, session directories), reduces each to its repository root, builds a
candidate per root, and marks the project that already covers it
(`ClaimedBy`: a `git` component on that root or a `sessions` component
containing it). `discover.Adopt` creates the project, or attaches to it when
the slug already exists; attaching twice is an upsert, so adopting again is
safe.

## Normalizing hand-attached components

`discover.Normalize` runs before `attach` (CLI and API) unless `--raw` /
`raw: true` is given. It makes `git` and `sessions` refs absolute (`git`
becomes the repository top level and must be a repository), resolves a kata
project name to its UID, a wiki or workspace name to its ID, and a session ID
or unique prefix to its full `agent:id` key. Unknown references are errors,
which the UI offers to override with a raw attach.
