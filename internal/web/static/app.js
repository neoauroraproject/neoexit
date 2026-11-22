"use strict";

/* ============================ helpers ============================ */

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const fa = (n) => Number(n).toLocaleString("fa-IR");
const SVGNS = "http://www.w3.org/2000/svg";

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  setAttrs(el, attrs);
  append(el, kids);
  return el;
}
function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  setAttrs(el, attrs);
  append(el, kids);
  return el;
}
function setAttrs(el, attrs) {
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.setAttribute("class", v);
    else if (k === "style") el.style.cssText = v; // CSSOM: allowed by the panel's CSP
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
}
function append(el, kids) {
  for (const k of kids.flat()) {
    if (k == null || k === false) continue;
    el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  }
}
// replaceChildren, but skipping null/false like h() does.
function fill(el, ...kids) {
  el.replaceChildren();
  append(el, kids);
}
// Fixed, trusted SVG markup (icons) only.
function art(markup, cls) {
  const t = document.createElement("template");
  t.innerHTML = markup.trim();
  const el = t.content.firstChild;
  if (cls) el.setAttribute("class", cls);
  return el;
}
const ltr = (t, cls) => h("span", { class: "ltr" + (cls ? " " + cls : ""), dir: "ltr" }, t);

function ago(iso) {
  if (!iso || iso.startsWith("0001")) return "هنوز تست نشده";
  const sec = Math.max(0, (Date.now() - new Date(iso)) / 1000);
  if (sec < 50) return "همین الان";
  if (sec < 3600) return fa(Math.round(sec / 60)) + " دقیقه پیش";
  if (sec < 86400) return fa(Math.round(sec / 3600)) + " ساعت پیش";
  return fa(Math.round(sec / 86400)) + " روز پیش";
}
function clock(iso) {
  const d = new Date(iso);
  return d.toLocaleTimeString("fa-IR", { hour: "2-digit", minute: "2-digit" });
}
function day(iso) {
  const d = new Date(iso), now = new Date();
  if (d.toDateString() === now.toDateString()) return "امروز";
  const y = new Date(now - 864e5);
  if (d.toDateString() === y.toDateString()) return "دیروز";
  return d.toLocaleDateString("fa-IR", { month: "long", day: "numeric" });
}

let toastTimer;
function toast(msg, bad) {
  const t = $("#toast");
  t.textContent = msg;
  const kind = bad ? " bad" : " good";
  t.className = "toast show" + kind;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.className = "toast" + kind), 3200);
}

// The button that saved turns green with a check for a moment. The page
// may have been drawn again meanwhile, so find its new copy by its text.
function markSaved(btn, label) {
  const b = btn.isConnected ? btn : $$("#view button, dialog[open] button").find((x) => x.textContent === label);
  if (!b || b.classList.contains("saved")) return;
  b.classList.add("saved");
  b.textContent = "ذخیره شد";
  setTimeout(() => { if (b.isConnected) { b.classList.remove("saved"); b.textContent = label; } }, 1800);
}

