// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { resetReadBlock, type Activity, type Ticket, type Today } from '../lib/read';
import { ticketHref } from './Today';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const role = { slug: 'support-l1', title: 'Support L1', tickets_passed: 7, tickets_total: 12 };
const perms = { id: 'support-l1/perms-03', title: 'The shared folder stopped accepting uploads', state: 'started' as const, lab_state: 'running' };
const ssh = { id: 'support-l1/ssh-01', title: "The new server won't let anyone in", state: null, lab_state: null };

let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;

const sudo = { id: 'support-l1/sudo-01', title: 'Who gave the intern sudo?' };

/** An activity answer ending on Wed 2026-11-25. */
function activityOf(extra: Partial<Activity> = {}): Activity {
  return { from: '2026-07-09', to: '2026-11-25', days: [], total: 0, recent: [], ...extra };
}
const passAt = (id: string, title: string, at: string, commit: string | null = null) => ({ ticket_id: id, title, at, commit });

function ticket(id: string, extra: Partial<Ticket> = {}): Ticket {
  return {
    id,
    title: id,
    role: 'support-l1',
    est_minutes: 15,
    tags: [],
    brief: 'Ticket OPS-114, from Elena (finance):\n\nHi! The team cannot save files in /srv/shared.\n\nThanks.',
    hint_count: 2,
    hints: [],
    progress: null,
    ...extra,
  };
}

