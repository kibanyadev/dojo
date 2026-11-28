// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ME_KEY } from './lib/me';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from './routes';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };

let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;
let calls: { url: string; method: string; headers: Headers; body: unknown }[];

function stubApi(handler: (url: string, method: string, state: { loggedOut: boolean }) => number | object) {
  calls = [];
  const state = { loggedOut: false };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const method = (init.method ?? 'GET').toUpperCase();
      calls.push({ url, method, headers: new Headers(init.headers), body: init.body });
      if (method === 'POST') state.loggedOut = true;
      const out = handler(url, method, state);
      return typeof out === 'number' ? new Response(null, { status: out }) : Response.json(out);
    }),
  );
}

let client: QueryClient;

async function mount(path: string) {
  router = createMemoryRouter(routes, { initialEntries: [path] });
  client = new QueryClient();
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
}

// Lets the fetch, the query and the redirect settle; each pass flushes inside act.
async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

beforeEach(() => {
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('bootstrap routing', () => {
  it('sends a signed-out visitor at /app to the sign-in screen', async () => {
    stubApi(() => 401);
    await mount('/');
    expect(router.state.location.pathname).toBe('/signin');
    expect(container.querySelector('h1')?.textContent).toContain('Your first day starts here.');
    expect(calls[0]).toMatchObject({ url: '/v1/me', method: 'GET' });
  });

  it('sends a signed-in visitor at /app to Today, with the username in the frame', async () => {
    stubApi(() => me);
    await mount('/');
    expect(router.state.location.pathname).toBe('/today');
    expect(container.querySelector('header a[href="/settings"]')?.getAttribute('aria-label')).toBe('Settings, @irene');
    expect(container.textContent).toContain('Sign out');
  });

  it('sends a signed-in visitor at /signin to Today', async () => {
    stubApi(() => me);
    await mount('/signin');
    expect(router.state.location.pathname).toBe('/today');
  });

  it('keeps the callback query on the sign-in screen and shows its notice', async () => {
    stubApi(() => 401);
    await mount('/signin?confirm=1');
    expect(container.querySelector('[role=status]')?.textContent).toContain('Confirm both boxes');
    await act(async () => router.navigate('/signin?error=access_denied'));
    await settle();
    expect(container.querySelector('[role=status]')?.textContent).toContain('cancelled');
  });

  it.each(['/', '/today', '/path', '/settings', '/settings/machines', '/t/foundations/terminal-01'])('sends a suspended account at %s to O1 with the suspended notice', async (path) => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ error: 'account_suspended' }, { status: 403 })),
    );
    await mount(path);
    expect(router.state.location.pathname).toBe('/signin');
    expect(router.state.location.search).toBe('?error=suspended');
    expect(container.querySelector('[role=status]')?.textContent).toContain('This account is suspended.');
    expect(container.textContent).not.toContain('Could not reach Dojo');
  });

  it('sends a learner suspended mid-session to O1 once, without a redirect loop', async () => {
    stubApi(() => me);
    await mount('/settings');
    expect(router.state.location.pathname).toBe('/settings');
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => Response.json({ error: 'account_suspended' }, { status: 403 })),
    );
    const navigations: string[] = [];
    const stop = router.subscribe((state) => navigations.push(state.location.pathname));
    await act(async () => client.refetchQueries({ queryKey: ME_KEY }));
    await settle();
    stop();
    expect(router.state.location.pathname).toBe('/signin');
    expect(navigations.filter((p) => p === '/signin').length).toBeLessThanOrEqual(1);
    expect(navigations).not.toContain('/today');
    expect(container.querySelector('[role=status]')?.textContent).toContain('This account is suspended.');
  });

  it('keeps the retry for a 403 that is not a suspension', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ error: 'forbidden' }, { status: 403 })));
    await mount('/');
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not reach Dojo');
  });

  it('shows a retry, not the sign-in screen, when the API fails', async () => {
    stubApi(() => 500);
    await mount('/');
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not reach Dojo');
    expect(router.state.location.pathname).toBe('/');
  });
});

describe('sign out', () => {
  async function clickSignOut() {
    const btn = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Sign out')!;
    await act(async () => btn.click());
    await settle();
  }

  it('posts {} with the CSRF header and goes to O1 on 204', async () => {
    stubApi((url, _m, out) => (url === '/v1/me' ? (out.loggedOut ? 401 : me) : 204));
    await mount('/');
    await clickSignOut();
    const post = calls.find((c) => c.method === 'POST')!;
    expect(post.url).toBe('/v1/auth/logout');
    expect(post.body).toBe('{}');
    expect(post.headers.get('X-Dojo-Csrf')).toBeTruthy();
    expect(post.headers.get('Content-Type')).toBe('application/json');
    expect(router.state.location.pathname).toBe('/signin');
  });

  it('tells the service worker to clear its data cache before the logout request', async () => {
    const order: string[] = [];
    const postMessage = vi.fn((msg: { type: string }, ports: MessagePort[]) => {
      order.push(msg.type);
      ports[0]?.postMessage('cleared');
    });
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: { controller: { postMessage } } });
    try {
      stubApi((url, m, out) => {
        if (url === '/v1/auth/logout') order.push(m);
        return url === '/v1/me' ? (out.loggedOut ? 401 : me) : 204;
      });
      await mount('/');
      await clickSignOut();
      expect(order.slice(0, 2)).toEqual(['clear', 'POST']);
    } finally {
      Reflect.deleteProperty(navigator, 'serviceWorker');
    }
  });

  it.each([401, 404])('goes to O1 on %i', async (status) => {
    const today = { current: null, next: null, role: null, all_passed: false };
    stubApi((url, _m, out) => (url === '/v1/me' ? (out.loggedOut ? 401 : me) : url === '/v1/today' ? today : status));
    await mount('/');
    await clickSignOut();
    expect(router.state.location.pathname).toBe('/signin');
  });

  it('stays and says so on a server error', async () => {
    stubApi((url) => (url === '/v1/me' ? me : 500));
    await mount('/');
    await clickSignOut();
    expect(router.state.location.pathname).toBe('/today');
    expect(container.textContent).toContain('Could not sign out');
  });
});
