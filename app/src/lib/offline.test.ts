// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from './api';
import { isOffline, setOffline } from './offline';

afterEach(() => {
  setOffline(false);
  vi.unstubAllGlobals();
});

describe('offline flag', () => {
  it('turns on when a read carries X-Dojo-Offline: 1 and off on a live answer', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { headers: { 'X-Dojo-Offline': '1' } })));
    await apiFetch('/v1/today');
    expect(isOffline()).toBe(true);
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}')));
    await apiFetch('/v1/today');
    expect(isOffline()).toBe(false);
  });

  it('lets only the cached routes decide the flag', async () => {
    setOffline(true);
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}')));
    for (const path of ['/v1/path', '/v1/catalog', '/v1/tickets/a/b/bundle', '/v1/tickets/a/b/progress', '/v1/stream']) {
      await apiFetch(path);
      expect(isOffline()).toBe(true);
    }
    await apiFetch('/v1/tickets/a/b?x=1');
    expect(isOffline()).toBe(false);
    setOffline(true);
    await apiFetch('/v1/me');
    expect(isOffline()).toBe(false);
    setOffline(true);
    await apiFetch('/v1/today');
    expect(isOffline()).toBe(false);
  });

  it('does not let a write answer change it', async () => {
    setOffline(false);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204, headers: { 'X-Dojo-Offline': '1' } })));
    await apiFetch('/v1/me', { method: 'PATCH', body: '{}' });
    expect(isOffline()).toBe(false);
  });

  it('refuses writes while offline, without touching the network, except sign-out', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    setOffline(true);
    await expect(apiFetch('/v1/tickets/a/b/hints/1', { method: 'POST', body: '{}' })).rejects.toThrow();
    await expect(apiFetch('/v1/me', { method: 'DELETE' })).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
    await apiFetch('/v1/auth/logout', { method: 'POST', body: '{}' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('clears when the browser comes back online', () => {
    setOffline(true);
    window.dispatchEvent(new Event('online'));
    expect(isOffline()).toBe(false);
  });
});