/** `today` is the GET /v1/today answer: a body, or a status code. `extra` answers /v1/activity (a body or a status) and /v1/tickets/*. */
function stubApi(today: Today | number, extra: { activity?: Activity | number; tickets?: Record<string, Ticket> } = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/today') return typeof today === 'number' ? new Response(null, { status: today }) : Response.json(today);
      if (url.startsWith('/v1/activity') && extra.activity !== undefined)
        return typeof extra.activity === 'number' ? new Response(null, { status: extra.activity }) : Response.json(extra.activity);
      const id = url.startsWith('/v1/tickets/') ? decodeURIComponent(url.slice('/v1/tickets/'.length)) : '';
      if (extra.tickets?.[id]) return Response.json(extra.tickets[id]);
      return new Response(null, { status: 404 });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(path = '/today') {
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
const link = (name: string) => [...container.querySelectorAll('a')].find((a) => a.textContent === name);

beforeEach(() => {
  resetReadBlock();
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('ticketHref', () => {
  it('keeps the slash and escapes each segment', () => {
    expect(ticketHref('support-l1/perms-03')).toBe('/t/support-l1/perms-03');
    expect(ticketHref('a b/c?d')).toBe('/t/a%20b/c%3Fd');
  });
});

describe('Today with a ticket in progress', () => {
  beforeEach(async () => {
    stubApi({ current: perms, next: ssh, role, all_passed: false });
    await mount();
  });

  it('shows the ticket, lab state and a link into it', () => {
    expect(container.querySelector('h1')?.textContent).toBe(perms.title);
    expect(text()).toContain('In progress');
    expect(text()).toContain('support-l1/perms-03 · lab running');
    expect(link('Continue the ticket')?.getAttribute('href')).toBe('/t/support-l1/perms-03');
  });

  it('lists now and next', () => {
    const list = container.querySelector('section[aria-label="Recent and next"]')!;
    expect(list.textContent).toContain(perms.title);
    expect(list.querySelector<HTMLAnchorElement>('a[href="/t/support-l1/ssh-01"]')).not.toBeNull();
  });

  it('has the inbox bell, no search the app cannot back, and an avatar to Settings', () => {
    expect(container.querySelector('header button[aria-label^="Notifications"]')).not.toBeNull();
    expect(container.textContent).not.toContain('Search or run a command');
    const avatar = container.querySelector('header a[href="/settings"]')!;
    expect(avatar.getAttribute('aria-label')).toBe('Settings, @irene');
    expect(avatar.textContent).toBe('i');
  });

  it('shows an empty belt that claims no color or count', () => {
    expect(text()).toContain('Your belt');
    expect(text()).toContain('Marks come from how you work, not from tickets.');
    expect(text()).not.toMatch(/white belt|of 4 marks/i);
  });

  it('draws role progress as a progressbar', () => {
    const bar = container.querySelector('[role=progressbar]')!;
    expect(bar.getAttribute('aria-valuenow')).toBe('7');
    expect(bar.getAttribute('aria-valuemax')).toBe('12');
    expect(bar.getAttribute('aria-label')).toBe('Support L1, 7 of 12 tickets');
    expect(text()).toContain('5 tickets to go.');
  });

  it('has the nav with Today current, in the frame landmarks', () => {
    expect(container.querySelector('header')).not.toBeNull();
    expect(container.querySelector('main')).not.toBeNull();
    expect(container.querySelector('nav[aria-label="Primary"] [aria-current=page]')?.textContent).toBe('Today');
    expect(container.querySelector('nav[aria-label="Primary, tabs"] a[href="/path"]')).not.toBeNull();
  });
});

const startedProgress = {
  state: 'started' as const,
  lab: null,
  lab_state: 'running',
  check_count: 1,
  last_check_ok: false,
  last_check_at: new Date(Date.now() - 3 * 60_000).toISOString(),
  last_check_passed: 1,
  last_check_total: 3,
  last_check_output: 'nobody else can',
  first_passed_at: null,
  passed_seconds: null,
  passed_commit: null,
};

const threeDaysAgo = new Date(Date.now() - 3 * 86_400_000).toISOString();
const historyActivity = activityOf({
  days: [
    { day: '2026-11-20', count: 1 },
    { day: '2026-11-24', count: 3 },
  ],
  total: 4,
  recent: [passAt(sudo.id, sudo.title, threeDaysAgo), passAt('support-l1/perms-02', 'Fix the mount', threeDaysAgo)],
});

const calls = () => (fetch as unknown as { mock: { calls: string[][] } }).mock.calls.map((c) => c[0] ?? '');

describe('Today with history', () => {
  beforeEach(async () => {
    stubApi(
      { current: perms, next: ssh, role, all_passed: false },
      { activity: historyActivity, tickets: { [perms.id]: ticket(perms.id, { progress: startedProgress }) } },
    );
    await mount();
  });

  it('shows the client message parsed from the brief', () => {
    const msg = container.querySelector('section[aria-label="Message from Elena"]')!;
    expect(msg.textContent).toContain('Elena · Finance');
    expect(msg.textContent).toContain('OPS-114');
    expect(msg.textContent).toContain('The team cannot save files in /srv/shared.');
    expect(msg.textContent).not.toContain('Thanks.');
  });

  it('puts the last check in Your lab', () => {
    const lab = container.querySelector('section[aria-label="Your lab"]')!;
    expect(lab.textContent).toContain('3 min ago');
    expect(lab.textContent).toContain('1 of 3 checks pass');
  });

  it('lists the last passed ticket before now and next, with no commit while none is served', () => {
    const rows = [...container.querySelectorAll('section[aria-label="Recent and next"] li')].map((li) => li.textContent);
    expect(rows).toHaveLength(3);
    expect(rows[0]).toContain('Passed');
    expect(rows[0]).toContain('support-l1/sudo-01');
    expect(rows[1]).toContain('Now');
    expect(rows[2]).toContain('Next');
  });

  it('draws the activity panel: 140 cells, a text alternative, no focus stops, recent lines without a sha', () => {
    const panel = container.querySelector('section[aria-label="Activity"]')!;
    const grid = panel.querySelector('[role=img]')!;
    expect(grid.children).toHaveLength(140);
    expect(grid.getAttribute('aria-label')).toBe('4 tickets passed in the last 20 weeks, most on Nov 24');
    expect(grid.querySelectorAll('[tabindex], a, button')).toHaveLength(0);
    expect(panel.textContent).toContain('last 20 weeks');
    const lines = [...panel.querySelectorAll('li')].map((li) => li.textContent);
    expect(lines).toHaveLength(2);
    expect(lines[0]).toBe('Who gave the intern sudo?3 days ago');
  });

  it('shows the sha on a recent line only when one is served', async () => {
    act(() => root.unmount());
    root = createRoot(container);
    stubApi(
      { current: perms, next: ssh, role, all_passed: false },
      { activity: activityOf({ total: 1, days: [{ day: '2026-11-24', count: 1 }], recent: [passAt(sudo.id, sudo.title, threeDaysAgo, '3f9a1c2deadbeef')] }) },
    );
    await mount();
    expect(container.querySelector('section[aria-label="Activity"] li')?.textContent).toContain('3f9a1c2');
    expect(container.querySelector('section[aria-label="Recent and next"] li')?.textContent).toContain('3f9a1c2');
  });

  it('makes exactly one activity request, sending the browser zone', () => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(calls().filter((u) => u.startsWith('/v1/activity'))).toEqual([`/v1/activity?tz=${encodeURIComponent(zone)}`]);
    expect(calls().some((u) => u.startsWith('/v1/path'))).toBe(false);
  });

  it('shows the phone last-check card with its count', () => {
    const last = container.querySelector('section[aria-label="Last check"]')!;
    expect(last.textContent).toContain('1/3');
    expect(last.textContent).toContain('nobody else can');
  });

  it('offers a hint link when the ticket has hints', () => {
    expect(link('Open the ticket for hints')?.getAttribute('href')).toBe('/t/support-l1/perms-03');
  });
});

describe('Today activity edge states', () => {
  it('omits the panel and the passed row quietly when the read fails, other sections stay', async () => {
    stubApi({ current: perms, next: ssh, role, all_passed: false }, { activity: 500, tickets: { [perms.id]: ticket(perms.id) } });
    await mount();
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    expect(container.querySelector('section[aria-label="Activity"]')).toBeNull();
    expect(container.querySelector('[role=alert]')).toBeNull();
    expect(container.querySelector('h1')?.textContent).toBe(perms.title);
    expect(container.querySelector('section[aria-label="Recent and next"]')?.textContent).toContain(ssh.title);
  });

  it('omits the panel for a learner with no passes', async () => {
    stubApi({ current: perms, next: ssh, role, all_passed: false }, { activity: activityOf(), tickets: {} });
    await mount();
    expect(container.querySelector('section[aria-label="Activity"]')).toBeNull();
  });

  it('holds the panel space while the read is out', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url === '/v1/me') return Promise.resolve(Response.json(me));
        if (url === '/v1/today') return Promise.resolve(Response.json({ current: perms, next: ssh, role, all_passed: false }));
        return new Promise<Response>(() => {});
      }),
    );
    await mount();
    const hold = container.querySelector('aside section[aria-hidden=true]')!;
    expect(hold.className).toContain('min-h-[216px]');
  });
});

