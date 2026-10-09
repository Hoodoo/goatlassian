"use strict";

// ---------- helpers ----------

const $ = (sel, root = document) => root.querySelector(sel);
const view = $("#view");
const tooltip = $("#tooltip");

// h builds DOM without innerHTML, so tool output can never inject markup.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "value") el.value = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

const SVGNS = "http://www.w3.org/2000/svg";
function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null) el.setAttribute(k, v);
  for (const c of children.flat()) if (c != null) el.append(c);
  return el;
}

async function api(path, opts = {}) {
  const init = { method: opts.method || "GET", headers: {} };
  if (opts.body !== undefined || init.method !== "GET") {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(opts.body ?? {});
  }
  const res = await fetch(path, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function toast(msg, err) {
  const t = h("div", { class: "toast" + (err ? " err" : ""), role: "status" }, msg);
  document.body.append(t);
  setTimeout(() => t.remove(), err ? 6000 : 2500);
}

const errorBox = (e) => h("div", { class: "error-box" }, String(e.message || e));

function ago(iso) {
  if (!iso || iso.startsWith("0001")) return "never";
  const d = (Date.now() - new Date(iso)) / 1000;
  if (d < 60) return "just now";
  if (d < 3600) return Math.floor(d / 60) + "m ago";
  if (d < 172800) return Math.floor(d / 3600) + "h ago";
  if (d < 86400 * 60) return Math.floor(d / 86400) + "d ago";
  return new Date(iso).toLocaleDateString();
}
const when = (iso) => (iso && !iso.startsWith("0001") ? new Date(iso).toLocaleString() : "");
const money = (v) => (!v ? "–" : v >= 100 ? "$" + v.toFixed(0) : "$" + v.toFixed(2));
function compact(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n || 0);
}

function showTip(ev, ...rows) {
  tooltip.replaceChildren(...rows);
  tooltip.hidden = false;
  const r = tooltip.getBoundingClientRect();
  let x = ev.clientX + 14, y = ev.clientY + 14;
  if (x + r.width > innerWidth - 8) x = ev.clientX - r.width - 14;
  if (y + r.height > innerHeight - 8) y = ev.clientY - r.height - 14;
  tooltip.style.left = x + "px";
  tooltip.style.top = y + "px";
}
function hideTip() { tooltip.hidden = true; }

const prefs = {
  get(k, d) { try { return localStorage.getItem("goat." + k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem("goat." + k, v); } catch { /* storage unavailable */ } },
};

// ---------- health ----------

const HEALTH = {
  alert: ["▲", "Needs attention"],
  warn: ["◆", "Warning"],
  info: ["●", "Info"],
  ok: ["✓", "OK"],
};
const RANK = { alert: 0, warn: 1, info: 2, ok: 3 };

function healthBadge(level, flags) {
  const [ic, label] = HEALTH[level] || HEALTH.ok;
  const el = h("span", { class: "health " + level }, h("span", { class: "ic", "aria-hidden": "true" }, ic), label);
  if (flags && flags.length) {
    el.addEventListener("mousemove", (ev) => showTip(ev, ...flags.map((f) => h("div", {}, `[${f.level}] ${f.message}`))));
    el.addEventListener("mouseleave", hideTip);
  }
  return el;
}

const flagChips = (flags) => flags.map((f) => h("span", { class: "chip flag-" + f.level, title: f.message }, f.code));

// ---------- meta, services, routing ----------

let META = null;
async function meta() {
  if (!META) META = await api("/api/meta");
  return META;
}

async function renderServices() {
  const box = $("#services");
  try {
    const list = await api("/api/services");
    const down = list.filter((x) => !x.up);
    box.replaceChildren(
      ...list.map((x) =>
        h("a", { class: "svc", href: x.up ? x.url : null, target: "_blank", rel: "noopener", title: x.up ? `${x.name} at ${x.url}` : `${x.name} is down: ${x.problem}\nstart: ${x.start}` },
          h("span", { class: "dot " + (x.up ? "up" : "down"), "aria-hidden": "true" }), x.name, x.up ? "" : " (down)")),
      down.length ? h("button", { class: "link", type: "button", onclick: startServices, title: down.map((x) => x.start).join("\n") }, "start") : null,
    );
    box.style.display = "inline-flex";
    box.style.gap = "10px";
  } catch (e) {
    box.replaceChildren(h("span", { class: "status-bad" }, "services: " + e.message));
  }
}

async function startServices() {
  try {
    await api("/api/services/start", { method: "POST" });
    toast("started missing services");
  } catch (e) {
    toast(e.message, true);
  }
  renderServices();
}

let refreshNext = false;
function q(fresh) {
  const r = fresh || refreshNext ? "refresh=1" : "";
  refreshNext = false;
  return r;
}

function route() {
  hideTip();
  const hash = location.hash.replace(/^#/, "") || "/";
  const [path, query] = hash.split("?");
  const params = new URLSearchParams(query || "");
  for (const a of document.querySelectorAll("[data-nav]")) {
    a.classList.toggle("active", (a.dataset.nav === "discover" && path.startsWith("/discover")) || (a.dataset.nav === "portfolio" && path === "/"));
  }
  if (path.startsWith("/p/")) return renderProject(decodeURIComponent(path.slice(3)));
  if (path.startsWith("/discover")) return renderDiscover(params);
  return renderPortfolio(params);
}
window.addEventListener("hashchange", route);

function setQuery(base, params) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) p.set(k, v);
  const next = "#" + base + (p.toString() ? "?" + p : "");
  if (location.hash !== next) history.replaceState(null, "", next);
}

// ---------- portfolio ----------

const COLUMNS = [
  { key: "project", label: "Project", sort: (r) => r.project.slug, asc: true },
  { key: "state", label: "State", sort: (r) => r.project.state, asc: true },
  { key: "health", label: "Health", sort: (r) => RANK[r.health] * 1e13 - new Date(r.last_activity || 0), asc: true },
  { key: "activity", label: "Last activity", sort: (r) => -new Date(r.last_activity || 0), asc: true },
  { key: "open", label: "Open", num: true, sort: (r) => r.metrics.issues_open },
  { key: "attn", label: "Attention", num: true, sort: (r) => r.metrics.issues_stuck + r.metrics.issues_needs_human + r.metrics.issues_overdue },
  { key: "closed", label: "Closed ·Nd", num: true, sort: (r) => r.metrics.issues_closed_recent },
  { key: "sessions", label: "Sessions ·Nd", num: true, sort: (r) => r.metrics.sessions_recent },
  { key: "cost", label: "Cost", num: true, sort: (r) => r.metrics.cost_usd },
  { key: "costr", label: "Cost ·Nd", num: true, sort: (r) => r.metrics.cost_recent_usd },
  { key: "tokens", label: "Tokens", num: true, sort: (r) => r.metrics.tokens },
  { key: "wiki", label: "Wiki drift", num: true, sort: (r) => r.metrics.wiki_drift },
];

async function renderPortfolio(params) {
  const sortKey = params.get("sort") || prefs.get("sort", "health");
  const dir = params.get("dir") || (COLUMNS.find((c) => c.key === sortKey)?.asc ? "asc" : "desc");
  const all = params.get("all") === "1";
  const state = params.get("state") || "";
  const health = params.get("health") || "";
  const search = params.get("q") || "";
  if (!view.firstChild) view.replaceChildren(h("p", { class: "muted" }, "Reading kata, owcli, bossman, and git…"));
  let pf;
  try {
    pf = await api("/api/portfolio?" + [all ? "all=1" : "", q()].filter(Boolean).join("&"));
  } catch (e) {
    return view.replaceChildren(errorBox(e));
  }
  $("#collected").textContent = "read " + ago(pf.collected_at);
  const N = pf.recent_days;
  const m = pf.totals;
  let rows = pf.reports.filter((r) => {
    if (state && r.project.state !== state) return false;
    if (health && r.health !== health) return false;
    if (search) {
      const hay = [r.project.slug, r.project.name, r.project.description, ...(r.project.tags || []), ...r.components.map((c) => c.ref)].join(" ").toLowerCase();
      if (!hay.includes(search.toLowerCase())) return false;
    }
    return true;
  });
  const col = COLUMNS.find((c) => c.key === sortKey) || COLUMNS[2];
  rows.sort((a, b) => {
    const x = col.sort(a), y = col.sort(b);
    const c = x < y ? -1 : x > y ? 1 : 0;
    return dir === "asc" ? c : -c;
  });
  const go = (patch) => {
    const next = { sort: sortKey, dir, all: all ? "1" : "", state, health, q: search, ...patch };
    setQuery("/", next);
    route();
  };
  const attention = m.issues_stuck + m.issues_needs_human + m.issues_overdue;
  const maxCost = Math.max(...rows.map((r) => r.metrics.cost_usd), 0.01);
  const toolProblems = Object.entries(pf.tools || {}).filter(([, v]) => v.problem);
  const sinks = pf.sinks || [];

  const tiles = h("div", { class: "tiles" },
    tile("Projects", pf.reports.length, `${pf.reports.filter((r) => r.health === "alert").length} need attention`),
    tile("Open issues", m.issues_open, `${attention} stuck, waiting on a human, or overdue`),
    tile(`Closed · ${N}d`, m.issues_closed_recent, `${m.issues_closed} closed in all`),
    tile(`Sessions · ${N}d`, m.sessions_recent, `${m.sessions} in all · ${m.interventions} interventions`),
    tile(`Cost · ${N}d`, money(m.cost_recent_usd), `${money(m.cost_usd)} in all`),
    tile(`Tokens · ${N}d`, compact(m.tokens_recent), `${compact(m.tokens)} in all`),
    pf.unassigned.sessions ? tile("Unassigned sessions", pf.unassigned.sessions, `${money(pf.unassigned.cost_usd)} · see Discover`) : null,
    sinks.length ? tile("Sinks", sinks.reduce((n, k) => n + k.sessions, 0) + " sessions",
      `${money(sinks.reduce((c, k) => c + k.cost_usd, 0))} in ${sinks.length} sink${sinks.length === 1 ? "" : "s"}`) : null,
  );
  // Sessions tagged sink:<name> in bossman that no project claims.
  const sinkCard = sinks.length ? h("div", { class: "card" }, h("h2", {}, "Sinks"),
    h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, ["Sink", "Sessions", "Cost", "Tokens", "Last activity"].map((l, i) => h("th", { scope: "col", class: i && i < 4 ? "num" : "" }, l)))),
      h("tbody", {}, sinks.map((k) => h("tr", {},
        h("td", {}, h("a", { href: k.url, target: "_blank", rel: "noopener", title: "Open in bossman" }, k.name + " ↗")),
        h("td", { class: "num" }, k.sessions),
        h("td", { class: "num" }, money(k.cost_usd)),
        h("td", { class: "num" }, k.tokens ? compact(k.tokens) : "–"),
        h("td", { title: when(k.last_activity) }, ago(k.last_activity)))))))) : null;

  const searchBox = h("input", { type: "search", placeholder: "Filter by name, tag, path…", value: search });
  searchBox.addEventListener("input", debounce(() => go({ q: searchBox.value }), 250));
  const sel = (value, opts, onchange, label) =>
    h("label", {}, label, h("select", { onchange: (e) => onchange(e.target.value) }, opts.map(([v, t]) => h("option", { value: v, selected: v === value }, t))));
  const filters = h("div", { class: "filters" },
    searchBox,
    sel(state, [["", "Any state"], ...(META?.states || []).map((x) => [x, x])], (v) => go({ state: v, all: v === "archived" ? "1" : all ? "1" : "" }), "State"),
    sel(health, [["", "Any health"], ["alert", "Needs attention"], ["warn", "Warning"], ["info", "Info"], ["ok", "OK"]], (v) => go({ health: v }), "Health"),
    h("label", {}, h("input", { type: "checkbox", checked: all, onchange: (e) => go({ all: e.target.checked ? "1" : "" }) }), "Include archived"),
  );

  const head = h("tr", {}, COLUMNS.map((c) => {
    const label = c.label.replace("N", N);
    const arrow = c.key === sortKey ? h("span", { class: "arrow" }, dir === "asc" ? " ▲" : " ▼") : null;
    return h("th", {
      class: "sortable" + (c.num ? " num" : ""), scope: "col", "aria-sort": c.key === sortKey ? (dir === "asc" ? "ascending" : "descending") : null,
      onclick: () => { prefs.set("sort", c.key); go({ sort: c.key, dir: c.key === sortKey ? (dir === "asc" ? "desc" : "asc") : c.asc ? "asc" : "desc" }); },
    }, label, arrow);
  }));
  const body = rows.map((r) => {
    const p = r.project, x = r.metrics;
    const attn = x.issues_stuck + x.issues_needs_human + x.issues_overdue;
    return h("tr", { class: "clickable", onclick: () => (location.hash = "#/p/" + encodeURIComponent(p.slug)) },
      h("td", { class: "proj" },
        h("div", { class: "title" }, h("a", { href: "#/p/" + encodeURIComponent(p.slug), onclick: (e) => e.stopPropagation() }, p.name)),
        h("div", { class: "sub" }, [p.slug !== p.name ? p.slug : null, p.description].filter(Boolean).join(" · ")),
        (p.tags || []).map((t) => h("span", { class: "chip tag" }, t))),
      h("td", {}, h("span", { class: "chip state-" + p.state }, p.state)),
      h("td", {}, healthBadge(r.health, r.flags), h("div", {}, flagChips(r.flags))),
      h("td", { title: when(r.last_activity) }, ago(r.last_activity), r.last_activity_by ? h("div", { class: "muted" }, r.last_activity_by) : null),
      h("td", { class: "num" }, x.issues_open || "–"),
      h("td", { class: "num" + (attn ? " status-bad" : "") }, attn || "–"),
      h("td", { class: "num" }, x.issues_closed_recent || "–"),
      h("td", { class: "num" }, x.sessions_recent || "–", h("span", { class: "muted" }, x.sessions ? ` / ${x.sessions}` : "")),
      h("td", { class: "num" }, h("div", { class: "inline-bar" },
        h("div", { class: "track" }, h("div", { class: "fill", style: `width:${(100 * x.cost_usd) / maxCost}%` })),
        h("span", { class: "v" }, money(x.cost_usd)))),
      h("td", { class: "num" }, money(x.cost_recent_usd)),
      h("td", { class: "num" }, x.tokens ? compact(x.tokens) : "–"),
      h("td", { class: "num" }, x.wiki_drift < 0 ? "–" : "+" + x.wiki_drift),
    );
  });
  view.replaceChildren(
    h("div", { class: "stack" },
      toolProblems.length ? h("div", { class: "error-box" }, toolProblems.map(([k, v]) => h("div", {}, `${k}: ${v.problem}`))) : null,
      tiles,
      filters,
      rows.length
        ? h("div", { class: "table-wrap" }, h("table", {}, h("thead", {}, head), h("tbody", {}, body)))
        : h("div", { class: "card empty" },
            pf.reports.length ? "No project matches these filters." : h("span", {}, "No projects yet. ", h("a", { href: "#/discover" }, "Discover"), " what kata, owcli, and bossman already know, or create one.")),
      sinkCard,
    ),
  );
  if (search) { searchBox.focus(); searchBox.setSelectionRange(search.length, search.length); }
}

