// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { resetReadBlock } from '../lib/read';
import { crossings, interleave, roleStatuses } from './Path';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene-gh', email: null, language: 'en', usage_stats: true, analytics_consent: null };

const role = (slug: string, title: string, o: object = {}) => ({
  slug,
  title,
  summary: '',
  locked: false,
  passed: false,
  tickets_passed: 0,
  tickets_total: 0,
  items: [],
  ...o,
});

const catalog = {
  catalog_version: 'v1',
  paths: [
    { slug: 'systems-devops', title: 'Systems and DevOps', status: 'available', position: 1 },
    { slug: 'security', title: 'Security', status: 'planned', position: 2 },
  ],
  roles: [
    { slug: 'foundations', title: 'Foundations', summary: '', kind: 'foundations', has_exam: false, advanced: false },
    { slug: 'support-l1', title: 'Support L1', summary: '', kind: 'role', has_exam: true, advanced: false },
    { slug: 'soc-l1', title: 'SOC L1', summary: '', kind: 'role', has_exam: true, advanced: false },
  ],
  path_roles: [
    { path: 'systems-devops', role: 'foundations', position: 1 },
    { path: 'systems-devops', role: 'support-l1', position: 2 },
    { path: 'security', role: 'foundations', position: 1 },
    { path: 'security', role: 'support-l1', position: 2 },
    { path: 'security', role: 'soc-l1', position: 3 },
  ],
  items: [],
};

const fullPath = {
  path: 'systems-devops',
  chosen: false,
  roles: [
    role('foundations', 'Foundations', { passed: true, tickets_passed: 2, tickets_total: 2 }),
    role('support-l1', 'Support L1', {
      summary: 'Tickets, users, permissions, a calm reply',
      tickets_passed: 1,
      tickets_total: 2,
      items: [
        { id: 'support-l1/perms-03', title: 'Fix the shared folder', order: 1, state: 'passed', lab_state: null },
        { id: 'support-l1/disk-01', title: 'Disk full', order: 2, state: null, lab_state: null },
      ],
    }),
    role('systems-l2', 'Systems L2', { tickets_total: 3 }),
    role('devops', 'Junior DevOps', { locked: true }),
  ],
};

let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;
let visited: string[] = [];

function stub(pathRes: () => Response, catalogRes: () => Response = () => Response.json(catalog)) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/path') return pathRes();
      if (url === '/v1/catalog') return catalogRes();
      return Response.json({ error: 'x' }, { status: 404 });
    }),
  );
}