async function api(method, path, body) {
  const r = await fetch("api/" + path, {
    method,
    headers: { "Content-Type": "application/json", "X-NeoExit": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  const data = await r.json().catch(() => ({}));
  if (r.status === 401 && path !== "login") { showLogin(); throw new Error(data.error || "وارد نشده‌اید"); }
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

async function act(btn, fn, done) {
  const label = btn ? btn.textContent : "";
  if (btn) btn.disabled = true;
  try {
    await fn();
    if (done) toast(done);
    await refresh(true);
    if (btn && done && btn.type === "submit") markSaved(btn, label);
  } catch (e) {
    toast(e.message, true);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast("کپی شد"); }
  catch { prompt("کپی کنید:", text); }
}

/* ============================ art ============================ */

const ART = {
  warp: `<svg viewBox="0 0 48 48" fill="none"><path d="M14 34h21a8 8 0 0 0 1.2-15.9A11 11 0 0 0 15.4 20 7 7 0 0 0 14 34z" fill="#ffd54d"/><path d="M14 34h21a8 8 0 0 0 1.2-15.9A11 11 0 0 0 15.4 20 7 7 0 0 0 14 34z" stroke="#2c2c2c" stroke-width="2"/><path d="M20 28h10m-4-4 4 4-4 4" stroke="#2c2c2c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
  direct4: `<svg viewBox="0 0 48 48" fill="none"><rect x="10" y="9" width="28" height="12" rx="3" fill="#2c2c2c"/><rect x="10" y="25" width="28" height="12" rx="3" stroke="#2c2c2c" stroke-width="2"/><circle cx="16" cy="15" r="1.8" fill="#ffd54d"/><circle cx="16" cy="31" r="1.8" fill="#2c2c2c"/><path d="M24 15h9M24 31h9" stroke="#8b8d91" stroke-width="2" stroke-linecap="round"/><path d="M24 41v-4" stroke="#2c2c2c" stroke-width="2"/></svg>`,
  direct6: `<svg viewBox="0 0 48 48" fill="none"><rect x="10" y="9" width="28" height="12" rx="3" stroke="#2c2c2c" stroke-width="2"/><rect x="10" y="25" width="28" height="12" rx="3" fill="#2c2c2c"/><circle cx="16" cy="15" r="1.8" fill="#2c2c2c"/><circle cx="16" cy="31" r="1.8" fill="#ffd54d"/><path d="M24 15h9M24 31h9" stroke="#8b8d91" stroke-width="2" stroke-linecap="round"/><path d="M24 41v-4" stroke="#2c2c2c" stroke-width="2"/></svg>`,
  socks5: `<svg viewBox="0 0 48 48" fill="none"><circle cx="24" cy="24" r="14" fill="#ffd54d"/><path d="M18 20v-5m12 5v-5M15 20h18v4a9 9 0 0 1-18 0v-4zM24 33v6" stroke="#2c2c2c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
  psiphon: `<svg viewBox="0 0 48 48" fill="none"><circle cx="24" cy="26" r="5" fill="#ffd54d" stroke="#2c2c2c" stroke-width="2"/><path d="M15.5 17.5a12 12 0 0 1 17 0M10.5 12.5a19 19 0 0 1 27 0" stroke="#2c2c2c" stroke-width="2" stroke-linecap="round"/><path d="M24 31v9" stroke="#2c2c2c" stroke-width="2" stroke-linecap="round"/></svg>`,
  opera: `<svg viewBox="0 0 48 48" fill="none"><circle cx="24" cy="24" r="14" stroke="#2c2c2c" stroke-width="2"/><ellipse cx="24" cy="24" rx="6" ry="14" stroke="#2c2c2c" stroke-width="2"/><path d="M10 24h28" stroke="#2c2c2c" stroke-width="2"/><circle cx="35" cy="13" r="5" fill="#ffd54d" stroke="#2c2c2c" stroke-width="2"/></svg>`,
  ssh: `<svg viewBox="0 0 48 48" fill="none"><rect x="8" y="11" width="32" height="26" rx="5" fill="#2c2c2c"/><path d="M15 21l5 4-5 4" stroke="#ffd54d" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/><path d="M24 30h8" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/></svg>`,
  http: `<svg viewBox="0 0 48 48" fill="none"><rect x="9" y="12" width="30" height="24" rx="5" stroke="#2c2c2c" stroke-width="2"/><path d="M9 19h30" stroke="#2c2c2c" stroke-width="2"/><circle cx="14" cy="15.5" r="1.3" fill="#2c2c2c"/><path d="M17 27h14m-4-4 4 4-4 4" stroke="#2c2c2c" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><rect x="28" y="7" width="12" height="7" rx="3.5" fill="#ffd54d"/></svg>`,
};
const thumb = (kind) => h("span", { class: "thumb", "aria-hidden": "true" }, art(ART[kind] || ART.http));
const ICON = {
  dots: `<svg viewBox="0 0 24 24"><circle cx="5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.6" fill="currentColor" stroke="none"/></svg>`,
  plus: `<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>`,
  minus: `<svg viewBox="0 0 24 24"><path d="M5 12h14"/></svg>`,
  expand: `<svg viewBox="0 0 24 24"><path d="M4 9V4h5M20 15v5h-5M4 4l6 6M20 20l-6-6"/></svg>`,
  close: `<svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>`,
  edit: `<svg viewBox="0 0 24 24"><path d="M4 20h4L19 9l-4-4L4 16v4zM14 6l4 4"/></svg>`,
  copy: `<svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="3"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/></svg>`,
  spark: `<svg viewBox="0 0 24 24"><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6"/></svg>`,
  pin: `<svg viewBox="0 0 24 24"><path d="M14 2l8 8-3 1-4 4 1 5-2 2-5-5-5 5-1-1 5-5-5-5 2-2 5 1 4-4z"/></svg>`,
};
const icon = (name) => art(ICON[name]);

const AVATAR = {
  chatgpt: ["GP", "#e3f1ea", "#1f6a4f"], claude: ["CL", "#f6e3d6", "#8a4521"], gemini: ["GE", "#e1e8fb", "#2c4ea3"],
  copilot: ["CO", "#e6e2f6", "#4b3c95"], perplexity: ["PX", "#dff0f1", "#1f6670"], grok: ["GK", "#ececec", "#2c2c2c"],
  youtube: ["YT", "#fbe1e1", "#a32020"], instagram: ["IG", "#f8e1ef", "#8a2463"],
};
const avatarOf = (sv) => AVATAR[sv.id] || [[...sv.name].slice(0, 2).join("").toUpperCase(), "#eee", "#333"];
// What a "geo" answer means differs for YouTube: Premium isn't sold there.
const stateWord = (svcId, st) => svcId === "youtube" && st === "geo" ? "بدون Premium" : STATE_WORD[st];

/* ============================ state ============================ */

let S = null;
let pollTimer;
let view = "dashboard";
const ui = { selected: undefined, zoom: 1, cx: 500, cy: 197, mapExpanded: false };

const STATE_WORD = { ok: "باز", geo: "کشور بسته", challenge: "کپچا", error: "وصل نشد", down: "بسته" };
const KIND_WORD = { warp: "WARP", socks5: "SOCKS5", http: "HTTP", direct4: "IPv4", direct6: "IPv6", psiphon: "سایفون", opera: "Opera", ssh: "SSH" };
const OPERA_WORD = { EU: "اروپا", AS: "آسیا", AM: "آمریکا" };
// What the small line under an exit's name says about its kind.
const kindLine = (x) => KIND_WORD[x.kind] + (x.plus ? "+" : "") +
  (x.kind === "opera" ? "، " + OPERA_WORD[x.region] : x.kind === "psiphon" ? "، " + (x.region ? country(x.region) : "بهترین کشور") : "");

const liveExit = (id) => (S.live.exits || []).find((x) => x.id === id) || { results: {}, trace: {} };
const liveSvc = (id) => (S.live.services || []).find((x) => x.id === id) || {};
const exitOf = (id) => S.exits.find((x) => x.id === id);
const country = (cc) => (COUNTRIES[cc] || [0, 0, cc || "نامشخص"])[2];
const usersOf = (exitId) => S.services.filter((sv) => sv.enabled && liveSvc(sv.id).exit === exitId);

async function refresh(force) {
  S = await api("GET", "state");
  render(force);
}

function poll() {
  clearTimeout(pollTimer);
  const busy = S && (S.live.running || (S.live.exits || []).some((x) => x.checking) || ["downloading", "restarting"].includes(S.update.state));
  pollTimer = setTimeout(async () => {
    if (!document.hidden) { try { await refresh(false); } catch {} }
    poll();
  }, busy ? 2000 : 6000);
}

/* ============================ theme, login ============================ */

try { const t = localStorage.getItem("neoexit-theme"); if (t) document.documentElement.dataset.theme = t; } catch {}
$("#theme").addEventListener("click", () => {
  const cur = document.documentElement.dataset.theme;
  const dark = cur ? cur === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = dark ? "light" : "dark";
  try { localStorage.setItem("neoexit-theme", dark ? "light" : "dark"); } catch {}
});

function showLogin() {
  $("#app").hidden = true;
  $("#login").hidden = false;
  clearTimeout(pollTimer);
  drawHL();
  $("#login-form [name=user]").focus();
}
$("#login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const f = ev.target;
  $("#login-error").textContent = "";
  try {
    await api("POST", "login", { user: f.user.value, pass: f.pass.value });
    f.pass.value = "";
    start();
  } catch (e) { $("#login-error").textContent = e.message; }
});
$("#logout").addEventListener("click", async () => { await api("POST", "logout").catch(() => {}); showLogin(); });
$("#check-now").addEventListener("click", (ev) => act(ev.currentTarget, () => api("POST", "check"), "تست همه‌ی خروجی‌ها شروع شد").then(poll));

/* ============================ sponsored ============================ */

// One message from the NeoAurora ads service on every page: a card on the
// dashboard, a line elsewhere and on the login page. The panel asks this
// server, never an outside host; each answer is one counted view.
const hl = { home: null, sidebar: null, at: {}, busy: {} };
async function loadHL(place) {
  if (hl.busy[place] || Date.now() - (hl.at[place] || 0) < 10 * 60e3) return;
  hl.busy[place] = true;
  try {
    const r = await fetch("api/spot?p=" + place, { credentials: "same-origin" }).then((x) => x.json());
    hl[place] = r.spot || null;
    hl.at[place] = Date.now();
  } catch { hl.at[place] = Date.now() - 8 * 60e3; } // try again in two minutes
  finally { hl.busy[place] = false; }
}
function hlCard(a, place) {
  const open = () => api("POST", "spot/open", { id: a.id, placement: place }).catch(() => {});
  return h("aside", { class: "hl hl-" + place, "aria-label": a.title },
    h("span", { class: "hl-mark", "aria-hidden": "true" }, icon("spark")),
    h("div", { class: "hl-text" },
      h("div", { class: "hl-top" }, h("span", { class: "hl-tag" }, "تبلیغ"),
        a.badge ? h("span", { class: "hl-badge" }, a.badge) : null,
        h("b", { class: "hl-title", dir: "auto" }, a.title)),
      a.body ? h("p", { class: "hl-body", dir: "auto" }, a.body) : null),
    a.link ? h("a", { class: "pill dark hl-go", href: a.link, target: "_blank", rel: "noopener noreferrer", onclick: $("#app").hidden ? null : open },
      a.button || "مشاهده") : null);
}
async function drawHL() {
  const login = !$("#login").hidden;
  const place = !login && view === "dashboard" ? "home" : "sidebar";
  const slot = $(login ? "#hl-login" : "#hl");
  await loadHL(place);
  const a = hl[place];
  const cur = slot.firstElementChild;
  if (cur && a && cur.dataset.id === a.id && cur.dataset.place === place) return;
  if (!a) { slot.replaceChildren(); return; }
  const card = hlCard(a, place);
  card.dataset.id = a.id;
  card.dataset.place = place;
  slot.replaceChildren(card);
}
setInterval(() => { if (!document.hidden) drawHL(); }, 60e3);

/* ============================ router ============================ */

const VIEWS = ["dashboard", "tests", "connect", "routing", "history", "settings"];
function route() {
  const v = location.hash.slice(1);
  view = VIEWS.includes(v) ? v : "dashboard";
  $$(".tabs a").forEach((a) => (a.dataset.view === view ? a.setAttribute("aria-current", "page") : a.removeAttribute("aria-current")));
  if (S) render(true);
  drawHL();
  window.scrollTo({ top: 0 });
}
window.addEventListener("hashchange", route);

// Views with forms are drawn once; the rest redraw on every poll.
function render(force) {
  if (!S) return;
  const v = $("#view");
  noticeVersion();
  const hasForm = view === "connect" || view === "settings" || view === "routing";
  if (!force && hasForm) { updateLive(); return; }
  closeMenu();
  const fn = { dashboard: viewDashboard, tests: viewTests, connect: viewConnect, routing: viewRouting, history: viewHistory, settings: viewSettings }[view];
  v.replaceChildren(...[updateBanner(), fn()].filter(Boolean));
  if (view === "routing") drawSaveBar();
  $("#check-now").disabled = S.live.running;
  if (view === "dashboard") placePop();
}
function updateLive() {
  $("#check-now").disabled = S.live.running;
  // Form pages aren't redrawn on polls; their live cards are.
  const uc = $("#update-card");
  if (uc && !uc.contains(document.activeElement)) fill(uc, updateCardBody());
}

function head(title, sub, ...tools) {
  return h("div", { class: "view-head" },
    h("div", {}, h("h1", {}, title), sub ? h("p", {}, sub) : null),
    tools.length ? h("div", { class: "head-tools" }, tools) : null);
}

/* ============================ dashboard ============================ */

function summaryLine() {
  if (S.live.running) return "در حال تست همه‌ی خروجی‌ها…";
  const on = S.services.filter((x) => x.enabled);
  const bad = on.filter((x) => liveSvc(x.id).state !== "ok");
  if (!on.length) return "هیچ سرویسی روشن نیست.";
  return (bad.length ? bad.map((x) => x.name).join("، ") + " مشکل دارد. " : "همه‌ی سرویس‌ها باز هستند. ") + "آخرین تست " + ago(S.live.last_run) + ".";
}

function viewDashboard() {
  const host = S.host.hostname || "این سرور";
  return h("div", {},
    head(h("span", {}, "خروجی‌های ", ltr(host)), summaryLine(),
      h("button", { class: "pill", type: "button", onclick: () => (location.hash = "#connect") }, "اتصال به 3x-ui"),
      h("button", { class: "pill dark", type: "button", onclick: () => openExit(null) }, icon("plus"), "افزودن خروجی")),
    h("div", { class: "dash" },
      h("div", { class: "dash-main" }, exitRow(), mapCard()),
      h("div", { class: "dash-side" }, gaugeCard(), servicesCard())));
}

function exitMeta(x) {
  const live = liveExit(x.id);
  if (!x.enabled) return [h("span", {}, "خاموش")];
  if (live.checking) return [h("span", {}, "در حال تست…")];
  if (live.err) return [h("span", { class: live.err.startsWith("در حال") ? "" : "bad" }, live.err)];
  if (!live.trace || !live.trace.ip) return [h("span", {}, "هنوز تست نشده")];
  const n = usersOf(x.id).length;
  return [h("span", {}, country(live.trace.country)), h("span", {}, ltr(live.trace.ms + " ms")), n ? h("span", {}, fa(n) + " سرویس") : null];
}

function exitRow() {
  const row = h("div", { class: "exits", role: "list", "aria-label": "خروجی‌ها" });
  for (const x of S.exits) {
    const card = h("div", {
      class: "card exit-card" + (x.enabled ? "" : " off"), role: "listitem", tabindex: "0",
      "aria-pressed": ui.selected === x.id ? "true" : "false",
      onclick: (ev) => { if (!ev.target.closest(".more")) selectExit(x.id); },
      ondblclick: (ev) => { if (!ev.target.closest(".more")) openExit(x); },
      title: "دو بار بزنید تا ویرایش شود",
      onkeydown: (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); selectExit(x.id); } },
    },
      thumb(x.kind),
      h("div", { style: "min-width:0" },
        h("div", { class: "name" }, x.name, x.plus ? h("span", { class: "chip" }, "+") : null),
        h("div", { class: "sub" }, exitMeta(x))),
      h("button", { class: "round small more", type: "button", "aria-label": "کارهای " + x.name, "aria-haspopup": "menu",
        onclick: (ev) => { ev.stopPropagation(); exitMenu(ev.currentTarget, x); } }, icon("dots")));
    row.append(card);
  }
  row.append(h("button", { class: "card exit-card add-card", type: "button", onclick: () => openExit(null) },
    h("span", { class: "thumb" }, icon("plus")), h("span", {}, "خروجی تازه")));
  return row;
}

function selectExit(id) {
  ui.selected = ui.selected === id ? null : id;
  render(true);
}

/* ---------- map ---------- */

function project(cc) {
  const c = COUNTRIES[cc];
  if (!c) return null;
  return { x: ((c[1] + 180) / 360) * WORLD.w, y: ((WORLD.lat0 - c[0]) / 360) * WORLD.w };
}

function mapPoints() {
  const pts = [];
  const home = S.exits.map((x) => liveExit(x.id)).find((l, i) => S.exits[i].kind === "direct4" && l.trace && l.trace.country)
    || S.exits.map((x) => liveExit(x.id)).find((l, i) => S.exits[i].kind.startsWith("direct") && l.trace && l.trace.country);
  const homeCC = home ? home.trace.country : null;
  const byCC = {};
  for (const x of S.exits) {
    const l = liveExit(x.id);
    if (!x.enabled || !l.trace || !l.trace.country) continue;
    (byCC[l.trace.country] ||= []).push(x);
  }
  for (const [cc, list] of Object.entries(byCC)) {
    const p = project(cc);
    if (!p) continue;
    const ring = list.length + (cc === homeCC ? 1 : 0);
    list.forEach((x, i) => {
      const k = i + (cc === homeCC ? 1 : 0);
      const a = (k / Math.max(ring, 1)) * Math.PI * 2 - Math.PI / 2;
      const r = ring > 1 ? 36 / Math.sqrt(ui.zoom) : 0;
      pts.push({ x, cc, px: p.x + r * Math.cos(a), py: p.y + r * Math.sin(a) });
    });
  }
  return { pts, home: homeCC ? { cc: homeCC, ...project(homeCC) } : null };
}

function viewBox() {
  const w = WORLD.w / ui.zoom, hh = WORLD.h / ui.zoom;
  const x = Math.min(Math.max(ui.cx - w / 2, 0), WORLD.w - w);
  const y = Math.min(Math.max(ui.cy - hh / 2, 0), WORLD.h - hh);
  return [x, y, w, hh];
}

function mapCard() {
  const { pts, home } = mapPoints();
  const vb = viewBox();
  const svg = s("svg", { class: "map-svg", viewBox: vb.join(" "), role: "img", "aria-label": "نقشه‌ی کشورهایی که خروجی‌ها از آن‌ها بیرون می‌روند", preserveAspectRatio: "xMidYMid meet" });
  svg.append(s("path", { class: "land", d: WORLD.dots }));
  const scale = 1.45 / Math.sqrt(ui.zoom);
  // Until someone picks one, show the card of the exit most services use.
  if (ui.selected === undefined && pts.length) {
    ui.selected = [...pts].sort((a, b) => usersOf(b.x.id).length - usersOf(a.x.id).length)[0].x.id;
  }
  if (home) {
    for (const p of pts) {
      if (Math.hypot(p.px - home.x, p.py - home.y) < 30) continue;
      const mx = (p.px + home.x) / 2, my = Math.min(p.py, home.y) - Math.abs(p.px - home.x) * 0.25 - 20;
      svg.append(s("path", { class: "arc", d: `M${home.x} ${home.y}Q${mx} ${my} ${p.px} ${p.py}` }));
    }
    const g = s("g", { class: "marker home", transform: `translate(${home.x} ${home.y}) scale(${scale})` },
      s("circle", { class: "ring", r: 22 }), s("circle", { class: "disc", r: 13 }), s("text", {}, "S"));
    g.append(s("title", {}, "این سرور، " + country(home.cc)));
    svg.append(g);
  }
  for (const p of pts) {
    const n = usersOf(p.x.id).length;
    const l = liveExit(p.x.id);
    const sel = ui.selected === p.x.id;
    const g = s("g", {
      class: "marker" + (sel ? " sel" : "") + (l.up ? "" : " down"), transform: `translate(${p.px} ${p.py}) scale(${scale})`,
      tabindex: "0", role: "button", "aria-label": p.x.name + "، " + country(p.cc),
      onclick: () => selectExit(p.x.id),
      onkeydown: (ev) => { if (ev.key === "Enter") selectExit(p.x.id); },
    }, s("circle", { class: "ring", r: 22 }), s("circle", { class: "disc", r: 14 }), s("text", {}, String(n)));
    g.dataset.id = p.x.id;
    g.dataset.x = p.px;
    g.dataset.y = p.py;
    svg.append(g);
  }

  const wrap = h("div", { class: "map-wrap", id: "map-wrap" }, svg,
    h("div", { class: "map-legend" },
      h("span", {}, h("i", { class: "home" }), "این سرور"),
      h("span", {}, h("i"), "خروجی؛ عدد، سرویس‌هایی است که از آن می‌روند")),
    h("div", { class: "map-tools" },
      h("button", { class: "round", type: "button", "aria-label": "بزرگ‌نمایی", onclick: () => zoom(1.6) }, icon("plus")),
      h("button", { class: "round", type: "button", "aria-label": "کوچک‌نمایی", onclick: () => zoom(1 / 1.6) }, icon("minus"))));
  dragMap(wrap, svg);

  return h("section", { class: "card map-card", "aria-labelledby": "map-title" },
    h("div", { class: "card-head" },
      h("h2", { class: "card-title", id: "map-title" }, "نقشه‌ی خروجی‌ها"),
      h("button", { class: "round", type: "button", "aria-label": "اندازه‌ی اول", title: "اندازه‌ی اول", onclick: () => { ui.zoom = 1; ui.cx = 500; ui.cy = 197; render(true); } }, icon("expand"))),
    wrap);
}

function zoom(f) {
  ui.zoom = Math.min(5, Math.max(1, ui.zoom * f));
  if (ui.selected) {
    const m = $(`.marker[data-id="${ui.selected}"]`);
    if (m && f > 1) { ui.cx = +m.dataset.x; ui.cy = +m.dataset.y; }
  }
  render(true);
}

function dragMap(wrap, svg) {
  let start = null;
  wrap.addEventListener("pointerdown", (ev) => {
    if (ev.target.closest(".marker, button, .pop") || ui.zoom === 1) return;
    start = { x: ev.clientX, y: ev.clientY, cx: ui.cx, cy: ui.cy };
    wrap.setPointerCapture(ev.pointerId);
    wrap.classList.add("dragging");
  });
  wrap.addEventListener("pointermove", (ev) => {
    if (!start) return;
    const r = svg.getBoundingClientRect();
    const k = (WORLD.w / ui.zoom) / r.width;
    const dir = getComputedStyle(document.body).direction === "rtl" ? 1 : 1;
    ui.cx = start.cx - (ev.clientX - start.x) * k * dir;
    ui.cy = start.cy - (ev.clientY - start.y) * k;
    const [x, y, w, hh] = viewBox();
    svg.setAttribute("viewBox", `${x} ${y} ${w} ${hh}`);
    placePop();
  });
  const end = () => {
    if (!start) return;
    start = null;
    wrap.classList.remove("dragging");
    const [x, y, w, hh] = viewBox();
    ui.cx = x + w / 2; ui.cy = y + hh / 2;
  };
  wrap.addEventListener("pointerup", end);
  wrap.addEventListener("pointercancel", end);
}

// The card above the selected marker, like a pinned tooltip.
function placePop() {
  const wrap = $("#map-wrap");
  if (!wrap) return;
  $(".pop", wrap.parentElement)?.remove();
  const id = ui.selected;
  const x = id && exitOf(id);
  const m = id && $(`.marker[data-id="${id}"]`, wrap);
  if (!x || !m) return;
  const l = liveExit(id);
  const users = usersOf(id);
  const pop = h("div", { class: "pop", role: "dialog", "aria-label": x.name },
    h("div", { class: "pop-head" }, thumb(x.kind),
      h("div", { style: "flex:1;min-width:0" }, h("b", {}, x.name), h("small", {}, kindLine(x))),
      h("button", { class: "round small", type: "button", "aria-label": "بستن", onclick: () => { ui.selected = null; render(true); } }, icon("close"))),
    h("hr", { class: "pop-rule" }),
    h("div", { class: "pop-row" }, h("span", {}, country(l.trace.country)), ltr(l.trace.ip || "—", "muted")),
    h("div", { class: "pop-row" }, h("span", { class: "muted" }, "پینگ تا کلادفلر"), ltr((l.trace.ms || 0) + " ms")),
    h("div", { class: "pop-row" }, h("span", { class: "muted" }, "سرویس‌ها"),
      h("span", {}, users.length ? users.map((u) => u.name).join("، ") : "هیچ‌کدام")));
  // On a phone the map is too small to float a card over it: it goes below.
  if (innerWidth <= 640) {
    pop.classList.add("below");
    wrap.after(pop);
    return;
  }
  wrap.append(pop);
  const r = m.getBoundingClientRect(), wr = wrap.getBoundingClientRect();
  const mx = r.left + r.width / 2 - wr.left, my = r.top - wr.top;
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let left = Math.min(Math.max(mx - pw / 2, 8), wr.width - pw - 8);
  let top = my - ph - 10;
  let origin = "center bottom";
  if (top < 8) { top = r.bottom - wr.top + 10; origin = "center top"; }
  pop.style.left = left + "px";
  pop.style.top = top + "px";
  pop.style.setProperty("--origin", origin);
}
window.addEventListener("resize", () => { if (view === "dashboard") placePop(); });

/* ---------- gauge + services ---------- */

function arc(a0, a1, r = 100, cx = 120, cy = 120) {
  const p = (a) => [cx + r * Math.cos(a), cy - r * Math.sin(a)];
  const [x0, y0] = p(a0), [x1, y1] = p(a1);
  return `M${x0.toFixed(2)} ${y0.toFixed(2)}A${r} ${r} 0 0 1 ${x1.toFixed(2)} ${y1.toFixed(2)}`;
}

function gaugeCard() {
  const on = S.services.filter((x) => x.enabled);
  const ok = on.filter((x) => liveSvc(x.id).state === "ok").length;
  const f = on.length ? ok / on.length : 0;
  const gap = 0.22;
  const split = Math.PI - f * Math.PI;
  const svg = s("svg", { viewBox: "0 0 240 132", "aria-hidden": "true" });
  if (f >= 1) svg.append(s("path", { class: "val", d: arc(Math.PI, 0) }));
  else if (f <= 0) svg.append(s("path", { class: "rest", d: arc(Math.PI, 0) }));
  else {
    svg.append(s("path", { class: "val", d: arc(Math.PI, Math.min(Math.PI, split + gap / 2)) }));
    svg.append(s("path", { class: "rest", d: arc(Math.max(0, split - gap / 2), 0) }));
  }
  const word = !on.length ? "سرویسی روشن نیست" : ok === on.length ? "همه باز هستند" : fa(on.length - ok) + " سرویس بسته است";
  return h("section", { class: "card gauge-card", "aria-label": "وضعیت دسترسی" },
    h("h2", { class: "card-title" }, "وضعیت دسترسی"),
    h("div", { class: "gauge" }, svg,
      h("div", { class: "gauge-num" }, ltr(Math.round(f * 100) + "%"), h("span", {}, word))));
}

function servicesCard() {
  const on = S.services.filter((x) => x.enabled);
  const ok = on.filter((x) => liveSvc(x.id).state === "ok").length;
  const list = h("ul", { class: "svc-list" });
  for (const sv of [...S.services].sort((a, b) => b.enabled - a.enabled)) {
    const live = liveSvc(sv.id);
    const ex = exitOf(live.exit);
    const lx = live.exit ? liveExit(live.exit) : null;
    const [ini, bg, fg] = avatarOf(sv);
    const st = sv.enabled ? live.state || "error" : "";
    const sub = !sv.enabled ? "خاموش" : !ex ? "خروجی ندارد" : (sv.pin ? "ثابت روی " : "از ") + ex.name;
    list.append(h("li", {}, h("button", {
      class: "svc-row" + (sv.enabled ? "" : " off"), type: "button", onclick: () => openService(sv.id),
      "aria-label": sv.name + "، " + (stateWord(sv.id, st) || "خاموش") + "، " + sub,
    },
      h("span", { class: "svc-icon", "aria-hidden": "true" }, icon(sv.pin ? "pin" : "spark")),
      h("span", { class: "svc-avatar", style: `background:${bg};color:${fg}`, "aria-hidden": "true" }, ini),
      h("span", {}, h("b", {}, ltr(sv.name)), h("small", {}, sub)),
      h("span", { class: "svc-end" },
        lx && lx.trace && lx.trace.country ? h("span", { class: "cc" }, lx.trace.country) : h("span"),
        sv.enabled ? h("span", {}, h("i", { class: "dot " + st, style: "margin-inline-end:4px" }), stateWord(sv.id, st) || "") : null))));
  }
  list.append(h("li", {}, h("button", { class: "svc-add", type: "button", onclick: () => openService(null) },
    h("span", { class: "svc-icon", "aria-hidden": "true" }, icon("plus")), h("span", {}, "سرویس تازه، با دامنه‌هایش"))));
  return h("section", { class: "dark-card", "aria-labelledby": "svc-title" },
    h("div", { class: "card-head" }, h("h2", { class: "card-title", id: "svc-title" }, "سرویس‌ها"), ltr(ok + "/" + on.length, "count")),
    list);
}

// Name and domains of a service, editable in its sheet; also the form for
// a new one (sv = null).
function serviceEditor(sv) {
  const f = h("form", { class: "svc-edit" },
    h("label", { class: "field" }, h("span", {}, "اسم"), h("input", { name: "name", required: true, maxlength: 40, value: sv ? sv.name : "", dir: "auto" })),
    h("label", { class: "field" }, h("span", {}, "دامنه‌ها (هر خط یکی؛ زیردامنه‌ها خودشان حساب می‌شوند)"),
      h("textarea", { name: "domains", rows: sv ? 5 : 4, dir: "ltr", spellcheck: "false", required: true, placeholder: "example.com\ncdn.example.net" })),
    !sv ? h("p", { class: "muted", style: "font-size:.8rem;margin:0" }, "تستش این است که صفحه‌ی اولِ دامنه‌ی اول از هر خروجی باز شود و نگوید «در کشور شما نیست».") : null,
    h("p", { class: "form-error", role: "alert" }),
    h("div", { class: "actions" },
      h("button", { class: "pill dark", type: "submit" }, sv ? "ذخیره" : "افزودن سرویس"),
      sv && sv.custom ? h("button", { class: "pill ghost danger", type: "button", onclick: () => {
        if (!confirm("سرویس «" + sv.name + "» حذف شود؟")) return;
        act(null, () => api("DELETE", "services/" + sv.id), "حذف شد").then(() => $("#service-dialog").close());
      } }, "حذف سرویس") : null));
  f.domains.value = sv ? sv.domains.join("\n") : "";
  f.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const err = $(".form-error", f);
    err.textContent = "";
    const body = { name: f.name.value.trim(), domains: f.domains.value.split("\n") };
    const btn = f.querySelector("[type=submit]");
    btn.disabled = true;
    try {
      const r = sv ? await api("PUT", "services/" + sv.id, body) : await api("POST", "services", body);
      toast(sv ? "ذخیره شد" : "اضافه شد؛ تستش شروع شد");
      await refresh(true);
      poll();
      openService(sv ? sv.id : r.id);
    } catch (e) { err.textContent = e.message; }
    finally { btn.disabled = false; }
  });
  return f;
}

