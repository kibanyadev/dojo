// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { readBlockedUntil, resetReadBlock, type StoryChosen, type StoryItem } from '../lib/read';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const ID = 'support-l1/story-03';
const CHOSEN: StoryChosen = { id: 'own', verdict: 'good', reaction: 'You own the mistake and say who you will tell.', chosen_at: '2026-11-28T10:00:00Z' };

const story = (over: Partial<StoryItem> = {}): StoryItem => ({
  id: ID,
  kind: 'story',
  role: 'support-l1',
  title: 'Elena writes again',
  est_minutes: 3,
  tags: [],
  done: false,
  from: { name: 'Elena', role: 'Finance' },
  scene: 'Sorry!! Me again. <b>What do I tell her?</b>',
  choices: [
    { id: 'quick', text: 'Tell Lucía it was a quick fix and it works.' },
    { id: 'own', text: "I'll fix the permissions and tell Lucía what changed." },
    { id: 'pass', text: 'Ask Lucía to fix it.' },
  ],
  chosen: null,
  ...over,
});

const path = {
  path: 'support',
  chosen: true,
  roles: [
    {
      slug: 'support-l1',
      title: 'Support L1',
      summary: '',
      locked: false,
      passed: false,
      tickets_passed: 1,
      tickets_total: 1,
      items: [{ id: 'support-l1/welcome', title: 'The welcome ticket', order: 1, state: 'passed', lab_state: null }],
      between_items: [{ id: ID, kind: 'story', title: 'Elena writes again', order: 2, done: false }],
    },
  ],
};

let container: HTMLDivElement;
let root: Root;
let current: StoryItem | number;
let choiceReply: () => Response;
let posts: { url: string; body: string }[];
let itemGets: number;