async function mount() {
  router = createMemoryRouter(routes, { initialEntries: ['/path'] });
  visited = [];
  router.subscribe((s) => visited.push(s.location.pathname));
  await act(async () => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 5; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

beforeEach(() => {
  resetReadBlock();
  vi.stubGlobal('EventSource', undefined);
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('roleStatuses', () => {
  it('marks the first open role as here and later ones as next', () => {
    expect(roleStatuses(fullPath.roles as never)).toEqual(['passed', 'here', 'next', 'soon']);
  });
});

describe('crossings', () => {
  it('finds shared roles, what they open and whether they are passed', () => {
    const c = crossings(catalog as never, fullPath as never);
    expect(c).toEqual([{ path: 'security', pathTitle: 'Security', shared: ['Foundations', 'Support L1'], opens: 'SOC L1', open: false, planned: true }]);
    const done = { ...fullPath, roles: fullPath.roles.map((r) => ({ ...r, passed: true })) };
    expect(crossings(catalog as never, done as never)[0]?.open).toBe(false);
    const live = { ...catalog, paths: [catalog.paths[0], { ...catalog.paths[1], status: 'available' }], items: [{ id: 'soc-l1/a', role: 'soc-l1', order: 1, kind: 'ticket', title: 'A', est_minutes: 5, tags: [] }] };
    expect(crossings(live as never, done as never)[0]?.open).toBe(true);
    expect(crossings(live as never, fullPath as never)[0]?.open).toBe(false);
    expect(crossings({ ...live, items: [] } as never, done as never)[0]?.open).toBe(false);
    const quizOnly = { ...live, items: [{ id: 'soc-l1/q', role: 'soc-l1', order: 1, kind: 'quiz', title: 'Q', est_minutes: 5, tags: [] }] };
    expect(crossings(quizOnly as never, done as never)[0]?.open).toBe(false);
    const noNext = { ...live, path_roles: live.path_roles.filter((x) => x.role !== 'soc-l1') };
    expect(crossings(noNext as never, done as never)[0]).toMatchObject({ opens: null, open: false });
  });
  it('is empty with no path or no overlap', () => {
    expect(crossings(catalog as never, { path: null, chosen: false, roles: [] })).toEqual([]);
    expect(crossings({ ...catalog, path_roles: [] } as never, fullPath as never)).toEqual([]);
  });
});

describe('interleave', () => {
  const t = (id: string, order: number) => ({ id, title: id, order, state: null, lab_state: null });
  const b = (id: string, order: number, kind = 'quiz') => ({ id, kind, title: id, order, done: false });
  it('sorts tickets and between items by order, tickets first on a tie', () => {
    const rows = interleave([t('t1', 1), t('t2', 3)] as never, [b('q', 2), b('s', 3, 'story')] as never);
    expect(rows.map((r) => r.item.id)).toEqual(['t1', 'q', 't2', 's']);
  });
});

describe('Path page', () => {
  it('lists between items among the tickets with kind, done state and links', async () => {
    const roles = [
      role('support-l1', 'Support L1', {
        tickets_total: 2,
        items: [
          { id: 'support-l1/perms-03', title: 'Fix the shared folder', order: 1, state: 'passed', lab_state: null },
          { id: 'support-l1/disk-01', title: 'Disk full', order: 3, state: null, lab_state: null },
        ],
        between_items: [
          { id: 'support-l1/quiz-01', kind: 'quiz', title: 'Users and logs', order: 2, done: true },
          { id: 'support-l1/write-01', kind: 'writing', title: 'Standup update', order: 4, done: false },
          { id: 'support-l1/story-01', kind: 'story', title: 'Elena asks twice', order: 5, done: false },
          { id: 'support-l1/fb-01', kind: 'feedback', title: 'Reply to Elena', order: 6, done: false },
        ],
      }),
    ];
    stub(() => Response.json({ path: 'systems-devops', chosen: true, roles }));
    await mount();
    const rows = [...container.querySelectorAll('details ol > li')];
    expect(rows.map((r) => r.querySelector('a')?.getAttribute('href'))).toEqual([
      '/t/support-l1/perms-03',
      '/quiz/support-l1/quiz-01',
      '/t/support-l1/disk-01',
      '/w/support-l1/write-01',
      '/story/support-l1/story-01',
      '/story/support-l1/fb-01',
    ]);
    expect(rows[1]?.textContent).toContain('Quiz');
    expect(rows[1]?.textContent).toContain('Done');
    expect(rows[3]?.textContent).toContain('Writing');
    expect(rows[3]?.textContent).toContain('Not done');
    expect(rows[4]?.textContent).toContain('Story');
    expect(rows[5]?.textContent).toContain('Feedback');
  });

  it('keeps the list for a role with no between items', async () => {
    stub(() => Response.json(fullPath));
    await mount();
    expect(container.querySelectorAll('details ol > li').length).toBe(2);
  });

  it('renders the timeline with status, exam line, progress and ticket links', async () => {
    stub(() => Response.json(fullPath));
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Your way to Junior DevOps');
    expect(container.textContent).toContain('path · Systems and DevOps');
    const items = container.querySelectorAll('ol[aria-label="Roles on your path"] > li');
    expect(items).toHaveLength(4);
    const text = (i: number) => items[i]?.textContent ?? '';
    expect(text(0)).toContain('Passed');
    expect(text(1)).toContain('You are here');
    expect(text(1)).toContain('ends in a role exam: one work day');
    expect(text(1)).toContain('1 of 2 tickets');
    expect(items[1]?.querySelector('p')?.textContent).toBe('Tickets, users, permissions, a calm reply');
    expect(items[0]?.querySelector('p')).toBeNull();
    expect(text(2)).toContain('after Support L1');
    expect(text(3)).not.toContain('after');
    expect(items[2]?.querySelector('details')).not.toBeNull();
    expect(container.querySelectorAll('ol[aria-label="Roles on your path"] [aria-hidden="true"]').length).toBeGreaterThan(0);
    expect(text(3)).toContain('the destination');
    expect(items[3]?.querySelector('.sr-only')?.textContent).toBe('coming later');
    expect(text(0)).toContain('done');
    const link = container.querySelector('a[href="/t/support-l1/perms-03"]');
    expect(link?.textContent).toContain('Fix the shared folder');
    expect(link?.textContent).toContain('Passed');
    expect(container.querySelector('a[href="/t/support-l1/disk-01"]')?.textContent).toContain('Not started');
  });

  it('shows the belt panel and the crossing paths with headings in order', async () => {
    stub(() => Response.json(fullPath));
    await mount();
    const heads = [...container.querySelectorAll('h1, h2')].map((h) => h.tagName);
    expect(heads[0]).toBe('H1');
    expect(heads.slice(1).every((t) => t === 'H2')).toBe(true);
    const text = container.textContent ?? '';
    expect(text).toContain('Your belt · how you work, on every path');
    expect(container.querySelector('a[href="/paths"]')?.textContent).toBe('What the belt measures');
    expect(text).toContain('Paths that cross yours');
    expect(text).toContain('Foundations and Support L1 also opens SOC L1 on the Security path, coming later');
    expect(text).toContain('Coming later');
    expect(text).toContain('Switching path keeps every role you passed.');
  });

  it('omits the crossing panel when the catalog fails', async () => {
    stub(
      () => Response.json(fullPath),
      () => Response.json({ error: 'x' }, { status: 401 }),
    );
    await mount();
    expect(container.textContent).not.toContain('Paths that cross yours');
    expect(container.textContent).toContain('Your belt');
  });

  it('still renders the roles when the catalog fails, without exam lines or path title', async () => {
    stub(
      () => Response.json(fullPath),
      () => Response.json({ error: 'x' }, { status: 401 }),
    );
    await mount();
    expect(container.querySelectorAll('ol[aria-label="Roles on your path"] > li')).toHaveLength(4);
    expect(container.textContent).not.toContain('role exam');
    expect(container.textContent).not.toContain('path ·');
  });

  it('announces a locked middle role as coming later', async () => {
    const roles = [role('a', 'Alpha', { passed: true }), role('b', 'Beta'), role('c', 'Gamma', { locked: true }), role('d', 'Delta', { locked: true })];
    stub(() => Response.json({ path: 'systems-devops', chosen: true, roles }));
    await mount();
    const items = container.querySelectorAll('ol[aria-label="Roles on your path"] > li');
    expect(items[2]?.textContent).toContain('coming later');
    expect(items[2]?.querySelector('.sr-only')?.textContent).toBe('coming later');
    expect(items[3]?.textContent).toContain('the destination');
  });

  it('handles every role passed', async () => {
    const roles = [role('a', 'Alpha', { passed: true }), role('b', 'Beta', { passed: true })];
    stub(() => Response.json({ path: 'systems-devops', chosen: true, roles }));
    await mount();
    expect(container.textContent).not.toContain('You are here');
    expect(container.querySelectorAll('ol[aria-label="Roles on your path"] > li')).toHaveLength(2);
    expect(container.querySelector('h1')?.textContent).toBe('Your way to Beta');
  });

  it('handles a single-role path', async () => {
    stub(() => Response.json({ path: 'systems-devops', chosen: true, roles: [role('foundations', 'Foundations')] }));
    await mount();
    expect(container.textContent).toContain('One role, each a real job');
    expect(container.textContent).toContain('You are here');
  });

  it('omits the exam line for a role without an exam', async () => {
    stub(() => Response.json({ path: 'systems-devops', chosen: true, roles: [role('foundations', 'Foundations')] }));
    await mount();
    expect(container.textContent).not.toContain('role exam');
  });

  it('says so when no path is open', async () => {
    stub(() => Response.json({ path: null, chosen: false, roles: [] }));
    await mount();
    expect(container.textContent).toContain('No path is open yet');
  });

  it('shows a plain error with retry on a failed read', async () => {
    stub(() => Response.json({ error: 'server_error' }, { status: 500 }));
    await mount();
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    const alert = container.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('Could not load this. Please try again.');
    expect(alert?.querySelector('button')?.textContent).toBe('Retry');
  });

  it('sends a signed-out read to /signin', async () => {
    stub(() => Response.json({ error: 'unauthenticated' }, { status: 401 }));
    await mount();
    expect(visited).toContain('/signin');
  });
});