/* ---------- service sheet ---------- */

function openService(id) {
  const body = $("#service-body");
  const d = $("#service-dialog");
  if (id === null) {
    fill(body,
      h("div", { class: "sheet-head" }, h("h2", { id: "service-dialog-title" }, "سرویس تازه"),
        h("button", { class: "round small", type: "button", "data-close": "", "aria-label": "بستن" }, icon("close"))),
      serviceEditor(null));
    if (!d.open) d.showModal();
    $("[name=name]", body).focus();
    return;
  }
  const sv = S.services.find((x) => x.id === id);
  if (!sv) return;
  const live = liveSvc(id);
  const choose = (pin) => act(null, () => api("PUT", "services/" + id, { pin }), pin ? sv.name + " روی " + exitOf(pin).name + " ثابت شد" : sv.name + ": انتخاب خودکار")
    .then(() => openService(id));
  const choices = h("div", { class: "choice", role: "group", "aria-label": "خروجی " + sv.name },
    h("button", { type: "button", "aria-pressed": sv.pin ? "false" : "true", onclick: () => choose("") },
      h("span", { class: "thumb" }, icon("spark")),
      h("span", {}, h("b", {}, "خودکار"), h("small", {}, "اولین خروجی باز، با اولویت همان کشور")),
      !sv.pin && live.exit ? h("span", { class: "chip" }, "الان " + (exitOf(live.exit) || {}).name) : h("span")),
    S.exits.filter((x) => x.enabled).map((x) => {
      const r = (liveExit(x.id).results || {})[id];
      return h("button", { type: "button", "aria-pressed": sv.pin === x.id ? "true" : "false", onclick: () => choose(x.id) },
        thumb(x.kind),
        h("span", {}, h("b", {}, x.name), h("small", {}, r ? stateWord(id, r.state) + (r.detail ? "، " + r.detail : "") : "تست نشده")),
        h("i", { class: "dot " + (r ? r.state : "") }));
    }));
  const tog = h("label", { class: "switch" },
    h("input", { type: "checkbox", checked: sv.enabled, "aria-label": sv.name + " روشن باشد",
      onchange: (ev) => act(null, () => api("PUT", "services/" + id, { enabled: ev.target.checked })).then(() => openService(id)) }), h("i"));
  fill(body,
    h("div", { class: "sheet-head" }, h("h2", { id: "service-dialog-title" }, ltr(sv.name)),
      h("button", { class: "round small", type: "button", "data-close": "", "aria-label": "بستن" }, icon("close"))),
    h("div", { class: "toggle-row" }, h("span", {}, h("b", {}, "تست و مسیر این سرویس"),
      h("small", {}, "خاموش که باشد، ترافیکش مثل بقیه‌ی ترافیک می‌رود و تست نمی‌شود.")), tog),
    sv.id === "youtube" ? h("p", { class: "hint" }, "تست YouTube این است که آیا Premium (تنها راه مطمئن بی‌تبلیغ شدن) از کشور آن خروجی فروخته می‌شود. «بدون Premium» یعنی خود یوتیوب کار می‌کند ولی Premium آنجا نیست.") : null,
    sv.id === "instagram" ? h("p", { class: "hint" }, "فقط باز شدن اینستاگرام تست می‌شود. موزیک اینستاگرام به کشوری بستگی دارد که اینستاگرام شما را در آن می‌بیند؛ بدون ورود به حساب نمی‌شود دقیق تستش کرد.") : null,
    sv.enabled ? h("div", { class: "field" }, h("span", {}, "از کدام خروجی برود"), choices) : null,
    h("h3", { class: "card-title", style: "margin-top:.4rem" }, "اسم و دامنه‌ها"),
    serviceEditor(sv));
  if (!d.open) d.showModal();
}

