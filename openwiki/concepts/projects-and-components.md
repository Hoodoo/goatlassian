---
type: Concept
title: Projects, Components, and the Store
description: goatlassian's data model - projects, lifecycle states, tags, open-ended component kinds, the event log, snapshots - and the SQLite schema that holds them.
tags: [data-model, store, sqlite, lifecycle]
verified:
  - by: owcli/v0.2.0-1-g3d84f34
    at: "2026-10-05T08:34:54.657Z"
sources:
  - id: openwiki-source-f03b05428a9d9452871a8dd0
    resource: repo://internal/cli/projects.go
  - id: openwiki-source-fecec150f494f58fc6e0ca2c
    resource: repo://internal/store/relocate.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
  - id: openwiki-source-6d8c1cdec697aee752bd7c32
    resource: repo://internal/store/store_test.go
generated: { by: "owcli/v0.2.0-1-g3d84f34", at: "2026-10-05T08:35:12.025Z" }
---

# Projects, Components, and the Store

goatlassian owns a small amount of data, all in `internal/store`: which
artifacts belong together (projects and their components), where each
project is in its lifecycle, a log of what people did to it, and periodic
metric snapshots. What the artifacts *contain* (issues, wiki pages, sessions,
commits) stays with the tool that owns them and is read live; see
[Tool Adapters](../architecture/tool-adapters.md).

## Projects

A `Project` has a numeric ID, a unique **slug** (lower-case letters, digits,
`.`, `_`, `-`, starting with a letter or digit), a display name (defaults to
the slug), a description, tags, a lifecycle state, and created/updated
times. `Slugify` derives slugs from names and directory names
(`.emacs.d` → `emacs.d`, `My Project!` → `my-project`). Tags are lower-cased,
stripped of a leading `#`, deduplicated, and sorted.

`EditProject` takes a `ProjectEdit` in which nil fields are left alone:
slug, name, description, a replacement tag set, and tags to add or remove.
Renaming the slug is allowed; it fails if the new slug exists.

### Lifecycle

States are `active`, `paused`, `done`, and `archived`, in that display
order. Any transition is allowed; `SetState` logs `old → new: why`. States
have consequences in [analysis](portfolio-analysis.md): `active` projects can
go stale, `paused`/`done`/`archived` projects are flagged when they show
activity, `done`/`archived` projects are flagged for open issues, and
`archived` projects are hidden from `status` and the portfolio unless asked.

## Components

A `Component` points at an artifact another tool owns: `kind`, `ref`, an
optional `label`, and string `attrs`. The pair (project, kind, ref) is
unique; `Attach` on an existing pair is an upsert that updates the label and
attributes, which makes adopting the same directory twice harmless.

Known kinds (`store.KnownKinds`):

| Kind | Ref |
| --- | --- |
| `git` | absolute repository top-level path |
| `kata` | kata project name; `attrs.uid` pins the project UID |
| `owcli-wiki` | owcli wiki ID; `attrs.root` pins the wiki's repository root, since owcli changes the ID when the repository joins a workspace |
| `owcli-workspace` | owcli workspace ID |
| `sessions` | a directory; sessions started in or under it (`attrs.agent` narrows to one agent) |
| `session` | one bossman session key (`claude:<id>`, `codex:<id>`); overrides directory ownership |
| `link` | a URL |

**Kind is free text.** Anything else (`slack`, `pr`, `email`) can be attached
today; it is stored, displayed, and linked when the ref is a URL. This keeps
future artifact types from being locked out until an adapter exists; see
[Adding a Component Kind](../workflows/adding-a-component-kind.md).

## Relocating after a move

Components store absolute paths, so moving repositories (or a home directory
to a new machine) leaves them pointing nowhere. `Store.Relocate(old, new,
wikiRoots, dryRun)` (`internal/store/relocate.go`, `goatlassian relocate`)
rewrites every path at or under the old prefix, in all projects including
archived ones:

- `git` refs are rewritten; a project that already has the new path refuses
  the whole relocate.
- `owcli-wiki` components get `attrs.root` rewritten. One attached before
  roots were recorded takes its root from `wikiRoots`, the current owcli wiki
  IDs; the CLI builds that from `owcli wikis`, so relocate must run before
  `owcli relocate` changes the hash IDs.
- A `sessions` component keeps its old directory, because bossman records
  where each past session ran, and gains a sibling for the new directory
  (same label and attributes) so future sessions count too.

Other kinds are left alone. Each change is logged as a `relocate` event in
its project, all in one transaction; `--dry-run` reports without writing,
and running it again finds nothing to change.

## The event log

Every change writes an `Event` in the same transaction: `created`,
`edited` (what changed), `state`, `note`, `attach`, `detach`, `relocate`. Each event
records its actor, from `$GOATLASSIAN_ACTOR`, then `$KATA_AUTHOR`, then
`$USER` (see [Configuration](../operations/configuration-and-services.md)).
`LastEvents` gives each project's latest event time; analysis reports it as
`LastTouched` rather than activity.

## Snapshots

`AddSnapshot` stores a JSON document per project with a timestamp;
`Snapshots(project, since)` reads them oldest first and `LastSnapshotAt`
tells the serve loop when the last one was taken. The document is
`portfolio.SnapshotData` (metrics, health, flag codes, state).

## Schema

`Open` creates `<home>/goatlassian.db` and applies the schema idempotently
(`CREATE … IF NOT EXISTS`) on every open:

```mermaid
erDiagram
  projects ||--o{ project_tags : has
  projects ||--o{ components : groups
  projects ||--o{ events : logs
  projects ||--o{ snapshots : records
  projects { int id PK; text slug UK; text name; text description; text state; text created_at; text updated_at }
  components { int id PK; int project_id FK; text kind; text ref; text label; text attrs; text created_at }
  events { int id PK; int project_id FK; text at; text actor; text kind; text message }
  snapshots { int id PK; int project_id FK; text at; text data }
```

Every child table references `projects(id) ON DELETE CASCADE`, so
`DeleteProject` removes a project's tags, components, log, and snapshots in
one statement. Timestamps are RFC 3339 UTC text; `attrs` and snapshot `data`
are JSON text.

## Errors

Lookups of a missing project or component wrap `store.ErrNotFound`; the CLI
prints it and the web API turns it into 404. Validation failures (bad slug,
duplicate slug, unknown state, empty note, component without kind or ref)
are plain errors with a readable message.

## Tests

`internal/store/store_test.go` covers create/duplicate/invalid slug, state
changes, edits including slug rename and tag add/remove, the event sequence
and actor, delete, component upsert, unknown kinds, detach by ID and by
kind/ref, and `Slugify`.