function tile(label, value, sub) {
  return h("div", { class: "tile" }, h("div", { class: "label" }, label), h("div", { class: "value" }, value), sub ? h("div", { class: "sub" }, sub) : null);
}

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

// ---------- project ----------

async function renderProject(slug) {
  let d;
  try {
    d = await api("/api/projects/" + encodeURIComponent(slug) + "?" + q());
  } catch (e) {
    return view.replaceChildren(h("p", {}, h("a", { href: "#/" }, "← Portfolio")), errorBox(e));
  }
  await meta();
  $("#collected").textContent = "read " + ago(d.collected_at);
  const r = d.report, p = r.project, m = r.metrics, N = d.recent_days;
  const reload = () => renderProject(p.slug);

  const stateSel = h("select", { "aria-label": "Lifecycle state" }, META.states.map((x) => h("option", { value: x, selected: x === p.state }, x)));
  stateSel.addEventListener("change", async () => {
    const why = prompt(`Why move ${p.slug} from ${p.state} to ${stateSel.value}? (kept in the log; optional)`);
    if (why === null) { stateSel.value = p.state; return; }
    try { await api(`/api/projects/${enc(p.slug)}/state`, { method: "POST", body: { state: stateSel.value, why } }); toast("state: " + stateSel.value); reload(); }
    catch (e) { toast(e.message, true); stateSel.value = p.state; }
  });

  const header = h("div", { class: "detail-head" },
    h("div", {},
      h("p", { style: "margin:0 0 6px" }, h("a", { href: "#/" }, "← Portfolio")),
      h("h1", {}, p.name, " ", h("span", { class: "muted mono" }, p.slug)),
      p.description ? h("p", { class: "edit-desc" }, p.description) : null,
      h("div", {}, (p.tags || []).map((t) => h("span", { class: "chip tag" }, t))),
    ),
    h("div", { class: "row" },
      healthBadge(r.health, r.flags),
      h("label", { class: "secondary" }, "State ", stateSel),
      h("button", { type: "button", class: "ghost", onclick: () => editDialog(p) }, "Edit"),
    ),
  );

  const tiles = h("div", { class: "tiles" },
    tile("Last activity", ago(r.last_activity), r.last_activity_by ? "from " + r.last_activity_by : "nothing recorded"),
    tile("Open issues", m.issues_open, `${m.issues_stuck + m.issues_needs_human + m.issues_overdue} need attention · ${m.issues_blocked} blocked`),
    tile(`Closed · ${N}d`, m.issues_closed_recent, `${m.issues_closed} in all`),
    tile(`Sessions · ${N}d`, m.sessions_recent, `${m.sessions} in all · ${m.agent_hours.toFixed(1)} agent-hours`),
    tile(`Cost · ${N}d`, money(m.cost_recent_usd), `${money(m.cost_usd)} in all`),
    tile("Tokens", compact(m.tokens), `${compact(m.tokens_recent)} in ${N}d · ${compact(m.output_tokens)} output`),
    tile("Interventions", m.interventions, `${m.tool_errors} tool errors`),
  );

  const flags = r.flags.length
    ? h("div", { class: "card" }, h("h2", {}, "Flags"), h("ul", { class: "flags" }, r.flags.map((f) => h("li", {}, healthBadge(f.level), " ", f.message))))
    : null;

  const comps = h("div", { class: "card" },
    h("h2", {}, "Components"),
    r.components.length ? r.components.map((c) => componentView(p, c, reload)) : h("p", { class: "muted" }, "Nothing attached yet."),
    attachForm(p, reload),
  );

  const notes = h("textarea", { placeholder: "Add a note to the log…", "aria-label": "Note" });
  const log = h("div", { class: "card" },
    h("h2", {}, "Log"),
    h("div", { class: "row", style: "align-items:flex-start" }, notes,
      h("button", { type: "button", onclick: async () => {
        try { await api(`/api/projects/${enc(p.slug)}/notes`, { method: "POST", body: { text: notes.value } }); reload(); }
        catch (e) { toast(e.message, true); }
      } }, "Add note")),
    h("div", { class: "log", style: "margin-top:10px" }, d.events.map((e) =>
      h("div", { class: "ev " + e.kind },
        h("span", { class: "muted", title: when(e.at) }, new Date(e.at).toLocaleString([], { dateStyle: "short", timeStyle: "short" })),
        h("span", { class: "chip" }, e.kind),
        h("span", { class: "msg" }, e.message, e.actor ? h("span", { class: "muted" }, " — " + e.actor) : null)))),
  );

  const danger = h("div", { class: "card" }, h("h2", {}, "Remove"),
    h("p", { class: "muted" }, "Archiving keeps the history. Deleting removes this project's components, log, and snapshots from goatlassian; repositories, issues, wikis, and sessions are never touched."),
    h("div", { class: "row" },
      p.state !== "archived" ? h("button", { type: "button", class: "ghost", onclick: async () => {
        await api(`/api/projects/${enc(p.slug)}/state`, { method: "POST", body: { state: "archived" } }); reload();
      } }, "Archive") : null,
      h("button", { type: "button", class: "danger", onclick: async () => {
        if (!confirm(`Delete ${p.slug} and its log from goatlassian?`)) return;
        try { await api(`/api/projects/${enc(p.slug)}`, { method: "DELETE" }); location.hash = "#/"; }
        catch (e) { toast(e.message, true); }
      } }, "Delete")));

  view.replaceChildren(h("div", { class: "stack" },
    header, tiles, flags, comps,
    historyCard(d.snapshots),
    h("div", { class: "grid2" }, log, danger)));
}