function stubApi(withPath: boolean) {
  posts = [];
  itemGets = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/path') return withPath ? Response.json(path) : new Response(null, { status: 404 });
      if (init?.method === 'POST') {
        posts.push({ url, body: String(init.body) });
        return choiceReply();
      }
      if (url === `/v1/items/${ID}`) {
        itemGets++;
        return typeof current === 'number' ? new Response(null, { status: current }) : Response.json(current);
      }
      return new Response(null, { status: 404 });
    }),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(item: StoryItem | number, withPath = false, at = `/story/${ID}`) {
  current = item;
  stubApi(withPath);
  const router = createMemoryRouter(routes, { initialEntries: [at] });
  await act(async () => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
}

const live = () => [...container.querySelectorAll('main [role="status"][aria-live="polite"]')].at(-1) ?? null;
const text = () => container.textContent ?? '';
const reply = (start: string) => [...container.querySelectorAll('button')].find((b) => b.textContent?.startsWith(start))!;
const click = async (el: Element) => {
  await act(async () => el.dispatchEvent(new MouseEvent('click', { bubbles: true })));
  await settle();
};

beforeEach(() => {
  resetReadBlock();
  choiceReply = () => Response.json({ chosen: CHOSEN });
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('Story not chosen yet', () => {
  beforeEach(() => mount(story(), true));

  it('shows one h1, the sender, the scene as text and every reply', () => {
    expect(container.querySelectorAll('h1')).toHaveLength(1);
    expect(container.querySelector('h1')?.textContent).toBe('Elena writes again');
    expect(text()).toContain('Elena · Finance');
    expect(text()).toContain('<b>What do I tell her?</b>');
    expect(container.querySelector('section[aria-label="The message"] b')).toBeNull();
    expect(text()).toContain('How do you reply?');
    expect(container.querySelectorAll('[role="group"] button')).toHaveLength(3);
    expect(text()).toContain('Your reply is final once you choose it.');
    expect(text()).not.toContain('Tone review');
  });

  it('keeps an empty polite live region ready for the verdict', () => {
    const region = live();
    expect(region).not.toBeNull();
    expect(region?.textContent).toBe('');
  });

  it('shows the role progress and the moment count from the path', () => {
    expect(text()).toContain('Support L1 · moment 1 of 1');
    expect(text()).toContain('1 · The welcome ticket');
    expect(container.querySelector('li[aria-current="step"]')?.textContent).toContain('2 · Elena writes again');
  });

  it('posts the chosen id, then shows the verdict and reaction in the live region', async () => {
    await click(reply("I'll fix"));
    expect(posts).toEqual([{ url: `/v1/items/${ID}/choice`, body: JSON.stringify({ choice: 'own' }) }]);
    const region = live()!;
    expect(region.textContent).toContain('good reply');
    expect(region.textContent).toContain('You own the mistake');
    expect(text()).not.toContain('+1 trust');
    const mine = reply("I'll fix");
    expect(mine.getAttribute('aria-pressed')).toBe('true');
    expect(mine.textContent).toContain('Your reply');
    expect(text()).not.toContain('Your reply is final once');
  });

  it('ignores a second click once a reply is chosen', async () => {
    await click(reply("I'll fix"));
    await click(reply('Ask Lucía'));
    expect(posts).toHaveLength(1);
  });

  it('sends one POST when a second reply is clicked while the first is pending', async () => {
    let release: (r: Response) => void = () => {};
    const gate = new Promise<Response>((resolve) => (release = resolve));
    const inner = (globalThis.fetch as unknown as (u: string, i?: RequestInit) => Promise<Response>).bind(globalThis);
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => (init?.method === 'POST' ? (posts.push({ url, body: String(init.body) }), gate) : inner(url, init)));
    await act(async () => reply("I'll fix").dispatchEvent(new MouseEvent('click', { bubbles: true })));
    await act(async () => reply('Ask Lucía').dispatchEvent(new MouseEvent('click', { bubbles: true })));
    expect(posts).toHaveLength(1);
    await act(async () => release(Response.json({ chosen: CHOSEN })));
    await settle();
    expect(posts).toHaveLength(1);
    expect(live()!.textContent).toContain('good reply');
  });

  it('is operable from the keyboard: replies are buttons', () => {
    for (const b of container.querySelectorAll('[role="group"] button')) expect(b.getAttribute('type')).toBe('button');
  });
});

describe('Story chosen on load', () => {
  it('shows the stored reply and verdict without a prompt to choose', async () => {
    await mount(story({ done: true, chosen: { ...CHOSEN, id: 'quick', verdict: 'poor', reaction: 'Lucía is still waiting.' } }));
    expect(reply('Tell Lucía').getAttribute('aria-pressed')).toBe('true');
    expect(text()).toContain('poor reply');
    expect(text()).toContain('Lucía is still waiting.');
    expect(text()).not.toContain('Your reply is final once');
    await click(reply('Ask Lucía'));
    expect(posts).toHaveLength(0);
  });
});

describe('Story errors', () => {
  it('refetches the stored choice after a 409', async () => {
    await mount(story());
    choiceReply = () => {
      current = story({ done: true, chosen: { ...CHOSEN, id: 'pass', verdict: 'okay', reaction: 'It works out, slowly.' } });
      return Response.json({ error: 'already_chosen' }, { status: 409 });
    };
    const before = itemGets;
    await click(reply("I'll fix"));
    expect(itemGets).toBeGreaterThan(before);
    expect(text()).toContain('okay reply');
    expect(text()).toContain('It works out, slowly.');
    expect(reply('Ask Lucía').getAttribute('aria-pressed')).toBe('true');
    expect(reply("I'll fix").getAttribute('aria-pressed')).toBe('false');
  });

  it('keeps the replies locked after a 409 until the refetch settles', async () => {
    await mount(story());
    let release: (r: Response) => void = () => {};
    const slow = new Promise<Response>((resolve) => (release = resolve));
    const inner = (globalThis.fetch as unknown as (u: string, i?: RequestInit) => Promise<Response>).bind(globalThis);
    let gets = 0;
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        posts.push({ url, body: String(init.body) });
        return Promise.resolve(Response.json({ error: 'already_chosen' }, { status: 409 }));
      }
      if (url === `/v1/items/${ID}` && ++gets === 1) return slow;
      return inner(url, init);
    });
    await act(async () => reply("I'll fix").dispatchEvent(new MouseEvent('click', { bubbles: true })));
    await settle();
    expect(gets).toBe(1);
    expect(reply('Ask Lucía').getAttribute('aria-disabled')).toBe('true');
    await act(async () => reply('Ask Lucía').dispatchEvent(new MouseEvent('click', { bubbles: true })));
    expect(posts).toHaveLength(1);
    current = story({ done: true, chosen: { ...CHOSEN, id: 'pass', verdict: 'okay', reaction: 'It works out, slowly.' } });
    await act(async () => release(Response.json(current)));
    await settle();
    expect(reply('Ask Lucía').getAttribute('aria-pressed')).toBe('true');
    expect(text()).toContain('It works out, slowly.');
  });

  it('says a reply is gone on 404 and refetches', async () => {
    await mount(story());
    choiceReply = () => Response.json({ error: 'not_found' }, { status: 404 });
    const before = itemGets;
    await click(reply("I'll fix"));
    expect(text()).toContain('That reply is no longer available.');
    expect(itemGets).toBeGreaterThan(before);
  });

  it('keeps the choices open after a failed send', async () => {
    await mount(story());
    choiceReply = () => Response.json({ error: 'server_error' }, { status: 500 });
    await click(reply("I'll fix"));
    expect(text()).toContain('Could not send your reply. Please try again.');
    expect(reply("I'll fix").getAttribute('aria-pressed')).toBe('false');
    choiceReply = () => Response.json({ chosen: CHOSEN });
    await click(reply("I'll fix"));
    expect(text()).toContain('good reply');
  });

  it('waits out a 429 with Retry-After', async () => {
    await mount(story());
    choiceReply = () => new Response(null, { status: 429, headers: { 'Retry-After': '1' } });
    await click(reply("I'll fix"));
    expect(text()).toContain('Too many requests');
    expect(readBlockedUntil()).toBeGreaterThan(Date.now());
    expect(reply("I'll fix").getAttribute('aria-disabled')).toBe('true');
  });

  it('says not found for a missing moment, with a way back', async () => {
    await mount(404);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('We could not find that moment.');
    expect(container.querySelector('a[href="/path"]')).not.toBeNull();
  });

  it('offers a retry when the read fails', async () => {
    await mount(500);
    await act(async () => new Promise((r) => setTimeout(r, 1100)));
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not load this.');
    current = story();
    await click([...container.querySelectorAll('button')].find((b) => b.textContent === 'Retry')!);
    expect(text()).toContain('How do you reply?');
  });

  it('treats a non-story item as not found', async () => {
    await mount({ ...story(), kind: 'quiz' } as unknown as StoryItem);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('We could not find that moment.');
  });

  it('says not found for a malformed id', async () => {
    await mount(story(), false, '/story/support-l1');
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('We could not find that moment.');
  });
});
