// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { itemKey, resetReadBlock, type StoryItem, type WritingWork } from '../lib/read';
import { PROGRESS_DEBOUNCE_MS } from '../lib/stream';
import { AUTOSAVE_IDLE_MS } from '../components/ReplyEditor';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const ID = 'support-l1/feedback-01';
const OTHER = 'support-l1/write-01';

const work = (over: Partial<WritingWork> = {}): WritingWork => ({
  draft: null,
  draft_saved_at: null,
  body: null,
  submitted_at: null,
  submit_count: 0,
  review_state: null,
  reviewed_at: null,
  review: null,
  ...over,
});

const feedback = (over: Partial<StoryItem> = {}): StoryItem => ({
  id: ID,
  kind: 'feedback',
  role: 'support-l1',
  title: 'Carmen reads your ticket',
  est_minutes: 8,
  tags: [],
  done: false,
  from: { name: 'Carmen', role: 'Mentor' },
  scene: 'Your reply to Elena was clear. <b>One thing</b>: you skipped the cause.',
  rubric: [
    { id: 'listen', text: 'You say what you heard' },
    { id: 'next', text: 'You say what you will change' },
  ],
  awards_mark: { slug: 'feedback-round', title: 'Feedback round with Carmen', held: false },
  work: null,
  ...over,
});

const REVIEW = {
  criteria: [
    { id: 'listen', text: 'You say what you heard', met: true, evidence: 'I skipped the cause', note: '' },
    { id: 'next', text: 'You say what you will change', met: false, evidence: '', note: 'Say one concrete change.' },
  ],
  next_step: 'Add the change you will make next time.',
};

type Listener = (e: MessageEvent<string>) => void;
const sources: { emit: (name: string, data?: string) => void }[] = [];
class FakeES {
  readyState = 1;
  onerror = null;
  private listeners = new Map<string, Listener[]>();
  constructor() {
    sources.push(this);
  }
  addEventListener(name: string, fn: Listener) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn]);
  }
  close() {}
  emit(name: string, data = '{}') {
    for (const fn of this.listeners.get(name) ?? []) fn({ data } as MessageEvent<string>);
  }
}

let container: HTMLDivElement;
let root: Root;
let client: QueryClient;
let current: StoryItem;
let puts: string[];
let posts: string[];
let putReply: () => Response;
let submitReply: () => Response;
let itemGets: number;

function stubApi() {
  puts = [];
  posts = [];
  itemGets = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/path') return new Response(null, { status: 404 });
      if (init?.method === 'PUT') {
        puts.push(String(init.body));
        return putReply();
      }
      if (init?.method === 'POST') {
        posts.push(String(init.body));
        return submitReply();
      }
      if (url === `/v1/items/${ID}`) {
        itemGets++;
        return Response.json(current);
      }
      return new Response(null, { status: 404 });
    }),
  );
}

const settle = async () => {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
};

async function mount(item: StoryItem) {
  current = item;
  stubApi();
  const router = createMemoryRouter(routes, { initialEntries: [`/story/${ID}`] });
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
}