describe('Today read budget', () => {
  it('needs no ticket read for the passed row however many are passed', async () => {
    stubApi(
      { current: perms, next: ssh, role, all_passed: false },
      { activity: activityOf({ total: 9, recent: [passAt('support-l1/sudo-04', 'Fourth', threeDaysAgo)] }), tickets: { [perms.id]: ticket(perms.id) } },
    );
    await mount();
    expect(calls().filter((u) => u.startsWith('/v1/tickets/'))).toEqual(['/v1/tickets/support-l1/perms-03']);
    expect(container.querySelector('section[aria-label="Recent and next"]')?.textContent).toContain('support-l1/sudo-04');
  });
});

describe('Today on day one', () => {
  const justNow = new Date().toISOString();
  const oneToday = activityOf({ total: 1, days: [{ day: '2026-11-25', count: 1 }], recent: [passAt(sudo.id, sudo.title, justNow)] });
  const first_run = { id: sudo.id, title: sudo.title, first_passed_at: justNow, passed_commit_state: null };
  const patches = () => (fetch as unknown as { mock: { calls: [string, RequestInit?][] } }).mock.calls.filter(([, init]) => init?.method === 'PATCH');

  it('welcomes the learner when the API serves first_run', async () => {
    stubApi({ first_run, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: oneToday, tickets: { [ssh.id]: ticket(ssh.id) } });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Welcome to the team, @irene.');
    expect(text()).toContain('day one · done');
    expect(container.querySelector('section[aria-label="Your first ticket is done"]')?.textContent).toContain('sudo-01');
    expect(container.querySelector('section[aria-label="Next up"]')?.textContent).toContain('about 15 min');
    expect(link('Start the next one')?.getAttribute('href')).toBe('/t/support-l1/ssh-01');
    const note = container.querySelector('section[aria-label="Activity"]')!;
    expect(note.textContent).toContain('One so far: support-l1/sudo-01');
    expect(note.querySelector('[role=img]')).toBeNull();
    expect(container.querySelector('section[aria-label="Your first ticket is done"]')?.textContent).toContain('11 tickets to go in Support L1.');
    expect(calls().filter((u) => u.startsWith('/v1/activity'))).toHaveLength(1);
  });

  it('names the sha in the note once one is served', async () => {
    const withSha = { ...oneToday, recent: [passAt(sudo.id, sudo.title, justNow, '4c1e9a0ffff')] };
    stubApi({ first_run, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: withSha, tickets: { [ssh.id]: ticket(ssh.id) } });
    await mount();
    expect(container.querySelector('section[aria-label="Activity"]')?.textContent).toContain('One so far: 4c1e9a0 support-l1/sudo-01');
    expect(container.querySelector('section[aria-label="Your first ticket is done"]')?.textContent).toContain('4c1e9a0');
  });

  it('still welcomes the learner when the activity read fails, without a commit line', async () => {
    stubApi({ first_run, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: 500, tickets: {} });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Welcome to the team, @irene.');
    expect(container.querySelector('section[aria-label="Your first ticket is done"]')?.textContent).not.toContain('signed commit');
  });

  it('uses the plain headline and the grid without first_run', async () => {
    stubApi({ first_run: null, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: { ...oneToday, recent: [passAt(sudo.id, sudo.title, threeDaysAgo)] }, tickets: {} });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Ready for the next ticket.');
    expect(container.querySelector('section[aria-label="Activity"] [role=img]')).not.toBeNull();
    expect(link('Start ssh-01')).toBeDefined();
  });

  it('marks the first run seen when the learner starts the next one', async () => {
    stubApi({ first_run, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: oneToday, tickets: { [ssh.id]: ticket(ssh.id) } });
    await mount();
    expect(patches()).toHaveLength(0);
    await act(async () => link('Start the next one')!.click());
    await settle();
    expect(router.state.location.pathname).toBe('/t/support-l1/ssh-01');
    expect(patches()).toHaveLength(1);
    expect(patches()[0]![0]).toBe('/v1/me');
    expect(JSON.parse(String(patches()[0]![1]!.body))).toEqual({ first_run_seen: true });
  });

  it('offers Continue and marks it seen when a ticket is already in progress', async () => {
    stubApi({ first_run, current: perms, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: oneToday, tickets: { [perms.id]: ticket(perms.id) } });
    await mount();
    expect(link('Start the next one')).toBeUndefined();
    await act(async () => link('Continue')!.click());
    await settle();
    expect(router.state.location.pathname).toBe('/t/support-l1/perms-03');
    expect(patches()).toHaveLength(1);
  });

  it('still opens the ticket when the seen PATCH fails', async () => {
    stubApi({ first_run, current: null, next: ssh, role: { ...role, tickets_passed: 1 }, all_passed: false }, { activity: oneToday });
    const base = fetch as unknown as (url: string, init?: RequestInit) => Promise<Response>;
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => (init?.method === 'PATCH' ? Promise.resolve(new Response(null, { status: 500 })) : base(url, init))));
    await mount();
    await act(async () => link('Start the next one')!.click());
    await settle();
    expect(router.state.location.pathname).toBe('/t/support-l1/ssh-01');
  });
});