/* ---------- exit menu ---------- */

function closeMenu() {
  const m = $("#menu");
  if (!m.hidden) { m.hidden = true; m.replaceChildren(); }
}
function exitMenu(btn, x) {
  const m = $("#menu");
  const i = S.exits.findIndex((e) => e.id === x.id);
  const item = (label, fn, cls) => h("button", { type: "button", role: "menuitem", class: cls || "", onclick: () => { closeMenu(); fn(); } }, label);
  fill(m,
    x.kind === "warp" && x.enabled ? item("گرفتن IP تازه", () => act(null, () => api("POST", "exits/" + x.id + "/rotate"), x.name + ": IP تازه گرفته شد")) : null,
    item("ویرایش", () => openExit(x)),
    item(x.enabled ? "خاموش کردن" : "روشن کردن", () => act(null, () => api("PUT", "exits/" + x.id, { enabled: !x.enabled }))),
    i > 0 ? item("اولویت بالاتر", () => act(null, () => api("POST", "exits/" + x.id + "/move", { dir: -1 }))) : null,
    i < S.exits.length - 1 ? item("اولویت پایین‌تر", () => act(null, () => api("POST", "exits/" + x.id + "/move", { dir: 1 }))) : null,
    h("hr"),
    item("حذف", () => { if (confirm("خروجی «" + x.name + "» حذف شود؟")) act(null, () => api("DELETE", "exits/" + x.id), "حذف شد"); }, "danger"));
  m.hidden = false;
  const r = btn.getBoundingClientRect();
  const mw = m.offsetWidth, mh = m.offsetHeight;
  let left = r.left;
  if (left + mw > innerWidth - 8) left = innerWidth - mw - 8;
  let top = r.bottom + 6;
  if (top + mh > innerHeight - 8) top = r.top - mh - 6;
  m.style.left = Math.max(8, left) + "px";
  m.style.top = top + "px";
  m.style.setProperty("--origin", (left === r.left ? "left" : "right") + " top");
  m.querySelector("button").focus();
}
document.addEventListener("click", (ev) => { if (!ev.target.closest("#menu, .more")) closeMenu(); });
document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeMenu(); });

/* ============================ tests ============================ */

function viewTests() {
  const services = S.services.filter((x) => x.enabled);
  const t = h("table", { class: "matrix" });
  const hr = h("tr", {}, h("th", { scope: "col" }, h("small", {}, "خروجی")));
  for (const sv of services) {
    const live = liveSvc(sv.id);
    const down = live.state === "down" || !live.exit;
    hr.append(h("th", { scope: "col" }, h("b", {}, ltr(sv.name)),
      h("small", { class: down ? "bad" : "" }, down ? "هیچ خروجی‌ای باز نیست" : (sv.pin ? "ثابت روی " : "از ") + (exitOf(live.exit) || {}).name)));
  }
  const body = h("tbody");
  for (const x of S.exits) {
    const l = liveExit(x.id);
    const row = h("tr", {}, h("th", { scope: "row" }, h("div", { class: "row-exit" }, thumb(x.kind),
      h("div", {}, h("b", {}, x.name), h("small", {}, !x.enabled ? "خاموش" : l.err ? l.err : l.trace && l.trace.ip ? [ltr(l.trace.ip), " ", country(l.trace.country)] : "تست نشده")))));
    for (const sv of services) {
      const r = (l.results || {})[sv.id];
      const used = liveSvc(sv.id).exit === x.id;
      const pinned = sv.pin === x.id;
      let st = "none", word = "—";
      if (x.enabled) {
        if (l.checking) { st = "none checking"; word = "در حال تست"; }
        else if (l.err) { st = "error"; word = STATE_WORD.error; }
        else if (r) { st = r.state; word = stateWord(sv.id, r.state) || r.state; }
        else word = "تست نشده";
      }
      const cls = ["cell", x.enabled && !l.checking ? "" : st, used ? "used" : "", pinned ? "pinned" : "", used && r && r.state !== "ok" ? "notok" : ""].join(" ");
      row.append(h("td", {}, h("button", {
        class: cls, type: "button", disabled: !x.enabled,
        title: r && r.detail ? r.detail : pinned ? "دوباره بزنید تا خودکار شود" : "ثابت کردن " + sv.name + " روی این خروجی",
        "aria-pressed": pinned ? "true" : "false",
        "aria-label": sv.name + " از " + x.name + ": " + word,
        onclick: (ev) => act(ev.currentTarget, () => api("PUT", "services/" + sv.id, { pin: pinned ? "" : x.id }),
          pinned ? sv.name + ": انتخاب خودکار" : sv.name + " روی " + x.name + " ثابت شد"),
      }, pinned ? icon("pin") : h("i", { class: "dot " + (r && !l.err ? r.state : l.err ? "error" : "") }),
        h("span", {}, word, used ? h("small", {}, pinned ? "ثابت" : r && r.state === "ok" ? "در حال استفاده" : "بهتری پیدا نشد") : null))));
    }
    body.append(row);
  }
  t.append(h("thead", {}, hr), body);
  return h("div", {},
    head("جدول تست", "هر سرویس از هر خروجی چه جوابی داد. خانه‌ی تیره، راهی است که الان استفاده می‌شود؛ با زدن روی هر خانه، آن سرویس روی همان خروجی ثابت می‌شود.",
      h("button", { class: "pill", type: "button", onclick: () => (location.hash = "#settings") }, "سرویس‌ها و تنظیمات")),
    h("section", { class: "card" },
      services.length ? h("div", { class: "matrix-scroll" }, t) : h("p", { class: "empty" }, "هیچ سرویسی روشن نیست. از داشبورد، روی یک سرویس بزنید و روشنش کنید."),
      h("div", { class: "legend" },
        h("span", {}, h("i", { class: "dot ok" }), "باز است"),
        h("span", {}, h("i", { class: "dot geo" }), "کشور را قبول نکرد"),
        h("span", {}, h("i", { class: "dot challenge" }), "IP بسته شده (کپچا)"),
        h("span", {}, h("i", { class: "dot error" }), "خود خروجی وصل نشد"))));
}

/* ============================ connect ============================ */

function viewConnect() {
  const p = S.proxy;
  const addr = p.listen + ":" + p.port;
  const notes = [];
  if (S.host.xui) notes.push(h("p", { class: "note" }, h("i", { class: "dot ok" }), "3x-ui روی این سرور پیدا شد."));
  if (S.host.neotunnel) notes.push(h("p", { class: "note" }, h("i", { class: "dot ok" }), "نئوتانل روی این سرور است و وضعیت NeoExit را در صفحه‌ی سرورهایش نشان می‌دهد."));
  if (p.listen !== "127.0.0.1") notes.push(h("p", { class: "note warn" }, "پروکسی از بیرون سرور هم باز است؛ رمزش را محکم نگه دارید."));

  const form = h("form", { class: "form" },
    h("fieldset", { class: "seg" }, h("legend", { class: "sr" }, "چه کسی وصل شود"),
      h("label", {}, h("input", { type: "radio", name: "listen", value: "127.0.0.1", checked: p.listen === "127.0.0.1" }), h("span", {}, "فقط همین سرور")),
      h("label", {}, h("input", { type: "radio", name: "listen", value: "0.0.0.0", checked: p.listen !== "127.0.0.1" }), h("span", {}, "از هر جا، با رمز"))),
    h("label", { class: "field" }, h("span", {}, "پورت"), h("input", { name: "port", type: "number", min: 1, max: 65535, dir: "ltr", value: p.port, required: true })),
    h("div", { class: "row2" },
      h("label", { class: "field" }, h("span", {}, "نام کاربری"), h("input", { name: "user", dir: "ltr", autocomplete: "off", value: p.user || "" })),
      h("label", { class: "field" }, h("span", {}, "رمز"), h("input", { name: "pass", dir: "ltr", autocomplete: "off", value: p.pass || "" }))),
    h("div", {}, h("button", { class: "pill dark", type: "submit" }, "ذخیره‌ی پروکسی")));
  form.addEventListener("submit", (ev) => {
    ev.preventDefault();
    act(form.querySelector("[type=submit]"), () => api("PUT", "proxy", {
      listen: form.listen.value, port: Number(form.port.value), user: form.user.value.trim(), pass: form.pass.value,
    }), "پروکسی ذخیره شد").then(() => render(true));
  });

  return h("div", {},
    head("اتصال", "برنامه‌ها به این پروکسی وصل می‌شوند. SOCKS5 و HTTP هر دو روی همین یک پورت کار می‌کنند."),
    mergeCard(),
    h("div", { class: "grid2", style: "margin-top:14px" },
      h("section", { class: "card" },
        h("h2", { class: "card-title" }, "پروکسی"),
        h("div", { class: "big-addr" }, ltr(addr), h("button", { class: "pill", type: "button", onclick: () => copy(addr) }, icon("copy"), "کپی")),
        h("div", { class: "toggle-row", style: "margin-top:.9rem" },
          h("span", {}, h("b", {}, "ترافیک این پروکسی"), h("small", {}, extraExitName(p))),
          h("button", { class: "pill", type: "button", onclick: () => openExtra("main") }, "تغییر")),
        notes, form),
      h("div", { class: "stack" },
        h("section", { class: "card", id: "routing-card" }, routingCardBody()),
        h("section", { class: "card" },
          h("h2", { class: "card-title" }, "روش دستی"),
          h("p", { class: "muted", style: "margin-top:.3rem" }, "اگر خودتان با تنظیمات Xray کار می‌کنید: فقط ترافیک سرویس‌های هوش مصنوعی از NeoExit می‌رود؛ بقیه مثل قبل."),
          h("ol", { class: "steps" },
            h("li", {}, h("span", {}, "در تنظیمات Xray، یک خروجی تازه بسازید و این را در حالت JSON بچسبانید. اولین خروجی نگذارید؛ اولی پیش‌فرض همه‌ی ترافیک است."),
              h("button", { class: "pill", type: "button", onclick: () => copy(JSON.stringify(S.snippet.outbound, null, 2)) }, icon("copy"), "خروجی")),
            h("li", {}, h("span", {}, "در قوانین مسیریابی، این قانون را بالای قوانین دیگر (زیر api و مسدودسازی) بگذارید."),
              h("button", { class: "pill", type: "button", onclick: () => copy(JSON.stringify(S.snippet.rule, null, 2)) }, icon("copy"), "قانون")),
            h("li", {}, h("span", {}, "Xray را ری‌استارت کنید.")))))),
    extraCard());
}

/* ---------- extra proxies ---------- */

// The host people put in their apps: 127.0.0.1 for a local-only port,
// otherwise the address this panel was opened at.
function extraHost(x) { return x.listen === "127.0.0.1" ? "127.0.0.1" : location.hostname; }
function extraLink(x) {
  const cred = x.user ? encodeURIComponent(x.user) + ":" + encodeURIComponent(x.pass || "") + "@" : "";
  return "socks5://" + cred + extraHost(x) + ":" + x.port;
}
function targetName(t) {
  if (t === "direct") return "مستقیم";
  if (t === "block") return "مسدود";
  const e = exitOf(t);
  return e ? e.name : "؟";
}
function extraExitName(x) {
  return x.exit ? "همه از " + targetName(x.exit) : "طبق جدول روتینگ";
}

function extraCard() {
  const list = (S.extra_proxies || []).map((x) => {
    const e = x.exit ? exitOf(x.exit) : null;
    const down = x.exit && !(liveExit(x.exit) || {}).up;
    return h("div", { class: "toggle-row" },
      h("button", { class: "linklike", type: "button", onclick: () => openExtra(x.id) },
        h("b", {}, h("i", { class: "dot " + (!x.running ? "error" : down ? "down" : "ok") }), " ", x.name, " ", ltr(extraHost(x) + ":" + x.port)),
        h("small", {}, extraExitName(x) + (e && !e.enabled ? " (خاموش)" : down ? " (الان قطع است)" : "") +
          (x.running ? "؛ " + fa(x.conns) + " اتصال" : "؛ پورت باز نشد") + (x.user ? "؛ با رمز" : ""))),
      h("div", { class: "actions-inline" },
        h("button", { class: "pill", type: "button", title: "لینک socks5://", onclick: () => copy(extraLink(x)) }, icon("copy"), "لینک"),
        h("button", { class: "pill", type: "button", title: "خروجی برای Xray / 3x-ui", onclick: () => copy(JSON.stringify(x.outbound, null, 2)) }, icon("copy"), "Xray")));
  });
  return h("section", { class: "card", style: "margin-top:14px" },
    h("div", { class: "card-head" }, h("h2", { class: "card-title" }, "پروکسی‌های بیشتر"),
      h("button", { class: "pill dark", type: "button", onclick: () => openExtra(null) }, icon("plus"), "پروکسی تازه")),
    h("p", { class: "muted", style: "margin-top:.3rem" }, "هر کدام پورت و رمز خودش را دارد و SOCKS5 و HTTP (با UDP) را با هم جواب می‌دهد. یا همه‌ی ترافیکش از یک خروجی می‌رود (مثلاً فقط IPv4)، یا طبق جدول صفحه‌ی «روتینگ» می‌رود؛ قانون‌ها می‌توانند فقط برای یک پروکسی باشند، مثل inboundTag در 3x-ui. برای اینکه همه‌ی ترافیک 3x-ui به NeoExit برسد، خروجی Xray یکی از این‌ها را اول خروجی‌های 3x-ui بگذارید و قانون‌های روتینگ آنجا را بردارید."),
    list.length ? h("div", { class: "stack", style: "margin-top:1rem" }, list)
      : h("p", { class: "note" }, "هنوز پروکسی دیگری نساخته‌اید. مثلاً یکی فقط از WARP و یکی فقط از سایفون آلمان، برای اینباندها یا کاربرهای مختلف 3x-ui."));
}

