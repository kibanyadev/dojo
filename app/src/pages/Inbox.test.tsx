// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { resetReadBlock } from '../lib/read';
import { formatInboxTime, groupInbox, safeInboxUrl, type InboxItem } from '../lib/inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene-gh', email: null, language: 'en', usage_stats: true, analytics_consent: null };

const NOW = new Date();
const iso = (daysAgo: number) => new Date(NOW.getTime() - daysAgo * 86_400_000).toISOString();

const row = (o: Partial<InboxItem> & Pick<InboxItem, 'id' | 'title'>): InboxItem => ({
  kind: 'check',
  ref: o.id,
  body: 'A body',
  url: '/t/support-l1/perms-02',
  read: false,
  at: new Date(NOW.getTime() - 60_000).toISOString(),
  ...o,
});

let items: InboxItem[];
let calls: { url: string; method: string; headers: Headers }[];
let container: HTMLDivElement;
let root: Root;
let router: ReturnType<typeof createMemoryRouter>;

function stub(pages?: Record<string, { next: string | null; items: InboxItem[] }>) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const method = (init.method ?? 'GET').toUpperCase();
      calls.push({ url, method, headers: new Headers(init.headers) });
      const unread = items.filter((i) => !i.read).length;
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/practice') return Response.json({ items: [] });
      if (url === '/v1/catalog') return Response.json({ catalog_version: 'v1', paths: [], roles: [], path_roles: [], items: [], marks: [] });
      if (method === 'POST' && url === '/v1/inbox/read-all') {
        items = items.map((i) => ({ ...i, read: true }));
        return new Response(null, { status: 204 });
      }
      const one = /^\/v1\/inbox\/([^/]+)\/read$/.exec(url);
      if (method === 'POST' && one) {
        items = items.map((i) => (i.id === one[1] ? { ...i, read: true } : i));
        return new Response(null, { status: 204 });
      }
      if (url === '/v1/inbox?limit=1') return Response.json({ unread, next: null, items: items.slice(0, 1) });
      if (url.startsWith('/v1/inbox?limit=20')) {
        const before = new URL(url, 'http://x').searchParams.get('before');
        const page = pages?.[before ?? ''];
        if (page) return Response.json({ unread, ...page });
        return Response.json({ unread, next: null, items });
      }
      return Response.json({ error: 'x' }, { status: 404 });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 5; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(path: string) {
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

const bell = () => container.querySelector<HTMLButtonElement>('header button[aria-haspopup="dialog"]')!;
const dialog = () => container.querySelector<HTMLElement>('[role="dialog"][aria-label="Notifications"]');
const click = async (el: Element) => {
  await act(async () => (el as HTMLElement).click());
  await settle();
};
const key = async (el: Element, k: string, init: KeyboardEventInit = {}) => {
  await act(async () => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));
  });
};
const posts = () => calls.filter((c) => c.method === 'POST').map((c) => c.url);

