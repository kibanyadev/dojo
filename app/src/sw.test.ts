import { readFileSync } from 'node:fs';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const source = readFileSync(new URL('../public/sw.js', import.meta.url), 'utf8');
const ORIGIN = 'https://dojo.test';
const HTML = '<script src="/app/assets/index-a1.js"></script><link href="/app/assets/index-a1.css">';

type Listener = (event: Record<string, unknown>) => void;

function keyOf(req: string | Request) {
  return typeof req === 'string' ? new URL(req, ORIGIN).href : req.url;
}

function fakeCaches() {
  const stores = new Map<string, Map<string, Response>>();
  const open = (name: string) => {
    const store = stores.get(name) ?? new Map<string, Response>();
    stores.set(name, store);
    return {
      match: async (req: string | Request) => store.get(keyOf(req))?.clone(),
      put: async (req: string | Request, res: Response) => void store.set(keyOf(req), res),
      keys: async () => [...store.keys()].map((u) => new Request(u)),
      delete: async (req: string | Request) => store.delete(keyOf(req)),
      addAll: async (urls: string[]) => {
        for (const u of urls) store.set(keyOf(u), await fetchStub(u));
      },
    };
  };
  return {
    stores,
    api: {
      open: async (name: string) => open(name),
      keys: async () => [...stores.keys()],
      delete: async (name: string) => stores.delete(name),
    },
  };
}

let fetchStub: (req: string | Request, init?: RequestInit) => Promise<Response>;
let listeners: Record<string, Listener>;
let store: ReturnType<typeof fakeCaches>;

function load() {
  listeners = {};
  store = fakeCaches();
  const self = {
    location: new URL(ORIGIN),
    addEventListener: (type: string, fn: Listener) => void (listeners[type] = fn),
  };
  new Function('self', 'caches', 'fetch', source)(self, store.api, (r: string | Request, i?: RequestInit) => fetchStub(r, i));
}

async function lifecycle(type: 'install' | 'activate') {
  let pending: Promise<unknown> = Promise.resolve();
  listeners[type]?.({ waitUntil: (p: Promise<unknown>) => (pending = p) });
  await pending;
}

function dispatch(url: string, init: { method?: string; mode?: string } = {}) {
  const request = { url: new URL(url, ORIGIN).href, method: init.method ?? 'GET', mode: init.mode ?? 'cors' };
  let answer: Promise<Response> | undefined;
  listeners.fetch?.({ request, respondWith: (p: Promise<Response>) => (answer = p) });
  return answer;
}

const html = (body = HTML) => new Response(body, { headers: { 'content-type': 'text/html' } });

beforeEach(() => {
  fetchStub = vi.fn(async (req) => {
    const path = new URL(keyOf(req)).pathname;
    return path.endsWith('.html') ? html() : new Response('asset');
  });
  load();
});

describe('install', () => {
  it('caches the shell, its assets and the offline page under a versioned name', async () => {
    await lifecycle('install');
    expect([...store.stores.keys()]).toEqual(['dojo-shell-v2']);
    const urls = [...(store.stores.get('dojo-shell-v2')?.keys() ?? [])].map((u) => new URL(u).pathname).sort();
    expect(urls).toEqual(['/app/assets/index-a1.css', '/app/assets/index-a1.js', '/app/index.html', '/app/offline.html']);
  });

  it('fails the install when the shell cannot be fetched', async () => {
    fetchStub = async () => new Response('no', { status: 502 });
    await expect(lifecycle('install')).rejects.toThrow();
  });
});

describe('activate', () => {
  it('deletes older shell caches and leaves other caches alone', async () => {
    store.stores.set('dojo-shell-v0', new Map());
    store.stores.set('dojo-shell-v2', new Map());
    store.stores.set('other', new Map());
    await lifecycle('activate');
    expect([...store.stores.keys()].sort()).toEqual(['dojo-shell-v2', 'other']);
  });
});