// Bytes in a short Persian form.
function fmtBytes(n) {
  if (!n) return "۰";
  const u = ["بایت", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return fa(n < 10 && i ? n.toFixed(1) : Math.round(n)) + " " + u[i];
}
// What went through one rule since NeoExit started.
function statOf(key) {
  const c = (S.stats || {})[key];
  return c ? fa(c.conns) + " اتصال · " + fmtBytes(c.bytes) : "—";
}
// Everything that went out as AI.
function aiStat() {
  let c = 0, b = 0;
  for (const [k, v] of Object.entries(S.stats || {})) if (k.startsWith("service:")) { c += v.conns; b += v.bytes; }
  return c ? fa(c) + " اتصال · " + fmtBytes(b) : "—";
}
const proxyName = (id) => id === "main" ? "پروکسی اصلی" : ((S.extra_proxies || []).find((p) => p.id === id) || { name: id }).name;

// The sheet of a proxy: an extra one (id), a new one (null) or the main
// proxy ("main", only where its traffic goes).
function openExtra(id) {
  const main = id === "main";
  const x = main ? S.proxy : id ? (S.extra_proxies || []).find((p) => p.id === id) : null;
  if (id && !x) return;
  const body = $("#proxy-body");
  const d = $("#proxy-dialog");
  const local = !x || x.listen === "127.0.0.1";
  const fixed = !!(x && x.exit);
  let f;
  const syncMode = () => { f.exit.closest("label").hidden = f.mode.value !== "exit"; $(".exit-hint", f).hidden = f.mode.value !== "exit"; };
  f = h("form", { class: "svc-edit" },
    main ? null : h("label", { class: "field" }, h("span", {}, "اسم"), h("input", { name: "name", maxlength: 40, dir: "auto", value: x ? x.name : "", placeholder: "پروکسی تازه" })),
    h("fieldset", { class: "seg" }, h("legend", { class: "sr" }, "ترافیک این پروکسی"),
      h("label", {}, h("input", { type: "radio", name: "mode", value: "table", checked: !fixed, onchange: syncMode }), h("span", {}, "طبق جدول روتینگ")),
      h("label", {}, h("input", { type: "radio", name: "mode", value: "exit", checked: fixed, onchange: syncMode }), h("span", {}, "همه از یک خروجی"))),
    h("label", { class: "field" }, h("span", {}, "خروجی"),
      h("select", { name: "exit" }, S.exits.map((e) => h("option", { value: e.id, selected: x && x.exit === e.id }, e.name + (e.enabled ? "" : " (خاموش)"))))),
    h("p", { class: "hint exit-hint" }, "همه‌ی ترافیک این پورت از همین خروجی می‌رود (آدرس‌های داخلی سرور جدا). اگر قطع شود، اتصال‌ها قطع می‌شوند و از راه دیگری نمی‌روند."),
    h("p", { class: "hint" }, "«طبق جدول روتینگ»: هوش مصنوعی از خروجی هوشمند، بقیه با قانون‌های صفحه‌ی روتینگ؛ قانون‌ها می‌توانند فقط برای این پروکسی باشند (مثل inboundTag در 3x-ui)."),
    main ? null : [
      h("fieldset", { class: "seg" }, h("legend", { class: "sr" }, "چه کسی وصل شود"),
        h("label", {}, h("input", { type: "radio", name: "listen", value: "127.0.0.1", checked: local }), h("span", {}, "فقط همین سرور")),
        h("label", {}, h("input", { type: "radio", name: "listen", value: "0.0.0.0", checked: !local }), h("span", {}, "از هر جا، با رمز"))),
      h("label", { class: "field" }, h("span", {}, "پورت" + (x ? "" : " (خالی = یک پورت آزاد)")), h("input", { name: "port", type: "number", min: 1, max: 65535, dir: "ltr", value: x ? x.port : "" })),
      h("div", { class: "row2" },
        h("label", { class: "field" }, h("span", {}, "نام کاربری"), h("input", { name: "user", dir: "ltr", autocomplete: "off", value: x ? x.user || "" : "" })),
        h("label", { class: "field" }, h("span", {}, "رمز"), h("input", { name: "pass", dir: "ltr", autocomplete: "off", value: x ? x.pass || "" : "" })))],
    h("p", { class: "form-error", role: "alert" }),
    h("div", { class: "actions" },
      h("button", { class: "pill dark", type: "submit" }, x ? "ذخیره" : "ساختن پروکسی"),
      x && !main ? h("button", { class: "pill ghost danger", type: "button", onclick: () => {
        if (!confirm("پروکسی «" + x.name + "» حذف شود؟ برنامه‌هایی که به آن وصل‌اند قطع می‌شوند.")) return;
        act(null, () => api("DELETE", "proxies/" + x.id), "حذف شد").then(() => d.close());
      } }, "حذف") : null));
  f.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const err = $(".form-error", f);
    err.textContent = "";
    const b = { exit: f.mode.value === "exit" ? f.exit.value : "" };
    if (!main) {
      Object.assign(b, { listen: f.listen.value, user: f.user.value.trim(), pass: f.pass.value });
      if (f.name.value.trim()) b.name = f.name.value.trim();
      if (f.port.value) b.port = Number(f.port.value);
      if (b.listen !== "127.0.0.1" && (!b.user || !b.pass)) { err.textContent = "پروکسی باز روی اینترنت نام کاربری و رمز لازم دارد."; return; }
    }
    const btn = f.querySelector("[type=submit]");
    btn.disabled = true;
    try {
      if (x) await api("PUT", "proxies/" + (main ? "main" : x.id), b); else await api("POST", "proxies", b);
      toast(x ? "ذخیره شد" : "پروکسی ساخته شد");
      await refresh(true);
      d.close();
    } catch (e) { err.textContent = e.message; }
    finally { btn.disabled = false; }
  });
  syncMode();
  fill(body,
    h("div", { class: "sheet-head" }, h("h2", { id: "proxy-dialog-title" }, main ? "پروکسی اصلی" : x ? x.name : "پروکسی تازه"),
      h("button", { class: "round small", type: "button", "data-close": "", "aria-label": "بستن" }, icon("close"))),
    f);
  if (!d.open) d.showModal();
}

/* ============================ routing ============================ */

// The table being edited; saved as a whole, like 3x-ui.
let RT = null;
function rtDraft() {
  if (!RT) RT = {
    rules: JSON.parse(JSON.stringify(S.rules || [])),
    other: S.settings.other || "",
    private_direct: !!S.private_direct,
    allow_private: [...(S.settings.allow_private || [])],
    dirty: false,
  };
  return RT;
}
function rtChanged() { rtDraft().dirty = true; render(true); }

const PROTO = { http: "HTTP", tls: "TLS", quic: "QUIC", bittorrent: "BitTorrent" };
function chips(list, max = 3) {
  if (!list || !list.length) return h("span", { class: "muted" }, "—");
  return h("span", { class: "chips" },
    list.slice(0, max).map((x) => h("span", { class: "chip ltr", dir: "ltr", title: x }, x)),
    list.length > max ? h("span", { class: "chip more", title: list.slice(max).join("\n") }, "+" + fa(list.length - max)) : null);
}
function targetLabel(t) {
  if (t === "direct") return h("span", { class: "tgt direct" }, "مستقیم");
  if (t === "block") return h("span", { class: "tgt block" }, "مسدود");
  const e = exitOf(t);
  return h("span", { class: "tgt exit" }, e ? e.name : "؟");
}
// A select of where traffic can go: direct, block or an exit.
function targetSelect(name, value, none) {
  const opt = (v, label) => h("option", { value: v, selected: value === v }, label);
  return h("select", name ? { name } : {},
    none ? opt("", none) : null,
    opt("direct", "مستقیم (direct)"),
    opt("block", "مسدود (block)"),
    S.exits.map((e) => opt(e.id, e.name + (e.enabled ? "" : " (خاموش)"))));
}

function viewRouting() {
  const d = rtDraft();
  const move = (i, dir) => { [d.rules[i], d.rules[i + dir]] = [d.rules[i + dir], d.rules[i]]; rtChanged(); };
  const rows = d.rules.map((r, i) => h("tr", { class: r.disabled ? "off" : "" },
    h("td", { "data-l": "#" }, fa(i + 1)),
    h("td", { "data-l": "فعال" }, h("label", { class: "switch small" },
      h("input", { type: "checkbox", checked: !r.disabled, "aria-label": "قانون " + (i + 1) + " فعال باشد", onchange: (ev) => { r.disabled = !ev.target.checked; rtChanged(); } }), h("i"))),
    h("td", { "data-l": "دامنه" }, r.name ? h("b", { class: "rule-name" }, r.name) : null, chips(r.domains)),
    h("td", { "data-l": "IP" }, chips(r.ips, 2)),
    h("td", { "data-l": "پورت" }, r.ports ? ltr(r.ports) : h("span", { class: "muted" }, "—")),
    h("td", { "data-l": "شبکه" }, r.network ? r.network.toUpperCase() : h("span", { class: "muted" }, "—")),
    h("td", { "data-l": "پروتکل" }, (r.protocols || []).length ? r.protocols.map((p) => PROTO[p] || p).join("، ") : h("span", { class: "muted" }, "—")),
    h("td", { "data-l": "ورودی" }, (r.inbounds || []).length ? r.inbounds.map(proxyName).join("، ") : h("span", { class: "muted" }, "همه")),
    h("td", { "data-l": "خروجی" }, targetLabel(r.target)),
    h("td", { "data-l": "ترافیک", class: "stat" }, statOf("rule:" + r.id)),
    h("td", { class: "ops" },
      h("button", { class: "round small", type: "button", "aria-label": "ویرایش قانون " + (i + 1), title: "ویرایش", onclick: () => openRule(i) }, icon("edit")),
      h("button", { class: "round small", type: "button", "aria-label": "بالاتر", disabled: i === 0, onclick: () => move(i, -1) }, "↑"),
      h("button", { class: "round small", type: "button", "aria-label": "پایین‌تر", disabled: i === d.rules.length - 1, onclick: () => move(i, 1) }, "↓"),
      h("button", { class: "round small", type: "button", "aria-label": "حذف قانون " + (i + 1), title: "حذف", onclick: () => { d.rules.splice(i, 1); rtChanged(); } }, icon("close")))));

  const aiNames = S.services.filter((s) => s.enabled).map((s) => s.name + " ← " + ((exitOf(liveSvc(s.id).exit) || {}).name || "…"));
  const pinned = (n, title, body, target, stat) => h("tr", { class: "pinned" },
    h("td", { "data-l": "#" }, n), h("td", {}, icon("pin")),
    h("td", { colspan: 6, class: "wide" }, h("b", {}, title), h("small", { class: "muted" }, body)),
    h("td", { "data-l": "خروجی" }, target), h("td", { "data-l": "ترافیک", class: "stat" }, stat), h("td", {}));

  const table = h("table", { class: "rt" },
    h("thead", {}, h("tr", {}, ["#", "", "دامنه", "IP", "پورت", "شبکه", "پروتکل", "ورودی", "خروجی", "ترافیک", ""].map((t) => h("th", { scope: "col" }, t)))),
    h("tbody", {},
      pinned("۰", "آدرس‌های داخلی سرور", " · geoip:private؛ 127.0.0.1، 10.x، 192.168.x…", targetLabel(d.private_direct ? "direct" : "block"), statOf("private")),
      pinned("۰", "هوش مصنوعی", " · همیشه از خروجی هوشمند هر سرویس: " + aiNames.join("، "), h("span", { class: "tgt smart" }, "هوشمند"), aiStat()),
      rows,
      h("tr", { class: "pinned" }, h("td", { "data-l": "#" }, "∞"), h("td", {}, icon("pin")),
        h("td", { colspan: 6, class: "wide" }, h("b", {}, "بقیه‌ی ترافیک"), h("small", { class: "muted" }, " · هر چه به هیچ قانونی نخورد")),
        h("td", { "data-l": "خروجی" }, targetSelect("", d.other, "مسیر عادی سرور (direct)")),
        h("td", { "data-l": "ترافیک", class: "stat" }, statOf("default")), h("td", {}))));
  $("select", table.tBodies[0].lastElementChild).addEventListener("change", (ev) => { d.other = ev.target.value; d.dirty = true; drawSaveBar(); });
  if (!d.rules.length) table.tBodies[0].insertBefore(h("tr", {}, h("td", { colspan: 11, class: "empty" }, "قانونی نیست. «قانون تازه» یا «دسته‌های آماده» را بزنید.")), table.tBodies[0].lastElementChild);

  const priv = h("div", { class: "stack" },
    h("fieldset", { class: "seg" }, h("legend", { class: "sr" }, "آدرس‌های داخلی سرور"),
      h("label", {}, h("input", { type: "radio", name: "priv", checked: !d.private_direct, onchange: () => { d.private_direct = false; rtChanged(); } }), h("span", {}, "مسدود (پیشنهادی)")),
      h("label", {}, h("input", { type: "radio", name: "priv", checked: d.private_direct, onchange: () => { d.private_direct = true; rtChanged(); } }), h("span", {}, "مستقیم، مثل 3x-ui"))),
    h("p", { class: "hint", style: "margin:0" }, d.private_direct
      ? "کاربرها می‌توانند به سرویس‌های روی خود سرور (مثلاً پنل‌ها روی 127.0.0.1) و شبکه‌ی داخلی برسند."
      : "کاربرها به 127.0.0.1، شبکه‌ی داخلی و پنل‌های روی سرور نمی‌رسند، حتی با دامنه‌ای که به آن‌ها اشاره کند."),
    d.private_direct ? null : h("label", { class: "field" }, h("span", {}, "استثنا (هر خط یکی)"),
      h("textarea", { rows: 2, dir: "ltr", spellcheck: "false", placeholder: "10.0.0.5\n192.168.1.0/24",
        oninput: (ev) => { d.allow_private = ev.target.value.split("\n").map((s) => s.trim()).filter(Boolean); d.dirty = true; drawSaveBar(); } })));
  const ta = $("textarea", priv);
  if (ta) ta.value = d.allow_private.join("\n");

  return h("div", {},
    head("روتینگ", "مثل بخش Routing Rules در 3x-ui، ولی داخل NeoExit: قانون‌ها از بالا به پایین؛ اولی که بخورد تصمیم می‌گیرد."),
    h("p", { class: "note warn" }, icon("spark"), h("span", {}, "در 3x-ui برای همه‌ی اینباندها ", h("b", {}, "Sniffing را روشن"), " کنید (HTTP، TLS، QUIC) و ", ltr("routeOnly"), " را خاموش بگذارید تا دامنه‌ها به NeoExit برسند. اگر برنامه‌ای فقط IP بفرستد، NeoExit خودش اسم سایت را از اول اتصال پیدا می‌کند، ولی Sniffing دقیق‌تر است.")),
    h("div", { id: "rt-save" }),
    h("section", { class: "card" },
      h("div", { class: "card-head" }, h("h2", { class: "card-title" }, "قانون‌ها"),
        h("div", { class: "actions-inline" },
          h("button", { class: "pill", type: "button", onclick: openPresets }, icon("plus"), "دسته‌های آماده"),
          h("button", { class: "pill dark", type: "button", onclick: () => openRule(-1) }, icon("plus"), "قانون تازه"))),
      h("div", { class: "rt-wrap" }, table)),
    h("div", { class: "grid2", style: "margin-top:14px" },
      h("div", { class: "stack" },
        h("section", { class: "card" }, h("h2", { class: "card-title" }, "امتحان کن"), testerCard()),
        h("section", { class: "card" }, h("h2", { class: "card-title" }, "آدرس‌های داخلی سرور"), priv)),
      h("section", { class: "card" }, h("h2", { class: "card-title" }, "DNS"), dnsCard())));
}

