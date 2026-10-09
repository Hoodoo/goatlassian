---
type: Reference
title: Web UI and JSON API
description: How goatlassian serve works - its JSON API, request guards, World caching, the snapshot loop, and the embedded single-page client.
tags: [web, api, ui, serve]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T16:05:54.595Z"
sources:
  - id: openwiki-source-bc7eae14d3b20f2f098061ac
    resource: repo://internal/cli/env.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
  - id: openwiki-source-213e0022dfc208535b4c26a9
    resource: repo://internal/web/static/app.js
  - id: openwiki-source-6dbe79f2b1613ac94797fd56
    resource: repo://internal/web/web.go
  - id: openwiki-source-eb4688fc0fba2b62687b137d
    resource: repo://internal/web/web_test.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T16:06:41.345Z" }
---

# Web UI and JSON API

`goatlassian serve` (`internal/cli/env.go`) starts `web.Server` on
`127.0.0.1:7799` by default, warning when `--addr` is not a loopback
address. The server exposes a JSON API and serves an embedded vanilla
JavaScript client from `internal/web/static` (`//go:embed static`). The UI
follows bossman's visual language (its stylesheet tokens are reused) so the
suite looks like one product. Everything shown comes from
[portfolio analysis](../concepts/portfolio-analysis.md); every outbound link
goes to the kata, owcli, or bossman web UI described in
[Tool Adapters](tool-adapters.md).

## API

| Route | Does |
| --- | --- |
| `GET /api/meta` | version, lifecycle states, known kinds, config, actor |
| `GET /api/portfolio[?all=1][&refresh=1]` | the portfolio; archived projects only with `all=1` |
| `POST /api/projects` | create (`slug` defaults to the slugified `name`) |
| `GET /api/projects/{slug}` | report, last 200 log events, 180 days of snapshots |
| `PATCH /api/projects/{slug}` | edit (`store.ProjectEdit`: slug, name, description, tags) |
| `DELETE /api/projects/{slug}` | delete from goatlassian |
| `POST /api/projects/{slug}/state` | `{state, why}` |
| `POST /api/projects/{slug}/notes` | `{text}` |
| `POST /api/projects/{slug}/components` | attach `{kind, ref, label, attrs, raw}`; normalized unless `raw` |
| `DELETE /api/projects/{slug}/components/{id}` | detach |
| `GET /api/discover` | discovery candidates |
| `POST /api/adopt` | `{root, slug, …}` |
| `GET /api/services`, `POST /api/services/start` | sibling UI status / start |

Errors are `{"error": "..."}`. `writeErr` maps `store.ErrNotFound` to 404 and
known validation messages (invalid slug, duplicate, unknown state, unknown
kata project, ambiguous session, …) to 400; anything else is 500. Request
bodies are limited to 1 MiB.

## Guards

The server binds to loopback, but a browser can still be tricked into
talking to it, so `ServeHTTP` checks two things before routing:

- **Host header.** Only `localhost`, loopback IPs, the configured listen
  host, or a name given with `--allow-host` (compared case-insensitively)
  are accepted (403 otherwise). This defeats DNS rebinding.
- **JSON-only state changes.** Any non-GET/HEAD request must have
  `Content-Type: application/json` (415 otherwise). A cross-site page can
  only send JSON after a CORS preflight, which the server never approves, so
  cross-site form posts cannot change projects.

## Caching and snapshots

`Server.World(fresh)` holds one `sources.World` for 20 seconds; `refresh=1`
forces a new collection (the header's Refresh button sets it). Mutations
only touch the store, so they show up immediately without re-collecting.

`SnapshotLoop` records a snapshot of every project at start-up when the last
one is older than the interval, then on every tick. The interval is
`--snapshot-every` (default 6h, 0 disables). `--services` starts missing
sibling UIs before serving.

## Client

`static/app.js` is a hash-routed single page:

- `#/` — portfolio: tiles (projects, open issues, closed, sessions, cost,
  tokens, unassigned sessions, sinks), filters (text, state, health, include
  archived), and a table sortable by any column (`COLUMNS`), with health
  badges whose flags show on hover. Sort, filter, and search live in the hash.
  When the portfolio has sinks, a Sinks card below the table lists each one
  (sessions, cost, tokens, last activity) linking to bossman filtered by its
  tag; the Sinks tile and card are absent otherwise.
- `#/p/<slug>` — project: header with health, state selector (asks for a
  reason, logged), edit dialog; metric tiles; flags; components with summary,
  problem, items (kata attention issues, recent sessions, unmerged branches)
  and deep links; attach form with a custom-kind option and a raw-attach
  fallback when normalization fails; history; log with notes; archive/delete.
- `#/discover` — candidates with evidence and an Adopt button per row.

DOM is built with `h()` from text nodes, never `innerHTML`, so tool output
cannot inject markup. History is drawn as small multiples (open issues,
closed issues, sessions, total cost), one series each, with a hover marker
and tooltip; the y scale has 25% headroom so a flat series does not look
maxed out. Theme and last sort are per-browser preferences in
`localStorage`, wrapped so the UI still works when storage is unavailable.

## Behind a reverse proxy

`web.Options` (from `serve --allow-host` and `--user-header`) lets the UI run
behind a proxy that signs people in, such as Google IAP:

- `AllowHosts` adds the proxy's public names to the Host check.
- `UserHeader` names a header the proxy sets to the signed-in user
  (`X-Goog-Authenticated-User-Email` for IAP). `Server.Viewer` reads it and
  strips everything up to the last `:` (IAP's `accounts.google.com:`
  prefix). A request without it gets 401 before routing, so traffic that
  bypasses the proxy fails closed.
- Every handler that writes goes through `storeFor(r)`, which is
  `Store.As(viewer)`: a copy of the store sharing its database with `Actor`
  set to the viewer. Events are attributed per request, and concurrent
  viewers never change the actor of the shared store. `/api/meta` reports
  the actor a request would write as.

The header is only as trustworthy as the network, so it is meant for an
address only the proxy can reach. When serving behind a proxy, point
`[services]` at the sibling UIs' public addresses so links work.

## Testing

`internal/web/web_test.go` drives the API through `ServeHTTP` with a fake
runner: create, duplicate, attach (including an unknown kata project → 400),
state, note, project detail, portfolio, rename, delete, and both guards.
`TestBehindProxy` covers the public name, the 401 without a user header, and
events attributed to two different signed-in users.
