"use strict";

const themeStorageKey = "ait-theme";
const validThemes = new Set(["system", "light", "dark"]);

function readTheme() {
  try {
    const theme = window.localStorage.getItem(themeStorageKey);
    return validThemes.has(theme) ? theme : "system";
  } catch (_) {
    return "system";
  }
}

function applyTheme(theme) {
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  } else {
    delete document.documentElement.dataset.theme;
  }
}

function saveTheme(theme) {
  applyTheme(theme);
  try { window.localStorage.setItem(themeStorageKey, theme); } catch (_) { /* Storage may be unavailable. */ }
}

const initialTheme = readTheme();
applyTheme(initialTheme);

const state = { query: "", cursor: "", loading: false };
const $ = (id) => document.getElementById(id);
const number = new Intl.NumberFormat();
const money = new Intl.NumberFormat(undefined, { style: "currency", currency: "USD", maximumFractionDigits: 4 });
const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const dateOnly = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });

function setText(id, value) { $(id).textContent = value; }
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
function tokenTotal(tokens) { return tokens && Number.isFinite(tokens.total) ? tokens.total : 0; }
function formatCost(micros) { return micros === null || micros === undefined ? "Unavailable" : money.format(micros / 1_000_000); }
function formatDate(value) { return value ? dateTime.format(new Date(value)) : "Unavailable"; }

function showNotice(message, error = false) {
  const notice = $("notice");
  notice.hidden = !message;
  notice.classList.toggle("error", error);
  notice.textContent = message || "";
}

async function api(path, options) {
  const response = await fetch(path, { headers: { Accept: "application/json" }, ...options });
  let payload;
  try { payload = await response.json(); } catch (_) { payload = null; }
  if (!response.ok) {
    throw new Error(payload?.error?.message || `Request failed (${response.status})`);
  }
  return payload;
}

function buildQuery() {
  const data = new FormData($("filters"));
  const params = new URLSearchParams();
  for (const [key, value] of data.entries()) {
    if (value) params.set(key, value);
  }
  if (!params.has("includeEstimates")) params.set("includeEstimates", "false");
  if (params.get("range") !== "custom") {
    params.delete("from"); params.delete("to");
  }
  return params.toString();
}

function renderSummary(summary) {
  setText("tokens", number.format(tokenTotal(summary.tokens)));
  setText("sessions-count", number.format(summary.sessions || 0));
  setText("events-count", `${number.format(summary.events || 0)} events`);
  setText("cost", formatCost(summary.costMicros));
	const c = summary.costCoverage || {};
	const costTokens = (c.pricedTokens || 0) + (c.unpricedTokens || 0);
	setText("cost-detail", costTokens ? `${Math.round((c.pricedTokens || 0) / costTokens * 100)}% of tokens priced · ${number.format(c.unpricedEvents || 0)} unknown-price events excluded` : "No priced token events");
  const t = summary.tokens || {};
  setText("token-detail", `Input ${number.format(t.inputUncached || 0)} · Cache read ${number.format(t.cacheRead || 0)} · Cache write ${number.format(t.cacheWrite || 0)} · Output ${number.format(t.output || 0)} · Reasoning ${number.format(t.reasoning || 0)}`);
  const q = summary.quality || {};
	setText("quality-mode", (q.estimated || 0) > 0 ? "Includes opt-in estimates" : "Reported and derived data");
  const total = (q.reported || 0) + (q.derived || 0) + (q.estimated || 0) + (q.legacy || 0);
  const measured = (q.reported || 0) + (q.derived || 0);
  setText("coverage", total ? `${Math.round(measured / total * 100)}%` : "No events");
  setText("coverage-detail", `Reported ${number.format(q.reported || 0)} · Derived ${number.format(q.derived || 0)} · Estimated ${number.format(q.estimated || 0)} · Legacy ${number.format(q.legacy || 0)}`);
  setText("range-label", `${dateOnly.format(new Date(summary.rangeFrom))} – ${dateOnly.format(new Date(summary.rangeTo))} · ${summary.timezone || $("tz").value}`);
  setText("freshness", summary.lastSuccessfulSync ? `Last synced ${formatDate(summary.lastSuccessfulSync)}` : "No successful sync recorded");
}