describe('fetch', () => {
  it('never handles other API routes, whatever the mode', async () => {
    for (const path of ['/v1/stream', '/v1/path', '/v1/catalog', '/v1/tickets/support-l1/perms-03/bundle', '/v1/tickets/support-l1/perms-03/progress', '/v1']) {
      expect(dispatch(path)).toBeUndefined();
      expect(dispatch(path, { mode: 'navigate' })).toBeUndefined();
    }
    expect(fetchStub).not.toHaveBeenCalled();
  });

  it('ignores writes and other origins', () => {
    expect(dispatch('/app/assets/index-a1.js', { method: 'POST' })).toBeUndefined();
    expect(dispatch('https://elsewhere.test/app/assets/x.js')).toBeUndefined();
    expect(dispatch('/app/offline.html', { mode: 'navigate' })).toBeUndefined();
  });

  it('serves navigations network first and refreshes the cached shell', async () => {
    await lifecycle('install');
    fetchStub = async () => html('<p>new</p>');
    const res = await dispatch('/app/today', { mode: 'navigate' });
    expect(await res?.text()).toBe('<p>new</p>');
    const cached = store.stores.get('dojo-shell-v2')?.get(`${ORIGIN}/app/index.html`);
    expect(await cached?.text()).toBe('<p>new</p>');
  });

  it('falls back to the cached shell, then to the offline page', async () => {
    fetchStub = async () => {
      throw new TypeError('offline');
    };
    store.stores.set('dojo-shell-v2', new Map([[`${ORIGIN}/app/offline.html`, new Response('offline page')]]));
    expect(await (await dispatch('/app/today', { mode: 'navigate' }))?.text()).toBe('offline page');
    store.stores.get('dojo-shell-v2')?.set(`${ORIGIN}/app/index.html`, new Response('shell'));
    expect(await (await dispatch('/app/today', { mode: 'navigate' }))?.text()).toBe('shell');
  });

  it('does not replace the shell with an error page', async () => {
    await lifecycle('install');
    fetchStub = async () => new Response('boom', { status: 500, headers: { 'content-type': 'text/html' } });
    await dispatch('/app/today', { mode: 'navigate' });
    const cached = store.stores.get('dojo-shell-v2')?.get(`${ORIGIN}/app/index.html`);
    expect(await cached?.text()).toBe(HTML);
  });

  it('serves hashed assets cache first and stores misses', async () => {
    await lifecycle('install');
    fetchStub = async () => new Response('network');
    expect(await (await dispatch('/app/assets/index-a1.js'))?.text()).toBe('asset');
    expect(await (await dispatch('/app/assets/lazy-b2.js'))?.text()).toBe('network');
    expect(store.stores.get('dojo-shell-v2')?.has(`${ORIGIN}/app/assets/lazy-b2.js`)).toBe(true);
  });

  it('does not store or replay failed asset responses', async () => {
    await lifecycle('install');
    for (const status of [404, 500]) {
      fetchStub = async () => new Response('bad', { status });
      const res = await dispatch('/app/assets/gone-c3.js');
      expect(res?.status).toBe(status);
      expect(store.stores.get('dojo-shell-v2')?.has(`${ORIGIN}/app/assets/gone-c3.js`)).toBe(false);
    }
    fetchStub = async () => new Response('fresh');
    expect(await (await dispatch('/app/assets/gone-c3.js'))?.text()).toBe('fresh');
  });

  it('does not replace the shell with a non-HTML navigation response', async () => {
    await lifecycle('install');
    fetchStub = async () => new Response('{}', { headers: { 'content-type': 'application/json' } });
    await dispatch('/app/today', { mode: 'navigate' });
    const cached = store.stores.get('dojo-shell-v2')?.get(`${ORIGIN}/app/index.html`);
    expect(await cached?.text()).toBe(HTML);
  });

  it('leaves no cached shell when an install asset fails', async () => {
    fetchStub = async (req) => {
      const path = new URL(keyOf(req)).pathname;
      if (path === '/app/assets/index-a1.js') throw new TypeError('offline');
      return path.endsWith('.html') ? html() : new Response('asset');
    };
    await expect(lifecycle('install')).rejects.toThrow();
    expect(store.stores.get('dojo-shell-v2')?.has(`${ORIGIN}/app/index.html`) ?? false).toBe(false);
  });
});

const DATA = 'dojo-data-v2';
const T = (n: number) => `/v1/tickets/support-l1/t-${n}`;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const dataKeys = () => [...(store.stores.get(DATA)?.keys() ?? [])].map((k) => new URL(k).pathname);
const offline = () => {
  fetchStub = async () => {
    throw new TypeError('offline');
  };
};