beforeEach(() => {
  resetReadBlock();
  vi.stubGlobal('EventSource', undefined);
  items = [
    row({ id: 'i1', title: 'Ticket passed · perms-02', body: 'Your fix survived the reboot.' }),
    row({ id: 'i2', kind: 'quiz', title: 'Quiz ready: users and logs', body: '8 questions, about 5 minutes', url: '/quiz/support-l1/quiz-01', read: true, at: iso(3) }),
  ];
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('inbox helpers', () => {
  it('follows only paths on this app', () => {
    expect(safeInboxUrl('/t/support-l1/perms-02')).toBe('/t/support-l1/perms-02');
    for (const bad of ['//evil.example', 'https://evil.example', '/\\evil.example', '/a\\b', 'javascript:alert(1)', 'evil', '', '/a\nb']) {
      expect(safeInboxUrl(bad)).toBeNull();
    }
  });

  it('groups today first and labels times', () => {
    const now = new Date('2026-12-02T12:00:00');
    const a = row({ id: 'a', title: 'a', at: '2026-12-02T10:42:00' });
    const b = row({ id: 'b', title: 'b', at: '2026-11-30T10:00:00' });
    expect(groupInbox([a, b], now).map((g) => [g.label, g.items.length])).toEqual([['Today', 1], ['Earlier', 1]]);
    expect(formatInboxTime(a.at, now)).toBe('10:42');
    expect(formatInboxTime(b.at, now)).toBe('Mon');
    expect(formatInboxTime('2026-10-01T10:00:00', now)).toBe('Oct 1');
  });
});

describe('bell dropdown', () => {
  it('shows an unread count in the bell label and the phone tab, never color alone', async () => {
    stub();
    await mount('/practice');
    expect(bell().getAttribute('aria-label')).toBe('Notifications, 1 unread');
    expect(container.querySelector('nav[aria-label="Primary, tabs"] a[href="/inbox"]')?.textContent).toContain('Inbox');
    expect(container.querySelector('nav[aria-label="Primary, tabs"] a[href="/inbox"] .sr-only')?.textContent).toBe(', 1 unread');
  });

  it('has a plain label when nothing is unread', async () => {
    items = items.map((i) => ({ ...i, read: true }));
    stub();
    await mount('/practice');
    expect(bell().getAttribute('aria-label')).toBe('Notifications');
    expect(container.querySelector('nav[aria-label="Primary, tabs"] a[href="/inbox"] .sr-only')).toBeNull();
  });

  it('opens on click, focuses inside, closes on Escape and returns focus to the bell', async () => {
    stub();
    await mount('/practice');
    expect(dialog()).toBeNull();
    expect(bell().getAttribute('aria-expanded')).toBe('false');
    await click(bell());
    expect(dialog()).not.toBeNull();
    expect(bell().getAttribute('aria-expanded')).toBe('true');
    expect(dialog()!.contains(document.activeElement)).toBe(true);
    expect(dialog()!.textContent).toContain('Ticket passed · perms-02');
    expect(dialog()!.textContent).toContain('Unread');
    await key(document.activeElement!, 'Escape');
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(bell());
  });

  it('closes on a click outside and on the bell again', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    await act(async () => {
      document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(dialog()).toBeNull();
    await click(bell());
    await click(bell());
    expect(dialog()).toBeNull();
  });

  it('traps Tab inside the dialog', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    const nodes = [...dialog()!.querySelectorAll<HTMLElement>('a[href], button:not([disabled])')];
    const last = nodes[nodes.length - 1]!;
    last.focus();
    await key(last, 'Tab');
    expect(document.activeElement).toBe(nodes[0]);
    await key(nodes[0]!, 'Tab', { shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it('opens an item: navigates to its url, marks it read and closes', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    const link = dialog()!.querySelector<HTMLAnchorElement>('a[href="/t/support-l1/perms-02"]')!;
    await click(link);
    expect(router.state.location.pathname).toBe('/t/support-l1/perms-02');
    expect(posts()).toEqual(['/v1/inbox/i1/read']);
    const post = calls.find((c) => c.method === 'POST')!;
    expect(post.headers.get('X-Dojo-Csrf')).toBeTruthy();
    expect(dialog()).toBeNull();
    expect(bell().getAttribute('aria-label')).toBe('Notifications');
  });

  it('does not mark a read item again', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    await click(dialog()!.querySelector('a[href="/quiz/support-l1/quiz-01"]')!);
    expect(router.state.location.pathname).toBe('/quiz/support-l1/quiz-01');
    expect(posts()).toEqual([]);
  });

  it('marks everything read', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    const all = [...dialog()!.querySelectorAll('button')].find((b) => b.textContent === 'Mark all read')!;
    await click(all);
    expect(posts()).toEqual(['/v1/inbox/read-all']);
    expect(bell().getAttribute('aria-label')).toBe('Notifications');
    expect(dialog()!.textContent).not.toContain('Unread');
  });

  it('pages with the next cursor', async () => {
    stub({
      '': { next: 'CURSOR1', items },
      CURSOR1: { next: null, items: [row({ id: 'i3', title: 'Older item', read: true, at: iso(20) })] },
    });
    await mount('/practice');
    await click(bell());
    expect(dialog()!.textContent).not.toContain('Older item');
    const more = [...dialog()!.querySelectorAll('button')].find((b) => b.textContent === 'Show older')!;
    await click(more);
    expect(calls.map((c) => c.url)).toContain('/v1/inbox?limit=20&before=CURSOR1');
    expect(dialog()!.textContent).toContain('Older item');
    expect([...dialog()!.querySelectorAll('button')].some((b) => b.textContent === 'Show older')).toBe(false);
  });

  describe('after "Show older"', () => {
    const three = () =>
      stub({
        '': { next: 'C1', items },
        C1: { next: 'C2', items: [row({ id: 'p2a', title: 'Page two first', read: true, at: iso(10) }), row({ id: 'p2b', title: 'Page two second', read: true, at: iso(11) })] },
        C2: { next: null, items: [row({ id: 'p3a', title: 'Page three first', read: true, at: iso(20) })] },
      });
    const older = () => [...dialog()!.querySelectorAll('button')].find((b) => b.textContent === 'Show older')!;

    it('moves focus to the first new row, and Escape and Tab still work while more pages remain', async () => {
      three();
      await mount('/practice');
      await click(bell());
      older().focus();
      await click(older());
      expect(document.activeElement?.textContent).toContain('Page two first');
      expect(older()).toBeDefined();
      const nodes = [...dialog()!.querySelectorAll<HTMLElement>('a[href], button:not([disabled])')];
      const last = nodes[nodes.length - 1]!;
      last.focus();
      await key(last, 'Tab');
      expect(document.activeElement).toBe(nodes[0]);
      await key(document.activeElement!, 'Escape');
      expect(dialog()).toBeNull();
      expect(document.activeElement).toBe(bell());
    });

    it('keeps focus in the dialog after the last page and Escape still closes', async () => {
      three();
      await mount('/practice');
      await click(bell());
      await click(older());
      await click(older());
      expect([...dialog()!.querySelectorAll('button')].some((b) => b.textContent === 'Show older')).toBe(false);
      expect(document.activeElement?.textContent).toContain('Page three first');
      await key(document.activeElement!, 'Escape');
      expect(dialog()).toBeNull();
      expect(document.activeElement).toBe(bell());
    });

    it('still traps and closes when focus has fallen to the body', async () => {
      three();
      await mount('/practice');
      await click(bell());
      (document.activeElement as HTMLElement).blur();
      expect(document.activeElement).toBe(document.body);
      const nodes = [...dialog()!.querySelectorAll<HTMLElement>('a[href], button:not([disabled])')];
      await key(document.body, 'Tab');
      expect(document.activeElement).toBe(nodes[0]);
      (document.activeElement as HTMLElement).blur();
      await key(document.body, 'Tab', { shiftKey: true });
      expect(document.activeElement).toBe(nodes[nodes.length - 1]);
      (document.activeElement as HTMLElement).blur();
      await key(document.body, 'Escape');
      expect(dialog()).toBeNull();
      expect(document.activeElement).toBe(bell());
    });

    it('wraps Shift+Tab from the dialog container to the last control', async () => {
      stub();
      await mount('/practice');
      await click(bell());
      dialog()!.focus();
      expect(document.activeElement).toBe(dialog());
      const nodes = [...dialog()!.querySelectorAll<HTMLElement>('a[href], button:not([disabled])')];
      await key(dialog()!, 'Tab', { shiftKey: true });
      expect(document.activeElement).toBe(nodes[nodes.length - 1]);
    });

    it('ignores a second click while a page is loading', async () => {
      three();
      await mount('/practice');
      await click(bell());
      const btn = older();
      await act(async () => {
        btn.click();
        btn.click();
      });
      await settle();
      expect(calls.filter((c) => c.url.includes('before=C1'))).toHaveLength(1);
    });
  });

  it('says "Nothing new" when the inbox is empty', async () => {
    items = [];
    stub();
    await mount('/practice');
    await click(bell());
    expect(dialog()!.textContent).toContain('Nothing new');
  });

  it('never follows an unsafe url, and still marks the row read', async () => {
    items = [row({ id: 'bad', title: 'Odd link', url: '//evil.example/x' })];
    stub();
    await mount('/practice');
    await click(bell());
    expect(dialog()!.querySelector('a[href*="evil"]')).toBeNull();
    const rowButton = [...dialog()!.querySelectorAll('button')].find((b) => b.textContent?.includes('Odd link'))!;
    await click(rowButton);
    expect(router.state.location.pathname).toBe('/practice');
    expect(posts()).toEqual(['/v1/inbox/bad/read']);
  });

  it('renders title and body as text, not markup', async () => {
    items = [row({ id: 'x', title: '<img src=x onerror=alert(1)>', body: '<b>bold</b>' })];
    stub();
    await mount('/practice');
    await click(bell());
    expect(dialog()!.querySelector('img')).toBeNull();
    expect(dialog()!.querySelector('b')).toBeNull();
    expect(dialog()!.textContent).toContain('<b>bold</b>');
  });

  it('links to the full inbox and the notification settings', async () => {
    stub();
    await mount('/practice');
    await click(bell());
    expect(dialog()!.querySelector('a[href="/settings"]')?.textContent).toBe('Notification settings');
    await click(dialog()!.querySelector('a[href="/inbox"]')!);
    expect(router.state.location.pathname).toBe('/inbox');
  });
});

describe('inbox page (phone)', () => {
  it('renders the Inbox heading, groups, rows and the active tab', async () => {
    stub();
    await mount('/inbox');
    expect(container.querySelector('h1')?.textContent).toBe('Inbox');
    expect(container.querySelector('[role="group"][aria-label="Today"]')?.textContent).toContain('Ticket passed · perms-02');
    expect(container.querySelector('[role="group"][aria-label="Earlier"]')?.textContent).toContain('Quiz ready: users and logs');
    expect(container.querySelector('nav[aria-label="Primary, tabs"] a[href="/inbox"]')?.getAttribute('aria-current')).toBe('page');
  });

  it('shows the empty state', async () => {
    items = [];
    stub();
    await mount('/inbox');
    expect(container.textContent).toContain('Nothing new');
  });

  it('marks all read and opens an item', async () => {
    stub();
    await mount('/inbox');
    await click([...container.querySelectorAll('main button')].find((b) => b.textContent === 'Mark all read')!);
    expect(posts()).toEqual(['/v1/inbox/read-all']);
    await click(container.querySelector('main a[href="/quiz/support-l1/quiz-01"]')!);
    expect(router.state.location.pathname).toBe('/quiz/support-l1/quiz-01');
  });

  it('shows a retry when the list cannot load', async () => {
    stub();
    vi.stubGlobal('fetch', vi.fn(async (url: string) => (url === '/v1/me' ? Response.json(me) : Response.json({ error: 'x' }, { status: 500 }))));
    await mount('/inbox');
    // A transient failure is retried once, after the default 1 s.
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not load this');
  });
});