function svgElement(name, attrs = {}) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", name);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
  return node;
}

function renderSeries(payload) {
  const points = payload.points || [];
  const svg = $("chart");
  clear(svg);
  clear($("series-table"));
  $("chart-empty").hidden = points.length !== 0;
  svg.hidden = points.length === 0;
  const title = svgElement("title", { id: "chart-title" }); title.textContent = "Daily token usage"; svg.appendChild(title);
  const desc = svgElement("desc", { id: "chart-desc" }); desc.textContent = "Token totals over the selected date range."; svg.appendChild(desc);
  for (const point of points) {
    const row = document.createElement("tr");
    [dateOnly.format(new Date(point.start)), number.format(tokenTotal(point.tokens)), number.format(point.sessions || 0), formatCost(point.costMicros)].forEach((value) => {
      const cell = document.createElement("td"); cell.textContent = value; row.appendChild(cell);
    });
    $("series-table").appendChild(row);
  }
  if (!points.length) return;
  const width = 900, height = 280, left = 54, right = 16, top = 16, bottom = 38;
  const chartW = width - left - right, chartH = height - top - bottom;
  const max = Math.max(1, ...points.map((p) => tokenTotal(p.tokens)));
  for (let i = 0; i <= 4; i++) {
    const y = top + chartH * i / 4;
    svg.appendChild(svgElement("line", { class: "grid", x1: left, y1: y, x2: width - right, y2: y }));
    const label = svgElement("text", { x: left - 8, y: y + 4, "text-anchor": "end" });
    label.textContent = number.format(Math.round(max * (4 - i) / 4)); svg.appendChild(label);
  }
  const xy = points.map((p, i) => ({ x: points.length === 1 ? left + chartW / 2 : left + chartW * i / (points.length - 1), y: top + chartH * (1 - tokenTotal(p.tokens) / max), p }));
  const path = xy.map((v, i) => `${i ? "L" : "M"}${v.x},${v.y}`).join(" ");
  svg.appendChild(svgElement("path", { class: "area", d: `${path} L${xy.at(-1).x},${top + chartH} L${xy[0].x},${top + chartH} Z` }));
  svg.appendChild(svgElement("path", { class: "line", d: path }));
  for (const value of xy) {
    const point = svgElement("circle", { class: "point", cx: value.x, cy: value.y, r: 4, tabindex: 0 });
    const tip = svgElement("title"); tip.textContent = `${dateOnly.format(new Date(value.p.start))}: ${number.format(tokenTotal(value.p.tokens))} tokens`; point.appendChild(tip); svg.appendChild(point);
  }
  const labelIndexes = [...new Set([0, Math.floor((points.length - 1) / 2), points.length - 1])];
  for (const i of labelIndexes) {
    const label = svgElement("text", { x: xy[i].x, y: height - 10, "text-anchor": i === 0 ? "start" : i === points.length - 1 ? "end" : "middle" });
    label.textContent = dateOnly.format(new Date(points[i].start)); svg.appendChild(label);
  }
}

function sessionRow(session) {
  const row = document.createElement("tr");
  const values = [formatDate(session.updatedAt), session.agent || session.provider || "Unknown", session.model || "Unknown", number.format(tokenTotal(session.tokens)), session.measurement || "Unknown"];
  values.forEach((value, index) => {
    const cell = document.createElement("td");
    cell.textContent = value;
    if (index === 1 && session.isSubagent) cell.textContent += " (subagent)";
    row.appendChild(cell);
  });
  row.title = session.sourceSessionId ? `Source session ${session.sourceSessionId}` : "Session details unavailable";
  return row;
}

function renderSessions(page, append = false) {
  const table = $("session-table");
  if (!append) clear(table);
  for (const session of page.sessions || []) table.appendChild(sessionRow(session));
  state.cursor = page.nextCursor || "";
  $("more").hidden = !state.cursor;
  $("sessions-empty").hidden = table.children.length !== 0;
}