describe('Today without a ticket in progress', () => {
  it('offers the next ticket', async () => {
    stubApi({ current: null, next: ssh, role, all_passed: false });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Ready for the next ticket.');
    expect(link('Start ssh-01')?.getAttribute('href')).toBe('/t/support-l1/ssh-01');
    expect(link('See the whole path')?.getAttribute('href')).toBe('/path');
  });

  it('says every ticket is passed', async () => {
    stubApi({ current: null, next: null, role: { ...role, tickets_passed: 12 }, all_passed: true });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Every ticket is passed.');
    expect(link('See your path')).toBeDefined();
    expect(container.querySelector('[role=progressbar]')?.getAttribute('aria-valuenow')).toBe('12');
  });

  it('shows a plain empty state with no data', async () => {
    stubApi({ current: null, next: null, role: null, all_passed: false });
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Nothing to work on yet.');
  });
});

describe('Today between item', () => {
  const quiz = { id: 'support-l1/quiz-03', kind: 'quiz' as const, role: 'support-l1', title: 'Logs and permissions', est_minutes: 5 };
  const story = { id: 'support-l1/story-01', kind: 'story' as const, role: 'support-l1', title: 'Elena has asked you twice today. How do you reply to the second ticket?', est_minutes: 1 };
  const card = () => container.querySelector('section[aria-label="Between tickets"]');

  it('shows a story as the dark card with its link', async () => {
    stubApi({ current: perms, next: ssh, role, all_passed: false, between: story });
    await mount();
    expect(card()?.textContent).toContain('Story · your team');
    expect(card()?.textContent).toContain(story.title);
    expect(card()?.querySelector('a')?.getAttribute('href')).toBe('/story/support-l1/story-01');
    expect(card()?.querySelector('a')?.textContent).toBe('Open the story');
  });

  it('labels a quiz and links to the quiz page', async () => {
    stubApi({ current: null, next: ssh, role, all_passed: false, between: quiz });
    await mount();
    expect(card()?.textContent).toContain('Quiz · 5 min');
    expect(card()?.querySelector('a')?.getAttribute('href')).toBe('/quiz/support-l1/quiz-03');
  });

  it('links writing to /w and feedback to the story page', async () => {
    stubApi({ current: null, next: ssh, role, all_passed: false, between: { ...quiz, id: 'support-l1/write-01', kind: 'writing' } });
    await mount();
    expect(card()?.querySelector('a')?.getAttribute('href')).toBe('/w/support-l1/write-01');
    expect(card()?.textContent).toContain('Writing · 5 min');
    act(() => root.unmount());
    root = createRoot(container);
    stubApi({ current: null, next: ssh, role, all_passed: false, between: { ...quiz, id: 'support-l1/fb-01', kind: 'feedback' } });
    await mount();
    expect(card()?.querySelector('a')?.getAttribute('href')).toBe('/story/support-l1/fb-01');
  });

  it('is offered even when every ticket is passed', async () => {
    stubApi({ current: null, next: null, role: { ...role, tickets_passed: 12 }, all_passed: true, between: quiz });
    await mount();
    expect(card()).not.toBeNull();
  });

  it('is hidden when between is null or missing', async () => {
    stubApi({ current: perms, next: ssh, role, all_passed: false, between: null });
    await mount();
    expect(card()).toBeNull();
    act(() => root.unmount());
    root = createRoot(container);
    stubApi({ current: perms, next: ssh, role, all_passed: false });
    await mount();
    expect(card()).toBeNull();
  });
});