function drawSaveBar() {
  const slot = $("#rt-save");
  if (!slot) return;
  const d = rtDraft();
  fill(slot, d.dirty ? h("div", { class: "savebar" },
    h("span", {}, "تغییرات ذخیره نشده‌اند."),
    h("button", { class: "pill ghost", type: "button", onclick: () => { RT = null; render(true); } }, "برگرداندن"),
    h("button", { class: "pill dark", type: "button", onclick: (ev) => act(ev.currentTarget, async () => {
      await api("PUT", "routing", { rules: d.rules, other: d.other, private_direct: d.private_direct, allow_private: d.allow_private });
      RT = null;
    }, "روتینگ ذخیره شد") }, "ذخیره")) : null);
}

// The rule sheet, with the fields 3x-ui has.
function openRule(i) {
  const d = rtDraft();
  const r = i >= 0 ? d.rules[i] : { target: "direct" };
  const dlg = $("#rule-dialog");
  const list = (v) => (v || []).join(",\n");
  const ta = (name, label, val, ph, hint) => h("label", { class: "field" }, h("span", {}, label),
    h("textarea", { name, rows: 3, dir: "ltr", spellcheck: "false", placeholder: ph }, val), hint ? h("small", { class: "muted" }, hint) : null);
  const proxies = [{ id: "main", name: "پروکسی اصلی" }, ...(S.extra_proxies || [])];
  const f = h("form", { class: "svc-edit" },
    h("div", { class: "row2" },
      h("label", { class: "field" }, h("span", {}, "اسم (اختیاری)"), h("input", { name: "name", dir: "auto", maxlength: 60, value: r.name || "" })),
      h("label", { class: "field" }, h("span", {}, "خروجی (Outbound)"), targetSelect("target", r.target))),
    ta("domains", "دامنه (Domain)", list(r.domains), "myip.ms,\next:hmray.dat:game-ea,\ngeosite:youtube,\ndomain:example.com",
      "با ویرگول یا خط جدا. myip.ms (هر جای اسم)، domain:، full:، regexp:، geosite:، ext:hmray.dat:..."),
    ta("ips", "IP", list(r.ips), "1.2.3.0/24,\ngeoip:ir,\next:hmray-geoip.dat:telegram", "IP، CIDR، geoip:، geoip:private، ext:"),
    h("p", { class: "hint", style: "margin:0" }, "مثل 3x-ui: همه‌ی بخش‌های پرشده‌ی یک قانون باید با هم درست باشند. برای «این دامنه‌ها یا این IPها» دو قانون جدا بسازید."),
    h("div", { class: "row2" },
      h("label", { class: "field" }, h("span", {}, "پورت (Port)"), h("input", { name: "ports", dir: "ltr", placeholder: "443,1000-2000", value: r.ports || "" })),
      h("label", { class: "field" }, h("span", {}, "شبکه (Network)"),
        h("select", { name: "network" },
          h("option", { value: "", selected: !r.network }, "TCP,UDP"),
          h("option", { value: "tcp", selected: r.network === "tcp" }, "TCP"),
          h("option", { value: "udp", selected: r.network === "udp" }, "UDP")))),
    h("fieldset", { class: "field checks" }, h("legend", {}, "پروتکل (Protocol)"),
      Object.entries(PROTO).map(([v, l]) => h("label", { class: "check" }, h("input", { type: "checkbox", name: "proto", value: v, checked: (r.protocols || []).includes(v) }), h("span", {}, l)))),
    h("fieldset", { class: "field checks" }, h("legend", {}, "ورودی (Inbound، مثل inboundTag؛ هیچ = همه)"),
      proxies.map((p) => h("label", { class: "check" }, h("input", { type: "checkbox", name: "inb", value: p.id, checked: (r.inbounds || []).includes(p.id) }), h("span", {}, p.name)))),
    h("p", { class: "form-error", role: "alert" }),
    h("div", { class: "actions" },
      h("button", { class: "pill dark", type: "submit" }, i >= 0 ? "اعمال" : "افزودن"),
      h("button", { class: "pill ghost", type: "button", "data-close": "" }, "انصراف"),
      h("small", { class: "muted" }, "بعد «ذخیره» را در بالای صفحه بزنید.")));
  f.addEventListener("submit", (ev) => {
    ev.preventDefault();
    const split = (s) => s.split(/[,\n،]/).map((x) => x.trim()).filter(Boolean);
    const nr = { ...r, name: f.name.value.trim(), target: f.target.value, domains: split(f.domains.value), ips: split(f.ips.value),
      ports: f.ports.value.trim(), network: f.network.value,
      protocols: $$("[name=proto]:checked", f).map((x) => x.value), inbounds: $$("[name=inb]:checked", f).map((x) => x.value) };
    if (!nr.domains.length && !nr.ips.length && !nr.ports && !nr.network && !nr.protocols.length && !nr.inbounds.length) {
      $(".form-error", f).textContent = "دست‌کم یک شرط لازم است.";
      return;
    }
    if (i >= 0) d.rules[i] = nr; else d.rules.push(nr);
    dlg.close();
    rtChanged();
  });
  fill($("#rule-body"),
    h("div", { class: "sheet-head" }, h("h2", { id: "rule-dialog-title" }, i >= 0 ? "قانون " + fa(i + 1) : "قانون تازه"),
      h("button", { class: "round small", type: "button", "data-close": "", "aria-label": "بستن" }, icon("close"))),
    f);
  if (!dlg.open) dlg.showModal();
}

// Ready rules from the HMray lists.
function openPresets() {
  const d = rtDraft();
  const dlg = $("#rule-dialog");
  dlg.addEventListener("close", () => { if (view === "routing") render(true); }, { once: true });
  fill($("#rule-body"),
    h("div", { class: "sheet-head" }, h("h2", { id: "rule-dialog-title" }, "دسته‌های آماده"),
      h("button", { class: "round small", type: "button", "data-close": "", "aria-label": "بستن" }, icon("close"))),
    h("p", { class: "hint" }, "هر کدام یک قانون می‌سازد که بعد قابل ویرایش است. مسدودها بالای جدول می‌روند، بقیه پایینش."),
    h("div", { class: "stack" }, (S.presets || []).map((p) => {
      const sel = targetSelect("", p.target || "direct");
      return h("div", { class: "toggle-row" },
        h("span", {}, h("b", {}, p.name), h("small", { class: "ltr", dir: "ltr" }, [...(p.domains || []), ...(p.ips || []), p.protocol ? "protocol:" + p.protocol : ""].filter(Boolean).join(", "))),
        h("div", { class: "actions-inline" }, sel, h("button", { class: "pill", type: "button", onclick: () => {
          // In one rule every condition must hold, so names and addresses
          // go in separate rules (as 3x-ui needs too).
          const add = [];
          if ((p.domains || []).length) add.push({ name: p.name, domains: p.domains, target: sel.value });
          if ((p.ips || []).length) add.push({ name: p.name + " (IP)", ips: p.ips, target: sel.value });
          if (p.protocol) add.push({ name: p.name, protocols: [p.protocol], target: sel.value });
          if (sel.value === "block") d.rules.unshift(...add); else d.rules.push(...add);
          toast("«" + p.name + "» اضافه شد؛ ذخیره را بزنید");
          d.dirty = true;
        } }, "افزودن")));
    })),
    h("div", { class: "actions", style: "margin-top:1rem" }, h("button", { class: "pill dark", type: "button", onclick: () => { dlg.close(); render(true); } }, "تمام")));
  if (!dlg.open) dlg.showModal();
}

// Where would this go? With unsaved edits, the edited table is tried.
const rtT = { host: "", port: "", network: "tcp", proxy: "main" };
function testerCard() {
  const d = rtDraft();
  const host = h("input", { dir: "ltr", placeholder: "chatgpt.com  ·  digikala.com  ·  1.2.3.4", value: rtT.host, "aria-label": "دامنه یا IP", oninput: (ev) => (rtT.host = ev.target.value) });
  const port = h("input", { type: "number", min: 1, max: 65535, dir: "ltr", placeholder: "443", value: rtT.port, "aria-label": "پورت", oninput: (ev) => (rtT.port = ev.target.value) });
  const net = h("select", { "aria-label": "شبکه", onchange: (ev) => (rtT.network = ev.target.value) },
    h("option", { value: "tcp", selected: rtT.network === "tcp" }, "TCP"), h("option", { value: "udp", selected: rtT.network === "udp" }, "UDP"));
  const prx = h("select", { "aria-label": "ورودی", onchange: (ev) => (rtT.proxy = ev.target.value) },
    [{ id: "main", name: "پروکسی اصلی" }, ...(S.extra_proxies || [])].map((p) => h("option", { value: p.id, selected: rtT.proxy === p.id }, p.name)));
  const out = h("div", { class: "test-out", "aria-live": "polite" });
  const run = async () => {
    fill(out, "…");
    try {
      const body = { host: host.value, port: Number(port.value) || 0, network: net.value, proxy: prx.value };
      if (d.dirty) body.rules = d.rules;
      const r = await api("POST", "routing/test", body);
      const dd = r.decision, [kind, v] = dd.rule.split(":");
      let why = { private: "آدرس داخلی سرور", exit: "این پروکسی روی یک خروجی است", default: "بقیه‌ی ترافیک" }[kind];
      if (kind === "service") why = v === "ai" ? "سایت هوش مصنوعی" : "سرویس " + ((S.services.find((s) => s.id === v) || { name: v }).name);
      if (kind === "rule") {
        const n = d.rules.findIndex((x) => x.id === v);
        why = "قانون " + (n >= 0 ? fa(n + 1) + (d.rules[n].name ? " (" + d.rules[n].name + ")" : "") : "");
      }
      fill(out, h("b", {}, why), " ← ", targetLabel(dd.target),
        r.up === false ? h("small", { class: "bad" }, " این خروجی الان قطع است؛ اتصال برقرار نمی‌شود.") : null,
        r.dns ? h("small", { class: "muted" }, r.dns === "ir" ? " · با DNS ایران resolve می‌شود" : " · با DNS معمول resolve می‌شود") : null,
        d.dirty ? h("small", { class: "muted" }, " · با تغییرات ذخیره‌نشده") : null);
    } catch (e) { fill(out, h("span", { class: "bad" }, e.message)); }
  };
  host.addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); run(); } });
  return h("div", { class: "stack", style: "margin-top:.8rem" },
    host, h("div", { class: "row3" }, port, net, prx),
    h("div", {}, h("button", { class: "pill dark", type: "button", onclick: run }, "امتحان کن")), out);
}

