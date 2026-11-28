import { isOffline, noteResponse } from './offline';
import { clearDataCache } from './pwa';

// The API expects this header on every state-changing request that carries the session cookie
// (internal/server/auth.go CSRF): any non-empty value, plus a JSON content type.
export const CSRF_HEADER = 'X-Dojo-Csrf';

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);

/** fetch with the session cookie, plus the CSRF header and a JSON content type on mutating requests. Reads also feed the offline flag. */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? 'GET').toUpperCase();
  // No offline write queue on the web: a write while the page shows cached data fails like a dropped connection.
  if (isOffline() && !SAFE_METHODS.has(method) && path !== '/v1/auth/logout') throw new TypeError('offline');
  const headers = new Headers(init.headers);
  if (!SAFE_METHODS.has(method)) {
    headers.set(CSRF_HEADER, '1');
    if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(path, { ...init, method, headers, credentials: 'same-origin' });
  if (SAFE_METHODS.has(method)) noteResponse(path, res);
  // The worker only sees a few routes: tell it about a 401 from any other so no learner data stays cached.
  if (res.status === 401) void clearDataCache();
  return res;
}