const enc = encodeURIComponent;

function componentView(p, c, reload) {
  const label = c.label && c.label !== c.ref ? c.label : null;
  return h("div", { class: "comp" },
    h("div", { class: "comp-head" },
      h("span", { class: "kind" }, c.kind),
      h("span", { class: "ref mono" }, c.ref),
      label ? h("span", { class: "muted" }, label) : null,
      h("span", { class: "actions" },
        c.links.map((l) => h("a", { href: l.url, target: "_blank", rel: "noopener" }, l.label + " ↗")),
        h("button", { type: "button", class: "link", title: "Detach", onclick: async () => {
          if (!confirm(`Detach ${c.kind} ${c.ref} from ${p.slug}?`)) return;
          try { await api(`/api/projects/${enc(p.slug)}/components/${c.id}`, { method: "DELETE" }); reload(); }
          catch (e) { toast(e.message, true); }
        } }, "detach"))),
    c.problem ? h("div", { class: "problem" }, c.problem) : null,
    c.summary ? h("div", { class: "secondary" }, c.summary, c.last_activity && !c.last_activity.startsWith("0001") ? h("span", { class: "muted" }, " · " + ago(c.last_activity)) : null) : null,
    c.items.length ? h("ul", { class: "items" }, c.items.map((it) =>
      h("li", { class: it.level || "" },
        it.url ? h("a", { href: it.url, target: "_blank", rel: "noopener" }, it.title) : it.title,
        it.detail ? h("span", { class: "muted" }, " — " + it.detail) : null,
        it.at && !it.at.startsWith("0001") ? h("span", { class: "muted" }, " · " + ago(it.at)) : null))) : null,
  );
}

