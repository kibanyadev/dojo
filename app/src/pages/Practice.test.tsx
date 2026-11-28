// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { resetReadBlock, type PracticeItem } from '../lib/read';
import { applyFilters, NO_FILTERS } from './Practice';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene-gh', email: null, language: 'en', usage_stats: true, analytics_consent: null };

const item = (o: Partial<PracticeItem> & Pick<PracticeItem, 'id' | 'title'>): PracticeItem => ({
  kind: 'practice',
  role: null,
  est_minutes: 20,
  tags: [],
  difficulty: null,
  state: null,
  lab_state: null,
  ...o,
});

const items: PracticeItem[] = [
  item({ id: 'practice/slow-shop', title: 'The slow shop', est_minutes: 40, tags: ['nginx', 'performance'], difficulty: 'medium' }),
  item({ id: 'practice/disk-full', title: 'Disk full at 3 a.m.', tags: ['storage'], difficulty: 'easy', state: 'passed' }),
  item({ id: 'practice/always-dns', title: 'DNS, it is always DNS', tags: ['networking'], difficulty: 'hard' }),
  item({ id: 'support-l1/perms-03', kind: 'ticket', role: 'support-l1', title: 'Fix the shared folder', tags: ['storage'], state: 'passed' }),
];

const catalog = {
  catalog_version: 'v1',
  paths: [],
  roles: [{ slug: 'support-l1', title: 'Support L1', summary: '', kind: 'role', has_exam: true, advanced: false }],
  path_roles: [],
  items: [],
  marks: [],
};

let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;
let visited: string[] = [];

function stub(practice: () => Response) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/practice') return practice();
      if (url === '/v1/catalog') return Response.json(catalog);
      return Response.json({ error: 'x' }, { status: 404 });
    }),
  );
}

async function mount() {
  router = createMemoryRouter(routes, { initialEntries: ['/practice'] });
  visited = [];
  router.subscribe((st) => visited.push(st.location.pathname));
  await act(async () => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 5; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

const titles = () => [...container.querySelectorAll('ul[aria-label="Practice scenarios"] h2')].map((h) => h.textContent);
const chip = (name: string) => [...container.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')].find((b) => b.textContent?.replace('✓ ', '') === name)!;
const press = async (name: string) => {
  await act(async () => chip(name).click());
};

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

describe('applyFilters', () => {
  it('combines groups and keeps the order', () => {
    expect(applyFilters(items, NO_FILTERS)).toHaveLength(4);
    expect(applyFilters(items, { ...NO_FILTERS, tag: 'storage' }).map((i) => i.id)).toEqual(['practice/disk-full', 'support-l1/perms-03']);
    expect(applyFilters(items, { ...NO_FILTERS, tag: 'storage', kind: 'ticket' }).map((i) => i.id)).toEqual(['support-l1/perms-03']);
  });
});

describe('Practice page', () => {
  it('renders the copy, the cards in API order and the start command', async () => {
    stub(() => Response.json({ items }));
    await mount();
    expect(container.querySelector('h1')?.textContent).toBe('Broken servers, on demand');
    expect(container.textContent).toContain('never block your path');
    expect(titles()).toEqual(['The slow shop', 'Disk full at 3 a.m.', 'DNS, it is always DNS', 'Fix the shared folder']);
    expect(container.querySelector('button[aria-label="Copy command: dojo start practice/disk-full"]')).not.toBeNull();
    expect(container.querySelector('a[href="/t/practice/slow-shop"]')?.textContent).toBe('The slow shop');
    expect(container.querySelector('nav[aria-label="Primary"] a[href="/practice"]')?.getAttribute('aria-current')).toBe('page');
  });

  it('labels practice by difficulty and tickets as revisits, with passed in text', async () => {
    stub(() => Response.json({ items }));
    await mount();
    const cards = [...container.querySelectorAll('ul[aria-label="Practice scenarios"] > li')];
    expect(cards[0]?.textContent).toContain('Medium');
    expect(cards[0]?.textContent).toContain('40 min');
    expect(cards[0]?.textContent).toContain('nginx · performance');
    expect(cards[0]?.textContent).not.toContain('Passed');
    expect(cards[1]?.textContent).toContain('Passed');
    expect(cards[3]?.textContent).toContain('Revisit');
    expect(cards[3]?.textContent).toContain('Support L1');
    expect(cards[3]?.textContent).toContain('Passed');
  });

  it('filters by kind, role, topic and difficulty with pressed chips, and All resets', async () => {
    stub(() => Response.json({ items }));
    await mount();
    expect(chip('All').getAttribute('aria-pressed')).toBe('true');
    await press('Revisit');
    expect(titles()).toEqual(['Fix the shared folder']);
    expect(chip('Revisit').getAttribute('aria-pressed')).toBe('true');
    expect(chip('All').getAttribute('aria-pressed')).toBe('false');
    await press('Revisit');
    expect(titles()).toHaveLength(4);
    await press('Practice');
    expect(titles()).toHaveLength(3);
    await press('Practice');
    await press('Support L1');
    expect(titles()).toEqual(['Fix the shared folder']);
    await press('All');
    await press('Storage');
    expect(titles()).toEqual(['Disk full at 3 a.m.', 'Fix the shared folder']);
    await press('Easy');
    expect(titles()).toEqual(['Disk full at 3 a.m.']);
    await press('All');
    await press('Hard');
    expect(titles()).toEqual(['DNS, it is always DNS']);
    expect(chip('Hard').getAttribute('aria-pressed')).toBe('true');
  });

  it('says so when no card matches and offers to show all', async () => {
    stub(() => Response.json({ items }));
    await mount();
    await press('Easy');
    await press('Revisit');
    expect(titles()).toEqual([]);
    expect(container.textContent).toContain('Nothing matches these filters.');
    await act(async () => [...container.querySelectorAll('button')].find((b) => b.textContent === 'Show all')!.click());
    expect(titles()).toHaveLength(4);
  });

  it('shows the empty state with no chips', async () => {
    stub(() => Response.json({ items: [] }));
    await mount();
    expect(container.textContent).toContain('No scenarios yet. New ones arrive every month.');
    expect(container.querySelector('button[aria-pressed]')).toBeNull();
  });

  it('shows an error with retry, and recovers', async () => {
    let fail = true;
    stub(() => (fail ? Response.json({ error: 'x' }, { status: 500 }) : Response.json({ items })));
    await mount();
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not load this. Please try again.');
    fail = false;
    await act(async () => [...container.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!.click());
    for (let i = 0; i < 3; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
    expect(titles()).toHaveLength(4);
  });

  it('sends a signed-out session to sign-in', async () => {
    stub(() => Response.json({ error: 'x' }, { status: 401 }));
    await mount();
    expect(visited).toContain('/signin');
  });
});
