// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { parseBlocks } from '../lib/markdown';
import { readBlockedUntil, resetReadBlock, type Ticket, type TicketProgress } from '../lib/read';
import { setOffline } from '../lib/offline';
import { duration, shortDate, viewOf } from './Ticket';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const ID = 'support-l1/perms-03';
const HINTS = ['Read `ls -ld shared`.', 'Read `man chmod`.', 'Pointer three.'];

const progress = (over: Partial<TicketProgress> = {}): TicketProgress => ({
  state: 'started',
  lab: 'local',
  lab_state: 'running',
  check_count: 0,
  last_check_ok: null,
  last_check_at: null,
  last_check_passed: null,
  last_check_total: null,
  last_check_output: null,
  first_passed_at: null,
  passed_seconds: null,
  passed_commit: null,
  ...over,
});

const ticket = (over: Partial<Ticket> = {}): Ticket => ({
  id: ID,
  title: 'The shared folder stopped accepting uploads',
  role: 'support-l1',
  est_minutes: 40,
  tags: [],
  brief: 'Elena says the team cannot save files.\n\n```\nls -ld /srv/shared\n```\n\n<script>alert(1)</script> and `dojo check`',
  hint_count: 3,
  hints: [],
  progress: null,
  ...over,
});

let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;
let current: Ticket | number;
let posts: string[];
let limited = false;

