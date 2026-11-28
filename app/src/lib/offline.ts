import { useSyncExternalStore } from 'react';

/** Header the service worker adds when it answers a read from its cached copy. */
export const OFFLINE_HEADER = 'X-Dojo-Offline';

let offline = false;
const listeners = new Set<() => void>();

export function isOffline(): boolean {
  return offline;
}

export function setOffline(next: boolean): void {
  if (offline === next) return;
  offline = next;
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}

/** True while the last read came from the worker's cache, so the page shows the W4 banner and holds writes. */
export function useOffline(): boolean {
  return useSyncExternalStore(subscribe, isOffline, () => false);
}

const TICKET = /^\/v1\/tickets\/[^/]+\/[^/]+$/;

/** The routes the worker answers from cache (same matcher as public/sw.js); only these say anything about the connection. */
export function isCachedRoute(path: string): boolean {
  const pathname = path.split(/[?#]/)[0] ?? path;
  return pathname === '/v1/me' || pathname === '/v1/today' || TICKET.test(pathname);
}

/** Records what a response says about the connection. Only reads on the cached routes can come from the cache. */
export function noteResponse(path: string, res: Response): void {
  if (!isCachedRoute(path)) return;
  setOffline(res.headers.get(OFFLINE_HEADER) === '1');
}

if (typeof window !== 'undefined') {
  // Back online: drop the flag; the refetch on reconnect sets it again if the network is still down.
  window.addEventListener('online', () => setOffline(false));
}

export const OFFLINE_WRITE_NOTE = 'Written hints are saved with the ticket. Asking Carmen waits for your connection.';