const REF_HINT = {
  git: "/path/to/repository",
  kata: "kata project name",
  "owcli-wiki": "wiki ID or name",
  "owcli-workspace": "workspace ID or name",
  sessions: "/directory sessions ran in",
  session: "session key or ID prefix",
  link: "https://…",
};

function attachForm(p, reload) {
  const kind = h("select", { "aria-label": "Kind" }, [...META.kinds, "other…"].map((k) => h("option", { value: k }, k)));
  const custom = h("input", { type: "text", placeholder: "kind, e.g. slack, pr, email", hidden: true, "aria-label": "Custom kind" });
  const ref = h("input", { type: "text", placeholder: REF_HINT.git, "aria-label": "Ref", style: "flex:2 1 260px" });
  const label = h("input", { type: "text", placeholder: "label (optional)", "aria-label": "Label" });
  kind.addEventListener("change", () => {
    custom.hidden = kind.value !== "other…";
    ref.placeholder = REF_HINT[kind.value] || "ref (a URL is linked)";
  });
  const submit = async (raw) => {
    const k = kind.value === "other…" ? custom.value.trim() : kind.value;
    try {
      await api(`/api/projects/${enc(p.slug)}/components`, { method: "POST", body: { kind: k, ref: ref.value.trim(), label: label.value.trim(), raw } });
      toast("attached " + k);
      reload();
    } catch (e) {
      if (!raw && confirm(e.message + "\n\nAttach it anyway, as given?")) return submit(true);
      if (raw) toast(e.message, true);
    }
  };
  return h("div", { style: "margin-top:12px" }, h("h2", {}, "Attach"),
    h("div", { class: "row" }, kind, custom, ref, label, h("button", { type: "button", onclick: () => submit(false) }, "Attach")));
}