function stubApi() {
  posts = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/v1/me') return Response.json(me);
      if (init?.method === 'POST') {
        posts.push(url);
        const n = Number(url.at(-1));
        if (n === 2 && limited) return new Response(null, { status: 429, headers: { 'Retry-After': '1' } });
        if (n === 3) return Response.json({ error: 'hint_locked' }, { status: 409 });
        return Response.json({ n, text: HINTS[n - 1] });
      }
      if (url === `/v1/tickets/${ID}`) return typeof current === 'number' ? new Response(null, { status: current }) : Response.json(current);
      return new Response(null, { status: 404 });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(t: Ticket | number, path = `/t/${ID}`) {
  current = t;
  stubApi();
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

const text = () => container.textContent ?? '';
const button = (name: string) => [...container.querySelectorAll('button')].find((b) => (b.getAttribute('aria-label') ?? b.textContent) === name);

beforeEach(() => {
  resetReadBlock();
  limited = false;
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('helpers', () => {
  it('formats durations', () => {
    expect(duration(34 * 60)).toBe('about 34 min');
    expect(duration(10)).toBe('about 1 min');
    expect(duration(65 * 60)).toBe('about 1 h');
    expect(duration(185 * 60)).toBe('about 3 h');
  });

  it('maps progress to a view', () => {
    expect(viewOf(null)).toBe('new');
    expect(viewOf(progress({ lab_state: 'starting' }))).toBe('starting');
    expect(viewOf(progress({ lab_state: null }))).toBe('running');
    expect(viewOf(progress({ state: 'passed' }))).toBe('passed');
    expect(viewOf(progress({ state: 'revisit', lab_state: 'expired' }))).toBe('revisit');
  });

  it('formats dates as month day', () => {
    expect(shortDate('2026-11-24T12:00:00Z')).toBe('Nov 24');
  });

  it('parses the markdown subset', () => {
    expect(parseBlocks('a\nb\n\n- x\n- y\n\n```\nc\n```')).toEqual([
      { kind: 'p', text: 'a b' },
      { kind: 'ul', items: ['x', 'y'] },
      { kind: 'code', text: 'c' },
    ]);
  });
});

describe('Ticket not started', () => {
  beforeEach(() => mount(ticket()));

  it('has one h1, the id badge and the start command', () => {
    expect(container.querySelectorAll('h1')).toHaveLength(1);
    expect(container.querySelector('h1')?.textContent).toBe('The shared folder stopped accepting uploads');
    expect(text()).toContain('Not started');
    expect(text()).toContain(ID);
    expect(container.querySelector(`button[aria-label="Copy command: dojo start ${ID}"]`)).not.toBeNull();
  });

  it('opens the report form for this ticket from the report link', async () => {
    await act(async () => button('Report a broken ticket')!.click());
    const dialog = container.querySelector('[role=dialog]')!;
    expect(dialog.textContent).toContain('Report a broken ticket');
    expect(document.activeElement?.tagName).toBe('TEXTAREA');
    expect(dialog.textContent).toContain('Include the output of my last check');
  });

  it('renders the brief with code blocks and never raw HTML', () => {
    expect(container.querySelector('pre')?.textContent).toBe('ls -ld /srv/shared');
    expect(container.querySelector('script')).toBeNull();
    expect(text()).toContain('<script>alert(1)</script>');
  });

  it('opens hint 1 in order and locks the rest', async () => {
    expect(button('Show hint 2')).toBeUndefined();
    expect(text()).toContain('unlocks after hint 1');
    await act(async () => button('Show hint 1')!.click());
    await settle();
    expect(posts).toEqual([`/v1/tickets/${ID}/hints/1`]);
    expect(container.querySelector('section[aria-label=Hints] code')?.textContent).toBe('ls -ld shared');
    expect(text()).toContain('1 of 3 open');
    expect(button('Show hint 2')).toBeDefined();
  });
});

const checked = { check_count: 2, last_check_ok: false, last_check_passed: 1, last_check_total: 3 };
const badge = () => container.querySelector('main span')?.textContent;
const startCmd = `button[aria-label="Copy command: dojo start ${ID}"]`;

describe('Ticket in progress', () => {
  it('shows the check command, the last check and the output block', async () => {
    await mount(ticket({ progress: progress({ ...checked, last_check_at: new Date().toISOString(), last_check_output: 'FAIL: other can write <b>' }) }));
    expect(text()).toContain('In progress');
    expect(container.querySelector('button[aria-label="Copy command: dojo check"]')).not.toBeNull();
    expect(text()).toContain('Results show here.');
    const status = container.querySelector('section[aria-label="Last check"] [role=status]')!;
    expect(status.textContent).toBe('Not yet · 1 of 3 checks pass · just now');
    const pre = container.querySelector('pre[aria-label="Check output"]')!;
    expect(pre.textContent).toBe('FAIL: other can write <b>');
    expect(pre.querySelector('b')).toBeNull();
    expect(container.querySelector('figcaption')?.textContent).toBe('last dojo check · from your laptop');
  });

  it('drops the laptop suffix for a hosted lab', async () => {
    await mount(ticket({ progress: progress({ ...checked, lab: 'hosted', last_check_output: 'x' }) }));
    expect(container.querySelector('figcaption')?.textContent).toBe('last dojo check');
  });

  it('hides the Hints tab when the ticket has no hints', async () => {
    await mount(ticket({ hint_count: 0, progress: progress() }));
    expect([...container.querySelectorAll('[role=tab]')].map((t) => t.textContent)).toEqual(['Ticket', 'Check']);
  });

  it('gives each tabpanel a tab stop', async () => {
    await mount(ticket({ progress: progress() }));
    expect(container.querySelector('#panel-ticket')?.getAttribute('tabindex')).toBe('0');
  });

  it('leaves the output block out before the first check', async () => {
    await mount(ticket({ progress: progress() }));
    expect(container.querySelector('pre[aria-label="Check output"]')).toBeNull();
    expect(container.querySelector('section[aria-label="Last check"]')).toBeNull();
  });

  it('offers the three phone tabs and keeps one h1 and ordered headings', async () => {
    await mount(ticket({ progress: progress() }));
    const tabs = [...container.querySelectorAll('[role=tab]')];
    expect(tabs.map((t) => t.textContent)).toEqual(['Ticket', 'Hints', 'Check']);
    expect(tabs.map((t) => t.getAttribute('aria-selected'))).toEqual(['true', 'false', 'false']);
    await act(async () => (tabs[1] as HTMLElement).click());
    expect(container.querySelector('#tab-hints')?.getAttribute('aria-selected')).toBe('true');
    expect(container.querySelector('#panel-ticket')?.className).toContain('max-md:hidden');
    expect(container.querySelector('#panel-hints')?.className).not.toContain('max-md:hidden');
    expect(container.querySelectorAll('h1')).toHaveLength(1);
    expect([...container.querySelectorAll('h1,h2')].map((h) => h.tagName)[0]).toBe('H1');
  });

  it('moves between tabs with the arrow keys', async () => {
    await mount(ticket({ progress: progress() }));
    const first = container.querySelector('#tab-ticket') as HTMLElement;
    await act(async () => first.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })));
    expect(container.querySelector('#tab-hints')?.getAttribute('aria-selected')).toBe('true');
  });
});

describe('Ticket starting', () => {
  it('says the lab is starting, with no command and no invented steps', async () => {
    await mount(ticket({ progress: progress({ lab_state: 'starting' }) }));
    expect(badge()).toBe('Starting lab');
    expect(text()).toContain('Your lab is booting. This can take a few minutes the first time on this computer.');
    expect(text()).toContain('lab starting');
    expect(container.querySelector('button[aria-label^="Copy command"]')).toBeNull();
    expect(container.querySelector('[role=tab]')).toBeNull();
  });
});

describe('Ticket did not start', () => {
  it('offers dojo doctor and the start command, and says the online lab is not available', async () => {
    await mount(ticket({ progress: progress({ lab_state: 'failed' }) }));
    expect(text()).toContain("Lab didn't start");
    expect(text()).toContain("The lab didn't start");
    expect(text()).toContain('dojo doctor');
    expect(text()).not.toContain('Nothing is broken');
    expect(text()).toContain('An online lab is not available yet.');
    expect(container.querySelector(startCmd)).not.toBeNull();
  });
});

describe('Ticket expired', () => {
  it('explains the 7 days and offers the start command and doctor', async () => {
    await mount(ticket({ progress: progress({ lab_state: 'expired' }) }));
    expect(badge()).toBe('Lab expired');
    expect(text()).toContain('Your lab expired');
    expect(text()).toContain('Your lab was marked expired after 7 days without a report from the CLI. Run dojo start again.');
    expect(text()).not.toContain('your work is in your repo');
    expect(text()).toContain('If the CLI seems stuck, run dojo doctor.');
    expect(container.querySelector(startCmd)).not.toBeNull();
  });
});

describe('Ticket passed', () => {
  const passed = (over: Partial<TicketProgress> = {}) =>
    ticket({ hints: HINTS.map((t, i) => ({ n: i + 1, text: t })), progress: progress({ state: 'passed', check_count: 3, last_check_ok: true, last_check_passed: 3, last_check_total: 3, passed_seconds: 34 * 60, ...over }) });

  it('shows time and checks, every hint, and no signed commit in M2', async () => {
    await mount(passed());
    expect(text()).toContain('Ticket passed');
    expect(text()).toContain('You fixed it in about 34 min.');
    expect(text()).toContain('passed in about 34 min');
    expect(container.querySelector('dl')?.textContent).toContain('about 34 min');
    expect(container.querySelector('dl')?.textContent).toContain('3/3');
    expect(container.querySelector('dl')?.textContent).toContain('checks pass');
    expect(text()).toContain('3 of 3 open');
    expect(text()).not.toContain('signed');
    expect(button('Show hint 1')).toBeUndefined();
    expect(container.querySelector('a[href="/path"]')).not.toBeNull();
  });

  it('shows the commit as signed only when it is verified', async () => {
    await mount(passed({ passed_commit: '3f9a1c2d9e0b', passed_commit_state: 'verified' }));
    expect(text()).toContain('signed');
    expect(text()).toContain('3f9a1c2');
    expect(text()).not.toContain('3f9a1c2d');
  });

  it.each(['pending', 'unverified'] as const)('does not claim signed for a %s commit', async (state) => {
    await mount(passed({ passed_commit: '3f9a1c2d9e0b', passed_commit_state: state }));
    expect(text()).not.toContain('signed');
  });
});

describe('Ticket revisit', () => {
  it('shows practice mode with the date, the plain start command and the hints open', async () => {
    const first = new Date(Date.now() - 2 * 86_400_000).toISOString();
    await mount(ticket({ hints: HINTS.map((t, i) => ({ n: i + 1, text: t })), progress: progress({ state: 'revisit', first_passed_at: first, passed_seconds: 2040 }) }));
    expect(badge()).toBe('Passed 2 days ago');
    expect(text()).toContain('practice mode');
    expect(text()).toContain(`You passed this ticket on ${shortDate(first)} in about 34 min.`);
    expect(text()).toContain('hint 1 · hint 2 · hint 3 open');
    expect(text()).toContain("It doesn't change your progress.");
    expect(container.querySelector(startCmd)).not.toBeNull();
    expect(text()).not.toContain('--again');
  });
});

describe('Ticket hints', () => {
  it('keeps unopened hints in dashed cards with the unlock rule', async () => {
    await mount(ticket({ hints: [{ n: 1, text: HINTS[0]! }], progress: progress() }));
    expect(text()).toContain('1 of 3 open');
    expect(text()).toContain('unlocks after hint 2');
    expect(button('Show hint 2')?.textContent).toBe('Show');
  });
});

describe('Ticket failures', () => {
  it('says a locked hint is locked', async () => {
    await mount(ticket({ hints: [{ n: 1, text: HINTS[0]! }, { n: 2, text: HINTS[1]! }] }));
    await act(async () => button('Show hint 3')!.click());
    await settle();
    expect(text()).toContain('Open the earlier hint first.');
  });

  it('blocks reads and keeps the hint button disabled after a 429 until Retry-After', async () => {
    limited = true;
    await mount(ticket({ hints: [{ n: 1, text: HINTS[0]! }] }));
    await act(async () => button('Show hint 2')!.click());
    await settle();
    expect(text()).toContain('Too many requests');
    expect(button('Show hint 2')!.disabled).toBe(true);
    expect(readBlockedUntil()).toBeGreaterThan(Date.now());
    await act(async () => new Promise((r) => setTimeout(r, 1100)));
    expect(button('Show hint 2')!.disabled).toBe(false);
  });

  it('reports a missing ticket with a way back', async () => {
    await mount(404);
    expect(container.querySelector('[role=alert]')?.textContent).toContain('could not find that ticket');
    expect(container.querySelector('a[href="/path"]')).not.toBeNull();
  });

  it('rejects a malformed id without a request', async () => {
    await mount(ticket(), '/t/nope');
    expect(container.querySelector('[role=alert]')?.textContent).toContain('could not find that ticket');
  });

  it('redirects to sign-in when the session ended', async () => {
    await mount(401);
    expect(router.state.location.pathname).toBe('/signin');
  });

  it('keeps a heading while loading', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => (url === '/v1/me' ? Promise.resolve(Response.json(me)) : new Promise<Response>(() => {}))));
    router = createMemoryRouter(routes, { initialEntries: [`/t/${ID}`] });
    await act(async () => {
      root.render(
        <QueryClientProvider client={new QueryClient()}>
          <RouterProvider router={router} />
        </QueryClientProvider>,
      );
    });
    await settle();
    expect(container.querySelector('h1')?.textContent).toBe('Ticket');
    expect(text()).toContain('Loading the ticket...');
  });
});