function dnsCard() {
  const c = S.dns || {};
  const f = h("form", { class: "stack", style: "margin-top:.8rem" },
    h("div", { class: "toggle-row" }, h("span", {}, h("b", {}, "DNS ایران برای دامنه‌های ایرانی"),
      h("small", {}, "دامنه‌های ایرانی (همه‌ی .ir و فهرست ایران HMray) با DNS داخل ایران resolve می‌شوند تا IP ایرانی بگیرند؛ بقیه با DNS معمول. اگر DNS ایران جواب ندهد، DNS معمول جایش را می‌گیرد. فقط برای ترافیکی که از خود سرور بیرون می‌رود.")),
      h("label", { class: "switch" }, h("input", { type: "checkbox", name: "ir_on", checked: !c.ir_off, "aria-label": "DNS ایران" }), h("i"))),
    h("label", { class: "field" }, h("span", {}, "DNSهای ایران (هر خط یکی)"),
      h("textarea", { name: "ir_servers", rows: 3, dir: "ltr", spellcheck: "false", placeholder: (c.def_ir_servers || []).join("\n") })),
    h("label", { class: "field" }, h("span", {}, "دامنه‌های ایرانی (مثل قانون‌ها)"),
      h("textarea", { name: "ir_domains", rows: 2, dir: "ltr", spellcheck: "false", placeholder: (c.def_ir_domains || []).join("\n") })),
    h("label", { class: "field" }, h("span", {}, "DNS معمول برای بقیه (خالی = DNS خود سرور)"),
      h("textarea", { name: "servers", rows: 2, dir: "ltr", spellcheck: "false", placeholder: "1.1.1.1\n8.8.8.8" })),
    h("div", {}, h("button", { class: "pill dark", type: "submit" }, "ذخیره‌ی DNS")));
  f.ir_servers.value = (c.ir_servers || []).join("\n");
  f.ir_domains.value = (c.ir_domains || []).join("\n");
  f.servers.value = (c.servers || []).join("\n");
  const lines = (s) => s.split("\n").map((x) => x.trim()).filter(Boolean);
  f.addEventListener("submit", (ev) => {
    ev.preventDefault();
    act(f.querySelector("[type=submit]"), () => api("PUT", "dns", {
      ir_off: !f.ir_on.checked, ir_servers: lines(f.ir_servers.value), ir_domains: lines(f.ir_domains.value), servers: lines(f.servers.value),
    }), "DNS ذخیره شد");
  });
  const q = h("input", { dir: "ltr", placeholder: "digikala.com", "aria-label": "دامنه برای امتحان DNS" });
  const out = h("div", { class: "test-out", "aria-live": "polite" });
  const run = async () => {
    fill(out, "…");
    try {
      const r = await api("POST", "dns/test", { host: q.value });
      fill(out, h("b", {}, r.via === "ir" ? "DNS ایران" : "DNS معمول"), " · ", fa(r.ms) + " میلی‌ثانیه · ", ltr(r.ips.slice(0, 3).join("  ")));
    } catch (e) { fill(out, h("span", { class: "bad" }, e.message)); }
  };
  q.addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); run(); } });
  return h("div", {}, f, h("div", { class: "stack tester", style: "margin-top:1rem" },
    h("b", {}, "امتحان DNS"), h("div", { class: "row2" }, q, h("button", { class: "pill", type: "button", onclick: run }, "resolve")), out));
}

/* ---------- whole Xray config, merged ---------- */

// Kept across redraws of the page (the pasted config, the choices).
const mx = { text: "", smart: false, cats: Object.fromEntries(XRAY.CATS.map((c) => [c.id, c.def])), out: null };

function mergeCard() {
  const input = h("textarea", { rows: 9, dir: "ltr", spellcheck: "false", "aria-label": "تنظیمات کامل Xray",
    placeholder: '{\n  "log": { ... },\n  "inbounds": [ ... ],\n  "outbounds": [ ... ],\n  "routing": { ... }\n}' });
  input.value = mx.text;
  const err = h("p", { class: "form-error", role: "alert" });
  const smartBox = h("input", { type: "checkbox", checked: mx.smart, "aria-label": "روتینگ هوشمند" });
  const cats = h("div", { class: "cat-grid" });
  const out = h("div", {});

  function targets() {
    let tags = [];
    try { tags = XRAY.tags(XRAY.parse(input.value)); } catch {}
    return tags;
  }
  function drawCats() {
    const tags = targets();
    cats.hidden = !smartBox.checked;
    fill(cats,
      h("div", { class: "cat-row fixed" }, h("span", {}, "هوش مصنوعی (همه‌ی سرویس‌های AI)"), h("b", {}, ltr("neoexit"))),
      XRAY.CATS.map((c) => {
        const sel = h("select", { "aria-label": c.name, onchange: (ev) => { mx.cats[c.id] = ev.target.value; } },
          h("option", { value: "" }, "دست نزن"),
          h("option", { value: "@block" }, "مسدود"),
          h("option", { value: "@direct" }, "مستقیم از همین سرور"),
          h("option", { value: "neoexit" }, "NeoExit"),
          tags.filter((t) => t.protocol !== "blackhole" && t.protocol !== "freedom").map((t) => h("option", { value: t.tag }, t.tag)));
        if (![...sel.options].some((o) => o.value === mx.cats[c.id])) mx.cats[c.id] = c.def.startsWith("@") ? c.def : "";
        sel.value = mx.cats[c.id];
        return h("label", { class: "cat-row" }, h("span", {}, c.name), sel);
      }));
  }
  input.addEventListener("input", () => { mx.text = input.value; err.textContent = ""; drawCats(); });
  smartBox.addEventListener("change", () => { mx.smart = smartBox.checked; drawCats(); });

  function build() {
    err.textContent = "";
    try {
      mx.out = XRAY.merge(input.value, { outbound: S.snippet.outbound, rule: S.snippet.rule, smart: smartBox.checked, cats: mx.cats });
    } catch (e) { mx.out = null; err.textContent = e.message; fill(out); return; }
    drawOut();
  }
  function drawOut() {
    const r = mx.out;
    if (!r) return fill(out);
    const rs = S.routing.status;
    const warn = r.smartUsed && !rs.current
      ? h("p", { class: "note warn" }, "قوانین هوشمند فایل‌های hmray.dat و hmray-geoip.dat را لازم دارند. " +
          (S.host.xui ? "اول از کادر «روتینگ هوشمند» پایین همین صفحه نصبشان کنید" : "اول آن‌ها را در پوشه‌ی Xray بگذارید") + "؛ وگرنه Xray بالا نمی‌آید.")
      : null;
    const res = h("textarea", { rows: 12, dir: "ltr", readonly: true, spellcheck: "false", "aria-label": "تنظیمات آماده" });
    res.value = r.text;
    fill(out,
      h("ul", { class: "merge-notes" }, r.notes.map((n) => h("li", {}, n))),
      warn,
      h("div", { class: "out-head" }, h("b", {}, "تنظیمات آماده"),
        h("button", { class: "pill dark", type: "button", onclick: () => copy(r.text) }, icon("copy"), "کپی همه")),
      res,
      h("ol", { class: "steps" },
        h("li", {}, h("span", {}, "در 3x-ui همان جایی که کپی کردید (Xray Configs ← Advanced) همه را پاک کنید و این را بچسبانید.")),
        h("li", {}, h("span", {}, "ذخیره کنید و Xray را ری‌استارت کنید."))));
  }

  drawCats();
  drawOut();
  return h("section", { class: "card" },
    h("h2", { class: "card-title" }, "راه ساده: کل تنظیمات Xray را بچسبانید"),
    h("p", { class: "muted", style: "margin-top:.3rem" },
      "در 3x-ui بروید به Xray Configs ← Advanced، کل متن را کپی کنید و اینجا بچسبانید. خروجی NeoExit و قوانین، هر کدام سر جای درستش اضافه می‌شود و بقیه‌ی تنظیمات شما دست نمی‌خورد. متن از مرورگر شما بیرون نمی‌رود."),
    h("div", { class: "form", style: "margin-top:1rem" },
      input, err,
      h("div", { class: "toggle-row" }, h("span", {}, h("b", {}, "روتینگ هوشمند HMray"),
        h("small", {}, "ترافیک را بر اساس نوع سرویس به خروجی دلخواه می‌فرستد: ایران مستقیم، بازی به ترکیه، تبلیغات مسدود و… برای هر دسته یکی از خروجی‌های خودتان را انتخاب کنید.")),
        h("label", { class: "switch" }, smartBox, h("i"))),
      cats,
      h("div", {}, h("button", { class: "pill dark", type: "button", onclick: build }, "ساختن تنظیمات"))),
    out);
}

/* ---------- Smart Routing lists ---------- */

function routingCardBody() {
  const R = S.routing, st = R.status;
  const size = (n) => fa((n / 1048576).toFixed(n > 1048576 ? 1 : 2)) + " MB";
  let line;
  if (!S.host.xui) line = h("p", { class: "note" }, "3x-ui روی این سرور پیدا نشد. دو فایل را دانلود کنید و کنار برنامه‌ی Xray بگذارید (در 3x-ui معمولاً ", ltr("/usr/local/x-ui/bin"), ").");
  else if (st.current) line = h("p", { class: "note" }, h("i", { class: "dot ok" }), "نصب است و به‌روز؛ ", ltr(st.dir), R.auto ? ". با هر به‌روزرسانی NeoExit خودش تازه می‌شود." : ".");
  else if (st.installed) line = h("p", { class: "note warn" }, "نسخه‌ی قدیمی‌تری در ", ltr(st.dir), " هست.");
  else line = h("p", { class: "note" }, "هنوز در ", ltr(st.dir || "3x-ui"), " نصب نشده.");
  const install = S.host.xui && !st.current
    ? h("button", { class: "pill dark", type: "button", onclick: (ev) => act(ev.currentTarget, async () => {
        const r = await api("POST", "routing/install");
        S.routing = r.routing;
        const c = $("#routing-card");
        if (c) fill(c, routingCardBody());
      }, "نصب شد؛ Xray را در 3x-ui ری‌استارت کنید") }, st.installed ? "به‌روز کردن" : "نصب در 3x-ui")
    : null;
  return [
    h("h2", { class: "card-title" }, "روتینگ هوشمند"),
    h("p", { class: "muted", style: "margin-top:.3rem" }, "فهرست دامنه‌ها و IPهای HMray که قوانین ", ltr("ext:hmray.dat:…"), " از آن خوانده می‌شود."),
    line,
    h("div", { class: "file-row" },
      R.files.map((f) => h("a", { class: "pill", href: "api/routing/files/" + f.name, download: f.name }, ltr(f.name), h("small", { class: "muted" }, size(f.size)))),
      install),
  ];
}

/* ============================ history ============================ */

function viewHistory() {
  const evs = S.live.events || [];
  const ol = h("ol", { class: "timeline" });
  let lastDay = "";
  for (const e of evs) {
    const d = day(e.at);
    ol.append(h("li", { class: e.kind === "switch" ? "switch-ev" : e.kind },
      h("time", { datetime: e.at }, clock(e.at)),
      h("span", { class: "tl-mark" }, h("i")),
      h("div", { class: "tl-body" }, e.text, d !== lastDay ? h("small", {}, d) : null)));
    lastDay = d;
  }
  return h("div", {},
    head("تاریخچه", "جابه‌جایی سرویس‌ها بین خروجی‌ها، IPهای تازه و خطاها، با دلیل هر کدام."),
    h("section", { class: "card" }, evs.length ? ol : h("p", { class: "empty" }, "هنوز اتفاقی نیفتاده. هر جابه‌جایی و IP تازه اینجا ثبت می‌شود.")));
}