describe('copy command', () => {
  const copyBtn = () => container.querySelector<HTMLButtonElement>('button[aria-label="Copy command: dojo check"]')!;
  const notice = () => copyBtn().parentElement!.querySelector('[role=status]')!;

  beforeEach(async () => {
    stubApi({ current: perms, next: null, role, all_passed: false });
    await mount();
  });

  it('announces Copied in a live region outside the button', async () => {
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    await act(async () => copyBtn().click());
    expect(writeText).toHaveBeenCalledWith('dojo check');
    expect(notice().textContent).toBe('Copied');
    expect(copyBtn().contains(notice())).toBe(false);
  });

  it('announces a clipboard failure the same way', async () => {
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn(async () => Promise.reject(new Error('denied'))) } });
    await act(async () => copyBtn().click());
    expect(notice().textContent).toBe('Copy failed, select the command instead');
  });
});

describe('Today failures', () => {
  it('keeps a Today heading while loading', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => (url === '/v1/me' ? Promise.resolve(Response.json(me)) : new Promise<Response>(() => {}))));
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Today');
    expect(text()).toContain('Loading today...');
  });

  it('redirects to sign-in when the session ended', async () => {
    stubApi(401);
    await mount();
    expect(router.state.location.pathname).toBe('/signin');
  });

  it('shows a retry on a server error and loads on retry', async () => {
    stubApi(500);
    await mount();
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not load this');
    expect(container.querySelector('h1')?.textContent).toBe('Today');
    stubApi({ current: perms, next: null, role, all_passed: false });
    const retry = [...container.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!;
    await act(async () => retry.click());
    await settle();
    expect(container.querySelector('h1')?.textContent).toBe(perms.title);
  });

  it('says the account is suspended without a retry', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url === '/v1/me' ? Response.json(me) : Response.json({ error: 'account_suspended' }, { status: 403 }),
      ),
    );
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('suspended');
    expect([...container.querySelectorAll('button')].some((b) => b.textContent === 'Retry')).toBe(false);
  });
});
