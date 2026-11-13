// Allmend service worker (ADR offline). Served as /sw.js; the server
// replaces the CONFIG line below (version, offline forms, CSP, offline pages).
//
// Online it changes nothing. Offline (network error, 502/503/504, timeout):
// pages come from the cache, the forms in CONFIG.writes go to an outbox in
// IndexedDB and are sent again later, in order, with Allmend-Vorgang and
// Allmend-Konto. The server validates them like any other request.
"use strict";

const CONFIG = {};

const PAGES = `allmend-${CONFIG.version}`;
const TIMEOUT_MS = 8000;
const KONTO = "Allmend-Konto";
const writes = CONFIG.writes.map((r) => new RegExp(r));
const entryPath = /^\/offline\/eintrag\/(\d+)$/;

class Unreachable extends Error {}

// ── IndexedDB: outbox (entries) and meta (current account) ──────────────

let dbPromise;
function db() {
  dbPromise ??= new Promise((resolve, reject) => {
    const req = indexedDB.open("allmend-offline", 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore("outbox", { keyPath: "id", autoIncrement: true });
      req.result.createObjectStore("meta");
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

async function tx(store, mode, fn) {
  const d = await db();
  return new Promise((resolve, reject) => {
    const t = d.transaction(store, mode);
    const req = fn(t.objectStore(store));
    t.oncomplete = () => resolve(req?.result);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error);
  });
}

const outbox = {
  all: () => tx("outbox", "readonly", (s) => s.getAll()), // ordered by id
  get: (id) => tx("outbox", "readonly", (s) => s.get(id)),
  add: (e) => tx("outbox", "readwrite", (s) => s.add(e)),
  put: (e) => tx("outbox", "readwrite", (s) => s.put(e)),
  del: (id) => tx("outbox", "readwrite", (s) => s.delete(id)),
};
const meta = {
  get: (k) => tx("meta", "readonly", (s) => s.get(k)),
  set: (k, v) => tx("meta", "readwrite", (s) => s.put(v, k)),
};

// ── lifecycle ───────────────────────────────────────────────────────────

self.addEventListener("install", (event) => {
  event.waitUntil(precache().then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name !== PAGES) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

// precache stores the offline pages (they carry the account's navigation)
// and the app's own scripts, styles and icons.
async function precache() {
  const cache = await caches.open(PAGES);
  try {
    await cache.addAll(CONFIG.assets);
  } catch {
    // offline during install: the assets come with the next visit
  }
  for (const path of CONFIG.pages) {
    try {
      const res = await fetch(path, { credentials: "same-origin", cache: "no-store" });
      const konto = res.headers.get(KONTO);
      if (res.status === 200 && konto) {
        await cache.put(path, res);
        await meta.set("konto", konto);
      }
    } catch {
      // offline during install: the pages come with the next visit
    }
  }
}

// ── fetch ───────────────────────────────────────────────────────────────

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  const path = url.pathname;
  if (req.method === "GET") {
    if (path.startsWith("/static/")) {
      event.respondWith(asset(req));
    } else if (req.mode === "navigate" && entryPath.test(path)) {
      event.respondWith(entryAnswer(Number(entryPath.exec(path)[1])));
    } else if (req.mode === "navigate" && path.startsWith("/schiffe")) {
      event.respondWith(online(req).catch(offlineOnly(() => statusPage("schiffe", 503))));
    } else if (req.mode === "navigate" && !never(path)) {
      event.respondWith(page(event, req));
    }
    return;
  }
  if (req.method === "POST" && req.mode === "navigate") {
    event.respondWith(post(req));
  }
});

function never(path) {
  return CONFIG.never.some((p) => path === p || path.startsWith(`${p}/`));
}

// online fetches req; an unreachable server becomes Unreachable.
async function online(req) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Unreachable("timeout")), TIMEOUT_MS);
  });
  try {
    const res = await Promise.race([fetch(req), timeout]);
    if (res.status === 502 || res.status === 503 || res.status === 504) throw new Unreachable(`status ${res.status}`);
    return res;
  } catch (e) {
    if (e instanceof TypeError) throw new Unreachable(e.message); // network error
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

// offlineOnly turns Unreachable into fallback(); every other error stays.
function offlineOnly(fallback) {
  return (e) => {
    if (e instanceof Unreachable) return fallback();
    throw e;
  };
}

async function asset(req) {
  try {
    const res = await online(req);
    if (res.status === 200 && res.type === "basic") {
      const copy = res.clone();
      (await caches.open(PAGES)).put(req, copy);
    }
    return res;
  } catch (e) {
    const cached = await caches.match(req, { cacheName: PAGES });
    if (cached && e instanceof Unreachable) return cached;
    throw e;
  }
}

async function page(event, req) {
  try {
    const res = await online(req);
    event.waitUntil(remember(req.url, res.clone()));
    return res;
  } catch (e) {
    if (!(e instanceof Unreachable)) throw e;
    const cached = await caches.match(req.url, { cacheName: PAGES });
    return cached ? withBanner(cached) : statusPage("nicht-verfuegbar", 503);
  }
}

// remember caches pages of the logged-in account; a new account drops all.
async function remember(key, res) {
  const konto = res.headers.get(KONTO);
  if (res.status !== 200 || res.type !== "basic" || !konto) return;
  if (/no-store/.test(res.headers.get("Cache-Control") ?? "")) return;
  if ((await meta.get("konto")) !== konto) {
    await caches.delete(PAGES);
    await meta.set("konto", konto);
    await precache();
  }
  await (await caches.open(PAGES)).put(key, res);
}

const dateFmt = new Intl.DateTimeFormat("de-CH", {
  timeZone: "Europe/Zurich",
  day: "numeric",
  month: "numeric",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

async function withBanner(cached) {
  const date = new Date(cached.headers.get("Date") ?? Date.now());
  const banner = `<p class="flash offline" role="status">Offline – Stand vom ${dateFmt.format(date)}</p>`;
  const html = (await cached.text()).replace("<main>", `<main>\n${banner}`);
  return new Response(html, { status: 200, headers: cached.headers });
}

async function statusPage(art, status) {
  const cached = await caches.match(`/offline/${art}`, { cacheName: PAGES });
  const headers = { "Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": CONFIG.csp };
  if (cached) return new Response(await cached.text(), { status, headers });
  const text = art === "schiffe" ? "Schiffe versenken braucht eine Verbindung." : "Allmend ist gerade nicht erreichbar.";
  return new Response(`<!doctype html><html lang="de"><meta charset="utf-8"><title>Offline · Allmend</title><p>${text}</p></html>`, { status, headers });
}

// ── outbox ──────────────────────────────────────────────────────────────

async function post(req) {
  const path = new URL(req.url).pathname;
  const writable = writes.some((r) => r.test(path));
  const body = writable ? await req.clone().text() : "";
  const replaces = entryFromReferrer(req.referrer);
  let res;
  try {
    res = await online(req);
  } catch (e) {
    if (!(e instanceof Unreachable)) throw e;
    if (path.startsWith("/schiffe")) return statusPage("schiffe", 503);
    const konto = await meta.get("konto");
    const type = req.headers.get("Content-Type") ?? "";
    if (!writable || !konto || type !== "application/x-www-form-urlencoded") return statusPage("nicht-moeglich", 503);
    await outbox.add({ key: crypto.randomUUID(), konto, url: req.url, type, body, label: label(path, body), created: Date.now(), state: "wartet" });
    if (replaces) await outbox.del(replaces); // a corrected rejected entry replaces it
    await notify();
    return statusPage("gespeichert", 200);
  }
  // A corrected rejected entry was sent and accepted (POST/redirect/GET).
  if (replaces && res.type === "opaqueredirect") {
    await outbox.del(replaces);
    await notify();
  }
  return res;
}

function entryFromReferrer(referrer) {
  if (!referrer) return 0;
  const u = new URL(referrer);
  const m = u.origin === self.location.origin && entryPath.exec(u.pathname);
  return m ? Number(m[1]) : 0;
}

function label(path, body) {
  const f = new URLSearchParams(body);
  const parts = path.split("/");
  if (path.startsWith("/ausgaben/")) return ["Ausgabe", f.get("description"), f.get("date")].filter(Boolean).join(" · ");
  if (path.startsWith("/arbeitswege/")) return `Arbeitswege ${parts[2]}`;
  if (path.startsWith("/velo/")) return ["Velo-Tour", f.get("title"), f.get("date")].filter(Boolean).join(" · ");
  return path;
}

let running = null;
function replay() {
  running ??= send().finally(() => {
    running = null;
  });
  return running;
}

// send transfers waiting entries one by one. Result: "ok", "offline" or "anmelden".
async function send() {
  const konto = await meta.get("konto");
  let result = "ok";
  for (const e of await outbox.all()) {
    if (e.state !== "wartet" || e.konto !== konto) continue;
    let res;
    try {
      res = await fetch(e.url, {
        method: "POST",
        body: e.body,
        headers: { "Content-Type": e.type, "Allmend-Vorgang": e.key, [KONTO]: e.konto },
        redirect: "manual",
        credentials: "same-origin",
        cache: "no-store",
      });
    } catch {
      result = "offline";
      break;
    }
    if (res.type === "opaqueredirect") {
      await outbox.del(e.id);
      continue;
    }
    if (res.status === 502 || res.status === 503 || res.status === 504) {
      result = "offline";
      break;
    }
    if (res.status === 401) {
      result = "anmelden";
      break;
    }
    e.state = "abgelehnt";
    e.status = res.status;
    e.html = await res.text();
    await outbox.put(e);
  }
  await notify(result);
  return result;
}

async function summary(result) {
  const konto = await meta.get("konto");
  const all = await outbox.all();
  return {
    type: "outbox",
    result,
    waiting: all.filter((e) => e.state === "wartet").length,
    rejected: all.filter((e) => e.state === "abgelehnt").length,
    entries: all.map((e) => ({ id: e.id, label: e.label, created: e.created, state: e.state, status: e.status ?? 0, foreign: e.konto !== konto })),
  };
}

async function notify(result) {
  const s = await summary(result);
  for (const c of await self.clients.matchAll({ type: "window" })) c.postMessage(s);
}

async function entryAnswer(id) {
  const e = await outbox.get(id);
  const headers = { "Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": CONFIG.csp };
  if (!e?.html) return statusPage("nicht-verfuegbar", 404);
  return new Response(e.html, { status: 200, headers });
}

// Messages from offline.js: {type: "replay" | "status" | "discard", id}.
self.addEventListener("message", (event) => {
  const port = event.ports[0];
  const msg = event.data ?? {};
  const work = (async () => {
    if (msg.type === "replay") return summary(await replay());
    if (msg.type === "discard") {
      await outbox.del(Number(msg.id));
      await notify();
    }
    return summary();
  })();
  event.waitUntil(work.then((s) => port?.postMessage(s), (e) => port?.postMessage({ type: "error", message: String(e) })));
});