// ---------- history: small multiples, one series each ----------

function historyCard(snaps) {
  const pts = snaps.map((x) => ({ at: new Date(x.at), d: x.data })).filter((x) => x.d && x.d.metrics);
  if (pts.length < 2) {
    return h("div", { class: "card" }, h("h2", {}, "History"),
      h("p", { class: "muted" }, pts.length ? "One snapshot so far; the trend appears after the next one." : "No snapshots yet. goatlassian serve records one every few hours; goatlassian snapshot records one now."));
  }
  const series = [
    ["Open issues", (m) => m.issues_open, (v) => String(v)],
    ["Closed issues", (m) => m.issues_closed, (v) => String(v)],
    ["Sessions", (m) => m.sessions, (v) => String(v)],
    ["Total cost", (m) => m.cost_usd, money],
  ];
  return h("div", { class: "card" }, h("h2", {}, "History"),
    h("div", { class: "minis" }, series.map(([title, get, fmt]) => mini(title, pts.map((p) => ({ at: p.at, v: get(p.d.metrics) })), fmt))));
}

function mini(title, pts, fmt) {
  const W = 300, H = 70, pad = 4;
  const t0 = pts[0].at.getTime(), t1 = pts[pts.length - 1].at.getTime() || t0 + 1;
  const vmax = Math.max(...pts.map((p) => p.v), 1) * 1.25; // headroom so a flat series does not read as maxed out
  const x = (t) => pad + ((W - 2 * pad) * (t - t0)) / Math.max(t1 - t0, 1);
  const y = (v) => H - pad - ((H - 2 * pad) * v) / vmax;
  const d = pts.map((p, i) => (i ? "L" : "M") + x(p.at.getTime()).toFixed(1) + " " + y(p.v).toFixed(1)).join(" ");
  const area = d + ` L${x(t1).toFixed(1)} ${H - pad} L${x(t0).toFixed(1)} ${H - pad} Z`;
  const marker = s("circle", { class: "marker", r: 4, cx: -10, cy: -10, visibility: "hidden" });
  const hit = s("rect", { class: "hit", x: 0, y: 0, width: W, height: H });
  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: "none", role: "img", "aria-label": `${title} over time` },
    s("line", { class: "base", x1: pad, x2: W - pad, y1: H - pad, y2: H - pad }),
    s("path", { class: "area", d: area }), s("path", { class: "line", d, "vector-effect": "non-scaling-stroke" }), marker, hit);
  hit.addEventListener("mousemove", (ev) => {
    const r = svg.getBoundingClientRect();
    const tx = t0 + ((ev.clientX - r.left) / r.width) * (t1 - t0);
    let best = pts[0];
    for (const p of pts) if (Math.abs(p.at - tx) < Math.abs(best.at - tx)) best = p;
    marker.setAttribute("cx", x(best.at.getTime()));
    marker.setAttribute("cy", y(best.v));
    marker.setAttribute("visibility", "visible");
    showTip(ev, h("div", { class: "t-head" }, best.at.toLocaleString()), h("div", { class: "t-row" }, h("span", {}, title), h("b", {}, fmt(best.v))));
  });
  hit.addEventListener("mouseleave", () => { marker.setAttribute("visibility", "hidden"); hideTip(); });
  const last = pts[pts.length - 1].v, first = pts[0].v;
  const delta = last - first;
  return h("div", { class: "mini" }, h("h3", {}, title),
    h("div", {}, h("span", { class: "big" }, fmt(last)), " ", h("span", { class: "muted" }, delta ? `${delta > 0 ? "+" : "−"}${fmt(Math.abs(delta))} since ${pts[0].at.toLocaleDateString()}` : "unchanged")),
    svg);
}