const text = () => container.textContent ?? '';
const area = () => container.querySelector('textarea')!;
const button = (label: string) => [...container.querySelectorAll('button')].find((b) => b.textContent === label)!;
async function type(value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(area(), value);
    area().dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const click = async (el: Element) => {
  await act(async () => el.dispatchEvent(new MouseEvent('click', { bubbles: true })));
  await settle();
};

beforeEach(() => {
  resetReadBlock();
  sources.length = 0;
  vi.stubGlobal('EventSource', FakeES);
  putReply = () => Response.json({ draft_saved_at: '2026-11-28T10:42:00Z' });
  submitReply = () =>
    Response.json({ item: feedback({ done: true, work: work({ body: 'x', submit_count: 1, review_state: 'pending' }) }), awarded: [] });
  client = new QueryClient();
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('the message', () => {
  it('shows who sent it, the message as text and the reply box, with no story choices', async () => {
    await mount(feedback());
    expect(text()).toContain('Carmen · Mentor');
    expect(container.querySelector('section[aria-label="The message"]')?.textContent).toContain('<b>One thing</b>');
    expect(container.querySelector('section[aria-label="The message"] b')).toBeNull();
    expect(area().getAttribute('aria-labelledby')).toBe('reply-title');
    expect(text()).toContain('Carmen reads your reply against these points.');
    expect(text()).not.toContain('How do you reply?');
    expect(text()).not.toContain('coming soon');
  });

  it('names the sender as the reviewer', async () => {
    await mount({ ...feedback(), from: { name: 'Lucia', role: 'Team lead' } });
    expect(text()).toContain('Lucia reads your reply against these points.');
    expect(text()).not.toContain('Carmen reads your reply');
  });

  it('restores the saved draft', async () => {
    await mount(feedback({ work: work({ draft: 'Thanks, I missed', draft_saved_at: '2026-11-28T10:42:00Z' }) }));
    expect(area().value).toBe('Thanks, I missed');
    expect(text()).toMatch(/Saved \d\d:\d\d/);
  });
});

describe('autosave', () => {
  it('saves 2 s after typing stops', async () => {
    await mount(feedback());
    vi.useFakeTimers();
    await type('Thanks');
    expect(puts).toHaveLength(0);
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(puts).toEqual([JSON.stringify({ body: 'Thanks' })]);
  });

  it('keeps a newer cached draft when an older save answers late', async () => {
    await mount(feedback({ work: work({ draft: 'newer', draft_saved_at: '2026-11-28T10:50:00Z' }) }));
    vi.useFakeTimers();
    await type('newer text');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(puts).toHaveLength(1);
    const cached = client.getQueryData<StoryItem>(itemKey(ID))?.work;
    expect(cached?.draft).toBe('newer');
    expect(cached?.draft_saved_at).toBe('2026-11-28T10:50:00Z');
  });
});

describe('submit', () => {
  it('sends the reply, shows the pending state and names the new belt mark', async () => {
    submitReply = () =>
      Response.json({
        item: feedback({ done: true, work: work({ body: 'Thanks', submit_count: 1, review_state: 'pending' }) }),
        awarded: [{ kind: 'mark', inbox_id: 'inbox-1', slug: 'feedback-round' }],
      });
    await mount(feedback());
    expect(button('Ask for review').disabled).toBe(true);
    await type('Thanks');
    await click(button('Ask for review'));
    expect(posts).toEqual([JSON.stringify({ body: 'Thanks' })]);
    expect(text()).toContain('Carmen is reading it');
    expect(text()).toContain('You earned a belt mark: Feedback round with Carmen.');
    expect(container.querySelector('a[href="/celebrate/inbox-1"]')?.textContent).toBe('See your mark');
    expect(client.getQueryData<StoryItem>(itemKey(ID))?.work?.review_state).toBe('pending');
  });

  it('keeps the text and says so when the send fails, without echoing it', async () => {
    submitReply = () => new Response(null, { status: 500 });
    await mount(feedback());
    await type('secret words');
    await click(button('Ask for review'));
    const alert = container.querySelector('[role="alert"]')?.textContent ?? '';
    expect(alert).toContain('Could not send your work. Your text is still here.');
    expect(alert).not.toContain('secret');
    expect(area().value).toBe('secret words');
  });
});

describe('review states', () => {
  const withState = (review_state: WritingWork['review_state'], review = null as WritingWork['review']) =>
    feedback({ work: work({ body: 'Hi', submit_count: 1, review_state, review }) });

  it('pending', async () => {
    await mount(withState('pending'));
    expect(text()).toContain('Carmen is reading it');
  });

  it('paused', async () => {
    await mount(withState('paused'));
    expect(text()).toContain('Reviews are paused, your work is saved');
  });

  it('capped', async () => {
    await mount(withState('capped'));
    expect(text()).toContain('No more reviews today. Your work is saved; ask for a review again tomorrow.');
  });

  it('failed', async () => {
    await mount(withState('failed'));
    expect(text()).toContain('The review could not run. Your work is saved; ask again.');
  });

  it('done: lists the rubric with met and not yet, and the next step', async () => {
    await mount(withState('done', REVIEW));
    const items = [...container.querySelectorAll('section[aria-label="Review"] li')];
    expect(items[0]?.textContent).toContain('Passed');
    expect(items[0]?.textContent).toContain('“I skipped the cause”');
    expect(items[1]?.textContent).toContain('Not yet');
    expect(text()).toContain('Add the change you will make next time.');
    expect(text()).not.toContain('Carmen is reading it');
  });
});

describe('live review', () => {
  it('refetches this item on its review event and ignores the review of another item', async () => {
    await mount(feedback({ work: work({ body: 'Hi', submit_count: 1, review_state: 'pending' }) }));
    expect(sources).toHaveLength(1);
    const before = itemGets;
    sources[0]!.emit('review', JSON.stringify({ item_id: OTHER, review_state: 'done' }));
    await act(async () => new Promise((r) => setTimeout(r, PROGRESS_DEBOUNCE_MS + 100)));
    expect(itemGets).toBe(before);
    current = feedback({ done: true, work: work({ body: 'Hi', submit_count: 1, review_state: 'done', review: REVIEW }) });
    sources[0]!.emit('review', JSON.stringify({ item_id: ID, review_state: 'done' }));
    await act(async () => new Promise((r) => setTimeout(r, PROGRESS_DEBOUNCE_MS + 100)));
    await settle();
    expect(itemGets).toBe(before + 1);
    expect(text()).toContain('Add the change you will make next time.');
    expect(text()).not.toContain('Carmen is reading it');
  });
});