describe('data cache', () => {
  it('keeps /v1/me and /v1/today network first and stores the last good answer', async () => {
    fetchStub = async () => json({ n: 1 });
    expect(await (await dispatch('/v1/me'))?.json()).toEqual({ n: 1 });
    await dispatch('/v1/today');
    expect(dataKeys().sort()).toEqual(['/v1/me', '/v1/today']);
    fetchStub = async () => json({ n: 2 });
    await dispatch('/v1/me');
    offline();
    const res = await dispatch('/v1/me');
    expect(await res?.json()).toEqual({ n: 2 });
    expect(res?.headers.get('X-Dojo-Offline')).toBe('1');
  });

  it('does not mark online answers and does not cache failed ones', async () => {
    fetchStub = async () => json({ n: 1 });
    expect((await dispatch('/v1/me'))?.headers.get('X-Dojo-Offline')).toBeNull();
    fetchStub = async () => json({ error: 'x' }, 500);
    expect((await dispatch('/v1/me'))?.status).toBe(500);
    offline();
    expect(await (await dispatch('/v1/me'))?.json()).toEqual({ n: 1 });
  });

  it('fails like the network when nothing is cached', async () => {
    offline();
    const res = await dispatch('/v1/today');
    expect(res?.type).toBe('error');
  });

  it('keeps the last 3 tickets and evicts the oldest', async () => {
    fetchStub = async (req) => json({ url: keyOf(req) });
    for (const n of [1, 2, 3, 4]) await dispatch(T(n));
    expect(dataKeys()).toEqual([T(2), T(3), T(4)]);
    offline();
    expect((await dispatch(T(1)))?.type).toBe('error');
    expect((await dispatch(T(2)))?.headers.get('X-Dojo-Offline')).toBe('1');
  });

  it('refreshing a ticket makes it the newest', async () => {
    fetchStub = async (req) => json({ url: keyOf(req) });
    for (const n of [1, 2, 3, 1, 4]) await dispatch(T(n));
    expect(dataKeys()).toEqual([T(3), T(1), T(4)]);
  });

  it('never caches the bundle, progress or any other subpath', async () => {
    fetchStub = async () => new Response('bytes');
    for (const path of [`${T(1)}/bundle`, `${T(1)}/progress`, '/v1/path', '/v1/stream', '/v1/catalog']) {
      expect(dispatch(path)).toBeUndefined();
    }
    expect(dataKeys()).toEqual([]);
  });

  it('sends every write straight to the network', async () => {
    for (const path of ['/v1/me', '/v1/today', T(1)]) {
      for (const method of ['POST', 'PATCH', 'PUT', 'DELETE']) expect(dispatch(path, { method })).toBeUndefined();
    }
    expect(dataKeys()).toEqual([]);
  });

  it('deletes the data cache on a 401', async () => {
    fetchStub = async () => json({ n: 1 });
    await dispatch('/v1/me');
    await dispatch(T(1));
    expect(dataKeys().length).toBe(2);
    fetchStub = async () => new Response(null, { status: 401 });
    expect((await dispatch('/v1/today'))?.status).toBe(401);
    expect(store.stores.has(DATA)).toBe(false);
  });

  it('clears the data cache on a clear message and acks on the port', async () => {
    fetchStub = async () => json({ n: 1 });
    await dispatch('/v1/me');
    store.stores.set('dojo-shell-v2', new Map([[`${ORIGIN}/app/index.html`, new Response('shell')]]));
    const postMessage = vi.fn();
    let pending: Promise<unknown> = Promise.resolve();
    listeners.message?.({ data: { type: 'clear' }, ports: [{ postMessage }], waitUntil: (p: Promise<unknown>) => (pending = p) });
    await pending;
    expect(store.stores.has(DATA)).toBe(false);
    expect(store.stores.has('dojo-shell-v2')).toBe(true);
    expect(postMessage).toHaveBeenCalled();
  });

  it('does not cache a read that was in flight when a clear message arrived', async () => {
    let release: (r: Response) => void = () => {};
    fetchStub = () => new Promise<Response>((r) => (release = r));
    const answer = dispatch('/v1/me');
    let pending: Promise<unknown> = Promise.resolve();
    listeners.message?.({ data: { type: 'clear' }, waitUntil: (p: Promise<unknown>) => (pending = p) });
    await pending;
    release(json({ n: 1 }));
    expect((await answer)?.status).toBe(200);
    expect(store.stores.has(DATA)).toBe(false);
  });

  it('does not cache a read that was in flight when a 401 cleared another route', async () => {
    let release: (r: Response) => void = () => {};
    fetchStub = () => new Promise<Response>((r) => (release = r));
    const slow = dispatch(T(1));
    const slowRelease = release;
    fetchStub = async () => new Response(null, { status: 401 });
    await dispatch('/v1/today');
    slowRelease(json({ n: 1 }));
    expect((await slow)?.status).toBe(200);
    expect(store.stores.has(DATA)).toBe(false);
  });

  it('caches again for reads that start after a clear', async () => {
    let pending: Promise<unknown> = Promise.resolve();
    listeners.message?.({ data: { type: 'clear' }, waitUntil: (p: Promise<unknown>) => (pending = p) });
    await pending;
    fetchStub = async () => json({ n: 1 });
    await dispatch('/v1/me');
    expect(dataKeys()).toEqual(['/v1/me']);
  });

  it('ignores other messages', async () => {
    fetchStub = async () => json({ n: 1 });
    await dispatch('/v1/me');
    listeners.message?.({ data: { type: 'other' }, waitUntil: () => {} });
    listeners.message?.({ data: null, waitUntil: () => {} });
    expect(store.stores.has(DATA)).toBe(true);
  });

  it('activation keeps the current data cache and deletes other versions', async () => {
    store.stores.set(DATA, new Map([[`${ORIGIN}/v1/me`, new Response('x')]]));
    store.stores.set('dojo-data-v1', new Map());
    store.stores.set('other', new Map());
    await lifecycle('activate');
    expect([...store.stores.keys()].sort()).toEqual([DATA, 'other']);
    expect(dataKeys()).toEqual(['/v1/me']);
  });
});