function renderBreakdown(payload) {
  const list = $("breakdown"); clear(list);
  const items = payload.items || [];
  for (const item of items) {
    const li = document.createElement("li");
    const key = document.createElement("strong"); key.textContent = item.key || "Unknown";
    const tokens = document.createElement("span"); tokens.textContent = number.format(tokenTotal(item.tokens));
    const detail = document.createElement("small"); detail.textContent = `${number.format(item.sessions || 0)} sessions · ${formatCost(item.costMicros)}`;
    li.append(key, tokens, detail); list.appendChild(li);
  }
  $("breakdown-empty").hidden = items.length !== 0;
}

function renderSync(status) {
  setText("sync-state", status.status || "Unknown");
  setText("sync-started", formatDate(status.startedAt));
  setText("sync-finished", formatDate(status.finishedAt));
  setText("sync-events", number.format(status.eventsCommitted || 0));
  setText("sync-sessions", number.format(status.sessionsCommitted || 0));
  setText("sync-skipped", number.format(status.skipped || 0));
  setText("sync-errors", number.format(status.errors || 0));
  const list = $("diagnostics"); clear(list);
  for (const diagnostic of status.diagnostics || []) { const li = document.createElement("li"); li.textContent = diagnostic; list.appendChild(li); }
}

function renderInventory(payload) {
  const table = $("inventory-table"); clear(table);
  for (const item of payload.items || []) {
    const row=document.createElement("tr");
    [item.provider || "Unknown", item.kind || "Unknown", item.display_name || "Unknown", item.scope || "Unknown", item.state || "Unknown"].forEach((value)=>{const cell=document.createElement("td");cell.textContent=value;row.appendChild(cell);});
    table.appendChild(row);
  }
  $("inventory-empty").hidden = table.children.length !== 0;
}

async function loadAll() {
  if (state.loading) return;
  state.loading = true; showNotice("");
  state.query = buildQuery(); state.cursor = "";
  try {
    const dimension = encodeURIComponent($("dimension").value);
    const [summary, series, sessions, breakdown, sync, inventory] = await Promise.all([
      api(`/api/v2/summary?${state.query}`), api(`/api/v2/series?interval=day&${state.query}`),
      api(`/api/v2/sessions?${state.query}`), api(`/api/v2/breakdowns?dimension=${dimension}&${state.query}`), api("/api/v2/sync/status"), api("/api/v2/inventory")
    ]);
    renderSummary(summary); renderSeries(series); renderSessions(sessions); renderBreakdown(breakdown); renderSync(sync); renderInventory(inventory);
  } catch (error) { showNotice(error.message, true); }
  finally { state.loading = false; }
}

async function runSync() {
  const button = $("sync"); button.disabled = true; showNotice("Syncing source records…");
  try {
    await api("/api/v2/sync", { method: "POST", body: "" });
    showNotice("Sync committed. Refreshing analytics…"); await loadAll();
  } catch (error) { showNotice(error.message, true); }
  finally { button.disabled = false; }
}

$("filters").addEventListener("submit", (event) => { event.preventDefault(); loadAll(); });
$("refresh").addEventListener("click", loadAll);
$("sync").addEventListener("click", runSync);
$("theme").value = initialTheme;
$("theme").addEventListener("change", () => saveTheme($("theme").value));
$("range").addEventListener("change", () => { const custom = $("range").value === "custom"; $("from").disabled = !custom; $("to").disabled = !custom; });
$("dimension").addEventListener("change", async () => {
  try { renderBreakdown(await api(`/api/v2/breakdowns?dimension=${encodeURIComponent($("dimension").value)}&${state.query || buildQuery()}`)); }
  catch (error) { showNotice(error.message, true); }
});
$("more").addEventListener("click", async () => {
  if (!state.cursor) return;
  try { renderSessions(await api(`/api/v2/sessions?${state.query}&cursor=${encodeURIComponent(state.cursor)}`), true); }
  catch (error) { showNotice(error.message, true); }
});

try { $("tz").value = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; } catch (_) { $("tz").value = "UTC"; }
if (window.EventSource) {
  const events = new EventSource("/api/v2/events");
  events.addEventListener("update", () => loadAll());
  events.onerror = () => setText("freshness", "Live updates disconnected; manual refresh is available");
}
loadAll();