describe('Ticket offline (W4)', () => {
  afterEach(() => setOffline(false));

  async function mountOffline(t: Ticket) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (init?.method && init.method !== 'GET') {
          posts.push(url);
          return Response.json({});
        }
        if (url === '/v1/me') return Response.json(me, { headers: { 'X-Dojo-Offline': '1' } });
        if (url === `/v1/tickets/${ID}`) return Response.json(t, { headers: { 'X-Dojo-Offline': '1' } });
        return new Response(null, { status: 404 });
      }),
    );
    posts = [];
    router = createMemoryRouter(routes, { initialEntries: [`/t/${ID}`] });
    await act(async () => {
      root.render(
        <QueryClientProvider client={new QueryClient()}>
          <RouterProvider router={router} />
        </QueryClientProvider>,
      );
    });
    await settle();
  }

  it('shows the banner, keeps the opened hints and disables the writes', async () => {
    await mountOffline(ticket({ hints: [{ n: 1, text: HINTS[0] ?? '' }], progress: progress() }));
    const banner = container.querySelector('[role=status]');
    expect(text()).toContain('You are offline. Your lab keeps running on your laptop, and dojo check results show here when you are back.');
    expect(banner).not.toBeNull();
    expect(text()).toContain('Read ls -ld shared');
    expect(text()).toContain('Written hints are saved with the ticket. Asking Carmen waits for your connection.');
    expect((button('Show hint 2') as HTMLButtonElement).disabled).toBe(true);
    expect((button('Report a broken ticket') as HTMLButtonElement).disabled).toBe(true);
    await act(async () => button('Show hint 2')?.click());
    expect(posts).toEqual([]);
  });

  it('shows no banner online', async () => {
    await mount(ticket());
    expect(text()).not.toContain('You are offline');
    expect((button('Show hint 1') as HTMLButtonElement).disabled).toBe(false);
  });
});
