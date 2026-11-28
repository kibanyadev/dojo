// Dojo app-shell worker. Bump VERSION when this file's own logic changes.
const VERSION = 'v2';
const PREFIX = 'dojo-shell-';
const CACHE = PREFIX + VERSION;
const DATA_PREFIX = 'dojo-data-';
const DATA = DATA_PREFIX + VERSION;
const KEEP_TICKETS = 3;
// A ticket id holds one slash (`role/slug`), so the ticket is exactly two segments; `/bundle` and every deeper path are not.
const TICKET = /^\/v1\/tickets\/[^/]+\/[^/]+$/;
const SHELL = '/app/index.html';
const OFFLINE = '/app/offline.html';
const ASSETS = '/app/assets/';

async function install() {
  const cache = await caches.open(CACHE);
  const res = await fetch(SHELL, { cache: 'reload' });
  if (!res.ok) throw new Error('shell unavailable');
  const html = await res.clone().text();
  const urls = new Set(html.match(/\/app\/assets\/[^"'\s)]+/g) ?? []);
  await cache.addAll([...urls, OFFLINE]);
  await cache.put(SHELL, res);
}

async function activate() {
  const keys = await caches.keys();
  const stale = (k) => (k.startsWith(PREFIX) && k !== CACHE) || (k.startsWith(DATA_PREFIX) && k !== DATA);
  await Promise.all(keys.filter(stale).map((k) => caches.delete(k)));
}

function dataKind(pathname) {
  if (pathname === '/v1/me' || pathname === '/v1/today') return 'single';
  if (TICKET.test(pathname)) return 'ticket';
  return null;
}

async function trimTickets(cache) {
  const keys = (await cache.keys()).filter((r) => new URL(r.url).pathname.startsWith('/v1/tickets/'));
  // keys() lists in insertion order, oldest first; put() of a known key keeps its slot, so delete it first on refresh.
  for (const r of keys.slice(0, Math.max(0, keys.length - KEEP_TICKETS))) await cache.delete(r);
}

// Bumped on every clear. A read that started before a clear must not write its answer after it.
let generation = 0;

async function clearData() {
  generation++;
  await caches.delete(DATA);
}

async function data(request, kind) {
  const url = new URL(request.url);
  const key = new Request(url.origin + url.pathname);
  const started = generation;
  try {
    const res = await fetch(request);
    if (res.status === 401) {
      await clearData();
    } else if (res.ok && started === generation) {
      const cache = await caches.open(DATA);
      if (kind === 'ticket') await cache.delete(key);
      if (started === generation) await cache.put(key, res.clone());
      if (kind === 'ticket' && started === generation) await trimTickets(cache);
      // A clear landed while the writes were in flight: drop whatever they recreated.
      if (started !== generation) await caches.delete(DATA);
    }
    return res;
  } catch {
    const cache = await caches.open(DATA);
    const hit = await cache.match(key);
    if (!hit) return Response.error();
    const headers = new Headers(hit.headers);
    headers.set('X-Dojo-Offline', '1');
    return new Response(hit.body, { status: hit.status, statusText: hit.statusText, headers });
  }
}

async function navigate(request) {
  const cache = await caches.open(CACHE);
  try {
    const res = await fetch(request);
    if (res.ok && (res.headers.get('content-type') ?? '').includes('text/html')) {
      await cache.put(SHELL, res.clone());
    }
    return res;
  } catch {
    return (await cache.match(SHELL)) ?? (await cache.match(OFFLINE)) ?? Response.error();
  }
}

async function asset(request) {
  const cache = await caches.open(CACHE);
  const hit = await cache.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok) await cache.put(request, res.clone());
  return res;
}

self.addEventListener('install', (event) => event.waitUntil(install()));
self.addEventListener('activate', (event) => event.waitUntil(activate()));

self.addEventListener('message', (event) => {
  if (!event.data || event.data.type !== 'clear') return;
  const done = clearData().then(() => event.ports?.[0]?.postMessage({ type: 'cleared' }));
  event.waitUntil(done);
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname === '/v1' || url.pathname.startsWith('/v1/')) {
    const kind = dataKind(url.pathname);
    if (kind) event.respondWith(data(request, kind));
    return;
  }
  if (request.mode === 'navigate' && url.pathname.startsWith('/app/') && url.pathname !== OFFLINE) {
    event.respondWith(navigate(request));
  } else if (url.pathname.startsWith(ASSETS)) {
    event.respondWith(asset(request));
  }
});