/* ============================ settings ============================ */

function viewSettings() {
  const st = S.settings;
  const sw = (name, checked) => h("label", { class: "switch" }, h("input", { type: "checkbox", name, checked, "aria-label": name }), h("i"));
  const f = h("form", { class: "form" },
    h("label", { class: "field" }, h("span", {}, "هر چند دقیقه یک بار تست شود"), h("input", { name: "check_minutes", type: "number", min: 2, max: 720, dir: "ltr", value: st.check_minutes, required: true })),
    h("div", { class: "toggle-row" }, h("span", {}, h("b", {}, "کشور ثابت"),
      h("small", {}, "وقتی سرویسی باید جابه‌جا شود، اول خروجی‌های همان کشور قبلی امتحان می‌شوند. عوض شدن پشت سر هم کشور حساب کاربری را مشکوک می‌کند.")), sw("same_country", st.same_country)),
    h("div", { class: "toggle-row" }, h("span", {}, h("b", {}, "IP تازه‌ی خودکار برای WARP"),
      h("small", {}, "اگر هیچ خروجی‌ای برای یک سرویس جواب ندهد، WARP حساب تازه و IP تازه می‌گیرد.")), sw("auto_rotate", st.auto_rotate)),
    h("label", { class: "field" }, h("span", {}, "حداکثر IP تازه در ساعت"), h("input", { name: "rotate_hourly", type: "number", min: 1, max: 30, dir: "ltr", value: st.rotate_hourly })),
    h("label", { class: "field" }, h("span", {}, "بقیه‌ی ترافیک (همان ردیف آخر جدول روتینگ)"),
      h("select", { name: "other" }, h("option", { value: "" }, "مسیر عادی خود سرور"), S.exits.map((x) => h("option", { value: x.id, selected: st.other === x.id }, x.name)))),
    h("div", {}, h("button", { class: "pill dark", type: "submit" }, "ذخیره‌ی تنظیمات")));
  f.addEventListener("submit", (ev) => {
    ev.preventDefault();
    act(f.querySelector("[type=submit]"), () => api("PUT", "settings", {
      check_minutes: Number(f.check_minutes.value), same_country: f.same_country.checked, auto_rotate: f.auto_rotate.checked,
      rotate_hourly: Number(f.rotate_hourly.value), other: f.other.value,
    }), "تنظیمات ذخیره شد");
  });

  const svcList = h("div", { class: "stack", style: "margin-top:1rem" }, S.services.map((sv) => h("div", { class: "toggle-row" },
    h("button", { class: "linklike", type: "button", onclick: () => openService(sv.id) }, h("b", {}, ltr(sv.name)), h("small", {}, fa(sv.domains.length) + " دامنه" + (sv.custom ? "، اضافه‌شده" : "") + "؛ ویرایش")),
    h("label", { class: "switch" }, h("input", { type: "checkbox", checked: sv.enabled, "aria-label": sv.name + " روشن باشد",
      onchange: (ev) => act(null, () => api("PUT", "services/" + sv.id, { enabled: ev.target.checked }), ev.target.checked ? sv.name + " روشن شد" : sv.name + " خاموش شد") }), h("i")))),
    h("div", {}, h("button", { class: "pill", type: "button", onclick: () => openService(null) }, icon("plus"), "سرویس تازه")));

  const pf = h("form", { class: "form" },
    h("label", { class: "field" }, h("span", {}, "نام کاربری"), h("input", { name: "user", dir: "ltr", autocomplete: "username", value: S.user, required: true })),
    h("label", { class: "field" }, h("span", {}, "رمز فعلی"), h("input", { name: "old", type: "password", dir: "ltr", autocomplete: "current-password", required: true })),
    h("label", { class: "field" }, h("span", {}, "رمز تازه"), h("input", { name: "new", type: "password", dir: "ltr", autocomplete: "new-password", minlength: 8, required: true })),
    h("div", {}, h("button", { class: "pill dark", type: "submit" }, "عوض کردن رمز")));
  pf.addEventListener("submit", (ev) => {
    ev.preventDefault();
    act(pf.querySelector("[type=submit]"), async () => {
      await api("PUT", "password", { old: pf.old.value, user: pf.user.value, new: pf.new.value });
      pf.old.value = pf.new.value = "";
    }, "رمز عوض شد");
  });

  return h("div", {},
    head("تنظیمات", null),
    h("div", { class: "grid2" },
      h("section", { class: "card" }, h("h2", { class: "card-title" }, "تست و جابه‌جایی"), f),
      h("div", { class: "stack" },
        h("section", { class: "card" }, h("h2", { class: "card-title" }, "سرویس‌ها"), svcList),
        h("section", { class: "card", id: "update-card" }, updateCardBody()),
        h("section", { class: "card" }, h("h2", { class: "card-title" }, "ورود به پنل"), pf))));
}

/* ---------- updates ---------- */

const UPDATE_STATE = {
  downloading: "در حال دانلود و بررسی نسخه‌ی تازه…",
  restarting: "نصب شد؛ NeoExit دوباره راه می‌افتد…",
  manual: "نسخه‌ی تازه نصب شد. برنامه را دوباره اجرا کنید تا بالا بیاید.",
};
let seenVersion = null;

function updateCardBody() {
  const u = S.update;
  const busy = u.state === "downloading" || u.state === "restarting";
  let line;
  if (u.state && UPDATE_STATE[u.state]) line = h("p", { class: "note" + (busy ? " busy" : "") }, UPDATE_STATE[u.state]);
  else if (u.state === "failed") line = h("p", { class: "note warn" }, "به‌روزرسانی نشد: ", ltr(u.problem || "")) ;
  else if (u.newer) line = h("p", { class: "note" }, h("i", { class: "dot ok" }), "نسخه‌ی تازه آماده است: ", ltr(u.latest));
  else if (u.err) line = h("p", { class: "note warn" }, "بررسی نشد: ", ltr(u.err));
  else if (u.latest) line = h("p", { class: "note" }, "به‌روز است. آخرین بررسی " + ago(u.checked) + ".");
  else line = h("p", { class: "note" }, "هنوز بررسی نشده.");
  const check = h("button", { class: "pill", type: "button", disabled: busy, onclick: (ev) => act(ev.currentTarget, async () => {
    S.update = await api("POST", "update/check");
    toast(S.update.newer ? "نسخه‌ی تازه پیدا شد: " + S.update.latest : "به‌روز است");
  }) }, "بررسی");
  const apply = u.newer && u.can
    ? h("button", { class: "pill dark", type: "button", disabled: busy, onclick: (ev) => act(ev.currentTarget, async () => {
        S.update = await api("POST", "update/apply");
        poll();
      }, "دانلود شروع شد") }, "دانلود و نصب " + u.latest)
    : null;
  const manual = u.newer && !u.can
    ? h("p", { class: "muted", style: "font-size:.82rem;margin-top:.6rem" }, "این نصب خودش به‌روز نمی‌شود؛ دستور نصب را دوباره روی سرور اجرا کنید.")
    : null;
  return [
    h("h2", { class: "card-title" }, "نسخه"),
    h("div", { class: "big-addr ver" }, ltr("NeoExit " + u.current), u.url && u.newer ? h("a", { class: "pill", href: u.url, target: "_blank", rel: "noopener noreferrer" }, "تغییرات") : null),
    line,
    u.newer && u.notes ? h("pre", { class: "notes", dir: "auto" }, u.notes) : null,
    manual,
    h("div", { class: "file-row" }, check, apply),
  ];
}

// The line on other pages when a new version is out.
function updateBanner() {
  const u = S.update;
  if (!u.newer || view === "settings") return null;
  return h("a", { class: "note update-banner", href: "#settings" }, h("i", { class: "dot ok" }),
    "نسخه‌ی تازه‌ی NeoExit آماده است: ", ltr(u.latest), h("span", { class: "go" }, "به‌روزرسانی ←"));
}

// After a restart into a new version, say so once.
function noticeVersion() {
  if (seenVersion && seenVersion !== S.version) toast("NeoExit به " + S.version + " به‌روز شد");
  seenVersion = S.version;
}

/* ============================ exit sheet ============================ */

let editing = null;
const dlg = $("#exit-dialog");
const xf = $("#exit-form");
for (const el of $$("[data-art]", xf)) el.append(art(ART[el.dataset.art]));

function syncKind() {
  const kind = (xf.querySelector("[name=kind]:checked") || {}).value || "";
  for (const el of $$("[data-for]", xf)) el.hidden = !el.dataset.for.split(" ").includes(kind);
  if (!editing && (!xf.name.value || autoNames().includes(xf.name.value))) xf.name.value = autoName(kind);
}
// A new exit's suggested name, from its kind and region.
function autoName(kind) {
  if (kind === "opera") return "Opera " + OPERA_WORD[xf.opera_region.value];
  if (kind === "psiphon") return xf.psiphon_region.value ? "سایفون " + country(xf.psiphon_region.value) : "سایفون";
  return { warp: "WARP", socks5: "SOCKS5", http: "HTTP", direct4: "IPv4 سرور", direct6: "IPv6 سرور", ssh: "SSH" }[kind] || "";
}
function autoNames() {
  return ["WARP", "SOCKS5", "HTTP", "IPv4 سرور", "IPv6 سرور", "SSH", "سایفون",
    ...Object.values(OPERA_WORD).map((r) => "Opera " + r), ...(S ? S.choices.psiphon : []).filter(Boolean).map((c) => "سایفون " + country(c))];
}
function fillCountries() {
  const sel = xf.psiphon_region;
  if (sel.options.length || !S) return;
  for (const cc of S.choices.psiphon) sel.append(h("option", { value: cc }, cc ? country(cc) + "  " + cc : "بهترین کشور (خودش انتخاب می‌کند)"));
}
function openExit(x) {
  editing = x || null;
  xf.reset();
  $("#exit-error").textContent = "";
  $("#exit-dialog-title").textContent = x ? "ویرایش " + x.name : "افزودن خروجی";
  $("#exit-save").textContent = x ? "ذخیره" : "افزودن";
  $("#kind-field").classList.toggle("locked", !!x);
  fillCountries();
  xf.querySelector(`[name=kind][value="${x ? x.kind : "warp"}"]`).checked = true;
  xf.pass.placeholder = xf.license.placeholder = "";
  xf.key.placeholder = "-----BEGIN OPENSSH PRIVATE KEY-----";
  if (x) {
    xf.name.value = x.name;
    xf.address.value = x.address || "";
    xf.user.value = x.user || "";
    if (x.kind === "opera") xf.opera_region.value = x.region || "EU";
    if (x.kind === "psiphon") xf.psiphon_region.value = x.region || "";
    if (x.has_pass) xf.pass.placeholder = "بدون تغییر";
    if (x.license) xf.license.placeholder = "بدون تغییر";
    if (x.has_key) xf.key.placeholder = "کلید فعلی می‌ماند؛ برای عوض کردن، کلید تازه را بچسبانید";
  }
  syncKind();
  dlg.showModal();
}
xf.addEventListener("change", (ev) => { if (["kind", "opera_region", "psiphon_region"].includes(ev.target.name)) syncKind(); });
xf.addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const kind = xf.querySelector("[name=kind]:checked").value;
  const body = { kind, name: xf.name.value.trim() };
  if (kind === "socks5" || kind === "http" || kind === "ssh") {
    body.address = xf.address.value.trim();
    body.user = xf.user.value.trim();
    if (xf.pass.value || !editing) body.pass = xf.pass.value;
  }
  if (kind === "ssh" && xf.key.value.trim()) body.key = xf.key.value;
  if (kind === "opera") body.region = xf.opera_region.value;
  if (kind === "psiphon") body.region = xf.psiphon_region.value;
  if (kind === "warp" && (xf.license.value || !editing)) body.license = xf.license.value.trim();
  const btn = $("#exit-save");
  btn.disabled = true;
  try {
    if (editing) await api("PUT", "exits/" + editing.id, body);
    else await api("POST", "exits", body);
    dlg.close();
    toast(editing ? "ذخیره شد" : "اضافه شد؛ تستش شروع شد");
    await refresh(true);
    poll();
  } catch (e) {
    $("#exit-error").textContent = e.message;
  } finally {
    btn.disabled = false;
  }
});

// Close buttons and backdrop clicks for both sheets.
for (const d of $$("dialog")) {
  d.addEventListener("click", (ev) => {
    if (ev.target.closest("[data-close]") || ev.target === d) d.close();
  });
}

/* ============================ start ============================ */

async function start() {
  try { await refresh(true); }
  catch { showLogin(); return; }
  $("#login").hidden = true;
  $("#app").hidden = false;
  route();
  poll();
}
start();