// ---------- dialogs ----------

function dialog(title, fields, onsave) {
  const dlg = h("dialog", {}, h("h2", {}, title), h("div", { class: "form-grid" }, fields.flatMap(([l, el]) => [h("label", {}, l), el])));
  const err = h("div", { class: "status-bad", hidden: true });
  dlg.append(err, h("div", { class: "dialog-actions" },
    h("button", { type: "button", class: "ghost", onclick: () => dlg.close() }, "Cancel"),
    h("button", { type: "button", onclick: async () => {
      try { await onsave(); dlg.close(); } catch (e) { err.textContent = e.message; err.hidden = false; }
    } }, "Save")));
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
}

const tagList = (v) => v.split(/[\s,]+/).map((t) => t.replace(/^#/, "")).filter(Boolean);

function editDialog(p) {
  const name = h("input", { type: "text", value: p.name });
  const slug = h("input", { type: "text", value: p.slug });
  const desc = h("textarea", { value: p.description });
  const tags = h("input", { type: "text", value: (p.tags || []).join(", ") });
  dialog("Edit " + p.slug, [["Name", name], ["Slug", slug], ["Description", desc], ["Tags", tags]], async () => {
    const np = await api("/api/projects/" + enc(p.slug), { method: "PATCH", body: { name: name.value, slug: slug.value, description: desc.value, tags: tagList(tags.value) } });
    if (np.slug !== p.slug) location.hash = "#/p/" + enc(np.slug);
    else renderProject(np.slug);
  });
}

function newProjectDialog() {
  const name = h("input", { type: "text", placeholder: "Shop rewrite" });
  const slug = h("input", { type: "text", placeholder: "derived from the name" });
  const desc = h("textarea", {});
  const tags = h("input", { type: "text", placeholder: "web, client" });
  dialog("New project", [["Name", name], ["Slug", slug], ["Description", desc], ["Tags", tags]], async () => {
    const p = await api("/api/projects", { method: "POST", body: { name: name.value.trim(), slug: slug.value.trim(), description: desc.value, tags: tagList(tags.value) } });
    location.hash = "#/p/" + enc(p.slug);
  });
}

// ---------- discover ----------

async function renderDiscover(params) {
  const showAll = params.get("all") === "1";
  view.replaceChildren(h("p", { class: "muted" }, "Asking kata, owcli, and bossman what they know…"));
  let cs;
  try {
    cs = await api("/api/discover?" + q());
  } catch (e) {
    return view.replaceChildren(errorBox(e));
  }
  const rows = cs.filter((c) => showAll || (!c.claimed_by && !c.missing));
  const body = rows.map((c) => {
    const slug = h("input", { type: "text", value: c.slug, "aria-label": "Slug for " + c.root, style: "width:160px" });
    return h("tr", {},
      h("td", { class: "proj" }, h("div", { class: "title mono" }, c.root), h("div", {}, c.evidence.map((e) => h("div", { class: "muted" }, e)))),
      h("td", {}, c.components.map((x) => h("span", { class: "chip" }, x.kind))),
      h("td", { class: "num" }, c.sessions || "–"),
      h("td", { class: "num" }, money(c.cost_usd)),
      h("td", {}, c.claimed_by
        ? h("a", { href: "#/p/" + enc(c.claimed_by) }, c.claimed_by)
        : h("div", { class: "row" }, slug, h("button", { type: "button", onclick: async () => {
            try { const p = await api("/api/adopt", { method: "POST", body: { root: c.root, slug: slug.value.trim() } }); toast("adopted " + p.slug); renderDiscover(params); }
            catch (e) { toast(e.message, true); }
          } }, "Adopt"))));
  });
  view.replaceChildren(h("div", { class: "stack" },
    h("div", {}, h("h1", {}, "Discover"),
      h("p", { class: "secondary" }, "Directories kata, owcli, and bossman know about, grouped by repository. Adopting one creates a project with its repository, kata projects, wikis, and sessions.")),
    h("div", { class: "filters" }, h("label", {}, h("input", { type: "checkbox", checked: showAll, onchange: (e) => { setQuery("/discover", { all: e.target.checked ? "1" : "" }); route(); } }), "Show covered and missing directories")),
    rows.length
      ? h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Directory", "Would attach", "Sessions", "Cost", "Project"].map((t, i) => h("th", { class: i === 2 || i === 3 ? "num" : "", scope: "col" }, t)))),
          h("tbody", {}, body)))
      : h("div", { class: "card empty" }, "Every directory the tools know about is covered by a project.")));
}

// ---------- boot ----------

function applyTheme() {
  const t = prefs.get("theme", "");
  if (t) document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
}
$("#theme").addEventListener("click", () => {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : matchMedia("(prefers-color-scheme: dark)").matches;
  prefs.set("theme", dark ? "light" : "dark");
  applyTheme();
});
$("#refresh").addEventListener("click", () => { refreshNext = true; renderServices(); route(); });
$("#new-project").addEventListener("click", () => meta().then(newProjectDialog));
applyTheme();
meta().then(() => { renderServices(); route(); }, (e) => view.replaceChildren(errorBox(e)));
