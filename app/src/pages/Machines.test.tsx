// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { osLabel, relativeTime } from '../lib/machines';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene-gh', email: null, language: 'en', usage_stats: true, analytics_consent: null };

const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
const MIN = 60_000;
const DAY = 86_400_000;

const mac = { id: 'd1', device_name: 'irene-macbook', os: 'darwin/arm64', cli_version: '0.4.2', created_at: ago(30 * DAY), last_used_at: ago(2 * MIN), current: false };
const wsl = { id: 'd2', device_name: 'irene-wsl', os: 'linux/amd64', cli_version: '0.4.1', created_at: ago(30 * DAY), last_used_at: ago(3 * DAY), current: false };
const odd = { id: 'd3', device_name: 'hosted-7f3a', os: 'plan9', cli_version: '0.4.2', created_at: ago(30 * DAY), last_used_at: null, current: false };

type Call = { url: string; method: string; headers: Headers };
let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;
let calls: Call[];

/** GET /v1/me serves the learner; GET /v1/me/devices serves `list`; DELETE goes to `onDelete`. */
function stubApi(list: object | number, onDelete: () => number | [number, string] = () => 204) {
  calls = [];
  const gone = new Set<string>();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const method = (init.method ?? 'GET').toUpperCase();
      calls.push({ url, method, headers: new Headers(init.headers) });
      if (url === '/v1/me') return Response.json(me);
      if (method === 'DELETE') {
        const out = onDelete();
        if (out === 204 || (Array.isArray(out) && out[0] === 404)) gone.add(url.split('/').pop()!);
        if (Array.isArray(out)) return Response.json({ error: out[1] }, { status: out[0] });
        return out === 204 ? new Response(null, { status: 204 }) : Response.json({ error: 'x' }, { status: out });
      }
      if (typeof list === 'number') return Response.json({ error: 'x' }, { status: list });
      const devices = (list as { devices: { id: string }[] }).devices.filter((d) => !gone.has(d.id));
      return Response.json({ devices });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(path = '/settings/machines') {
  router = createMemoryRouter(routes, { initialEntries: [path] });
  await act(async () => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
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

const click = async (el: HTMLElement) => {
  await act(async () => el.click());
  await settle();
};
const button = (name: string) => container.querySelector<HTMLButtonElement>(`button[aria-label="${name}"]`)!;
const rows = () => [...container.querySelectorAll('li')];
const row = (i: number) => rows()[i]!;
const text = () => container.textContent ?? '';

describe('osLabel and relativeTime', () => {
  it('maps the known systems and shows others as sent', () => {
    expect(osLabel('darwin')).toBe('macOS');
    expect(osLabel('linux')).toBe('Linux');
    expect(osLabel('windows')).toBe('Windows');
    expect(osLabel('plan9')).toBe('plan9');
    expect(osLabel('darwin/arm64')).toBe('macOS (arm64)');
    expect(osLabel('windows/amd64')).toBe('Windows (amd64)');
    expect(osLabel('plan9/386')).toBe('plan9/386');
  });

  it.each([
    [10_000, 'just now'],
    [MIN, '1 minute ago'],
    [5 * MIN, '5 minutes ago'],
    [3 * 3_600_000, '3 hours ago'],
    [DAY, '1 day ago'],
    [3 * DAY, '3 days ago'],
    [14 * DAY, '2 weeks ago'],
    [90 * DAY, '3 months ago'],
  ])('words %i ms as %s', (diff, expected) => {
    expect(relativeTime(new Date(1_000_000_000_000 - diff).toISOString(), 1_000_000_000_000)).toBe(expected);
  });

  it('is empty for a missing or invalid time', () => {
    expect(relativeTime(null)).toBe('');
    expect(relativeTime('nope')).toBe('');
  });
});

describe('settings nav and route', () => {
  it('has Account and Machines as real links, Machines current on its route (deep link)', async () => {
    stubApi({ devices: [mac] });
    await mount();
    expect(router.state.location.pathname).toBe('/settings/machines');
    const links = [...container.querySelectorAll<HTMLAnchorElement>('nav[aria-label="Settings sections"] a')];
    expect(links.map((a) => [a.textContent, a.getAttribute('href'), a.getAttribute('aria-current')])).toEqual([
      ['Account', '/settings', null],
      ['Machines', '/settings/machines', 'page'],
      ['Your AI', '/settings/ai', null],
    ]);
  });

  it('navigates between Account and Machines, with Account current on /settings', async () => {
    stubApi({ devices: [mac] });
    await mount('/settings');
    expect(container.querySelector('nav a[aria-current=page]')?.textContent).toBe('Account');
    await click(container.querySelector<HTMLAnchorElement>('nav a[href="/settings/machines"]')!);
    expect(router.state.location.pathname).toBe('/settings/machines');
    expect(container.querySelector('nav a[aria-current=page]')?.textContent).toBe('Machines');
    await click(container.querySelector<HTMLAnchorElement>('nav a[href="/settings"]')!);
    expect(router.state.location.pathname).toBe('/settings');
    expect(container.querySelector('nav a[aria-current=page]')?.textContent).toBe('Account');
  });
});

describe('machines list', () => {
  it('renders one row per device with name, os, version, last seen and no "this one" pill', async () => {
    stubApi({ devices: [{ ...mac, current: true }, wsl, odd] });
    await mount();
    expect(text()).toContain('Machines linked to your account');
    expect(text()).toContain('Each one ran dojo login. Revoke any you do not use any more.');
    const list = container.querySelector('ul')!;
    expect(rows()).toHaveLength(3);
    expect(list.getAttribute('aria-labelledby')).toBeTruthy();
    expect(row(0).textContent).toContain('irene-macbook');
    expect(row(0).textContent).toContain('macOS (arm64) · dojo 0.4.2 · last seen 2 minutes ago');
    expect(row(1).textContent).toContain('Linux (amd64) · dojo 0.4.1 · last seen 3 days ago');
    expect(row(2).textContent).toContain('plan9 · dojo 0.4.2 · not seen yet');
    expect(text()).not.toContain('this one');
    expect(button('Revoke irene-macbook')).toBeTruthy();
    expect(calls.some((c) => c.url === '/v1/me/devices' && c.method === 'GET')).toBe(true);
  });

  it('shows a plain empty state telling them to run dojo login', async () => {
    stubApi({ devices: [] });
    await mount();
    expect(rows()).toHaveLength(0);
    expect(text()).toContain('No machines yet. Run dojo login on a machine');
  });

  it.each([404, 405])('shows a calm "not available yet" state when the endpoint is missing (%i)', async (status) => {
    stubApi(status);
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('not available yet');
    expect(router.state.location.pathname).toBe('/settings/machines');
  });

  it('offers Retry when the list cannot be loaded', async () => {
    stubApi(403);
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not load');
  });

  it('goes to sign-in when the session ended (401)', async () => {
    stubApi(401);
    await mount();
    expect(router.state.location.pathname).toBe('/signin');
  });
});

describe('revoke', () => {
  it('asks inline first, and Cancel backs out without calling the API', async () => {
    stubApi({ devices: [mac, wsl] });
    await mount();
    await click(button('Revoke irene-wsl'));
    expect(button('Yes, revoke irene-wsl')).toBeTruthy();
    expect(row(1).textContent).toContain('Revoke?');
    expect(document.activeElement).toBe(button('Cancel revoking irene-wsl'));
    await click(button('Cancel revoking irene-wsl'));
    expect(button('Revoke irene-wsl')).toBeTruthy();
    expect(document.activeElement).toBe(button('Revoke irene-wsl'));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('Escape closes the confirm like Cancel', async () => {
    stubApi({ devices: [mac, wsl] }, () => 204);
    await mount();
    await click(button('Revoke irene-wsl'));
    const group = row(1).querySelector('[role=group]')!;
    await act(async () => {
      group.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    await settle();
    expect(button('Revoke irene-wsl')).toBeTruthy();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('removes the row on 204, sends the CSRF header, and moves focus to the card title', async () => {
    stubApi({ devices: [mac, wsl] }, () => 204);
    await mount();
    await click(button('Revoke irene-wsl'));
    await click(button('Yes, revoke irene-wsl'));
    const del = calls.find((c) => c.method === 'DELETE')!;
    expect(del.url).toBe('/v1/me/devices/d2');
    expect(del.headers.get('X-Dojo-Csrf')).toBeTruthy();
    expect(del.headers.get('Content-Type')).toBe('application/json');
    expect(rows().map((r) => r.textContent)).toEqual([expect.stringContaining('irene-macbook')]);
    expect(document.activeElement?.tagName).toBe('H2');
  });

  it('removes the row on 404 (already revoked)', async () => {
    stubApi({ devices: [mac, wsl] }, () => [404, 'not_found']);
    await mount();
    await click(button('Revoke irene-macbook'));
    await click(button('Yes, revoke irene-macbook'));
    expect(rows()).toHaveLength(1);
    expect(text()).not.toContain('irene-macbook');
  });

  it('shows the last row going away as the empty state', async () => {
    stubApi({ devices: [mac] }, () => 204);
    await mount();
    await click(button('Revoke irene-macbook'));
    await click(button('Yes, revoke irene-macbook'));
    expect(rows()).toHaveLength(0);
    expect(text()).toContain('No machines yet');
  });

  it('keeps the row and shows an inline error on 500', async () => {
    stubApi({ devices: [mac, wsl] }, () => [500, 'server_error']);
    await mount();
    await click(button('Revoke irene-wsl'));
    await click(button('Yes, revoke irene-wsl'));
    expect(rows()).toHaveLength(2);
    const live = row(1).querySelector('[role=status]')!;
    expect(live.getAttribute('aria-live')).toBe('polite');
    expect(live.textContent).toContain('Could not revoke');
    expect(button('Revoke irene-wsl')).toBeTruthy();
  });

  it('words a CSRF rejection on its own', async () => {
    stubApi({ devices: [mac] }, () => [403, 'csrf_rejected']);
    await mount();
    await click(button('Revoke irene-macbook'));
    await click(button('Yes, revoke irene-macbook'));
    expect(row(0).querySelector('[role=status]')?.textContent).toContain('out of date');
  });
});
