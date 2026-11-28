// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { itemKey, readBlockedUntil, resetReadBlock, type WritingItem, type WritingWork } from '../lib/read';
import { AUTOSAVE_IDLE_MS, AUTOSAVE_MIN_GAP_MS } from './Writing';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const ID = 'support-l1/write-01';

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

const writing = (over: Partial<WritingItem> = {}): WritingItem => ({
  id: ID,
  kind: 'writing',
  role: 'support-l1',
  title: 'Tell Elena what happened',
  est_minutes: 10,
  tags: ['writing'],
  done: false,
  prompt: 'Elena asked why the shared folder broke. <b>Reply to her.</b>',
  rubric: [
    { id: 'found', text: 'What you found' },
    { id: 'next', text: 'What she does now' },
  ],
  awards_mark: { slug: 'written-english', title: 'Written English for tickets', held: false },
  work: null,
  ...over,
});

const REVIEW = {
  criteria: [
    { id: 'found', text: 'What you found', met: true, evidence: 'The team group now owns /srv/shared', note: '' },
    { id: 'next', text: 'What she does now', met: false, evidence: '', note: 'Nothing tells her she can try again.' },
  ],
  next_step: 'Add one line: what Elena should do now.',
};

let container: HTMLDivElement;
let root: Root;
let client: QueryClient;
let current: WritingItem | number;
let puts: { url: string; body: string }[];
let posts: { url: string; body: string }[];
let putReply: () => Response;
let submitReply: () => Response;
let itemGets: number;
let submitGate: Promise<void> | null = null;
let router: ReturnType<typeof createMemoryRouter>;

function stubApi() {
  puts = [];
  posts = [];
  itemGets = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/v1/me') return Response.json(me);
      if (init?.method === 'PUT') {
        puts.push({ url, body: String(init.body) });
        return putReply();
      }
      if (init?.method === 'POST') {
        posts.push({ url, body: String(init.body) });
        if (submitGate) await submitGate;
        return submitReply();
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

async function mount(item: WritingItem | number, at = `/w/${ID}`) {
  current = item;
  stubApi();
  router = createMemoryRouter(routes, { initialEntries: [at] });
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
const click = async (el: Element) => {
  await act(async () => el.dispatchEvent(new MouseEvent('click', { bubbles: true })));
  if (vi.isFakeTimers()) await act(async () => vi.advanceTimersByTimeAsync(0));
  else await settle();
};
async function type(value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    setter.call(area(), value);
    area().dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => {
  resetReadBlock();
  submitGate = null;
  putReply = () => Response.json({ draft_saved_at: '2026-11-28T10:42:00Z' });
  submitReply = () => Response.json({ item: writing({ done: true, work: work({ body: 'x', submit_count: 1, review_state: 'pending' }) }), awarded: [] });
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

describe('loading', () => {
  it('shows the task, an empty labelled textarea and no draft status when there is no work', async () => {
    await mount(writing());
    expect(container.querySelectorAll('h1')).toHaveLength(1);
    expect(container.querySelector('h1')?.textContent).toBe('Tell Elena what happened');
    expect(text()).toContain('<b>Reply to her.</b>');
    expect(container.querySelector('section[aria-label="The task"] b')).toBeNull();
    expect(area().value).toBe('');
    expect(area().getAttribute('aria-labelledby')).toBe('reply-title');
    expect(text()).toContain('0 of 4,000 characters');
    expect(text()).not.toContain('Saved');
    expect(text()).toContain('Ask for a review when you are ready.');
    expect(text()).toContain('What she does now');
    expect(button('Ask for review').disabled).toBe(true);
  });

  it('restores the saved draft and says when it was saved', async () => {
    await mount(writing({ work: work({ draft: 'Hi Elena, almost', draft_saved_at: '2026-11-28T10:42:00Z' }) }));
    expect(area().value).toBe('Hi Elena, almost');
    expect(text()).toMatch(/Saved \d\d:\d\d/);
    expect(text()).toContain('16 of 4,000 characters');
    expect(button('Ask for review').disabled).toBe(false);
  });

  it('falls back to the submitted body when there is no draft', async () => {
    await mount(writing({ work: work({ body: 'Sent text', submit_count: 1, review_state: 'pending' }) }));
    expect(area().value).toBe('Sent text');
  });

  it('shows the not found message for an unknown task and for another kind', async () => {
    await mount(404);
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('We could not find that task.');
    expect(text()).toContain('Back to the path');
    act(() => root.unmount());
    root = createRoot(container);
    await mount({ id: ID, kind: 'quiz' } as unknown as WritingItem);
    expect(text()).toContain('We could not find that task.');
  });

  it('offers a retry on a failed read', async () => {
    await mount(500);
    await act(async () => new Promise((r) => setTimeout(r, 1200)));
    expect(text()).toContain('Could not load this. Please try again.');
    current = writing();
    await click(button('Retry'));
    expect(container.querySelector('h1')?.textContent).toBe('Tell Elena what happened');
  });

  it('refuses a malformed id without a request for the item', async () => {
    await mount(writing(), '/w/onlyone');
    expect(text()).toContain('We could not find that task.');
    expect(itemGets).toBe(0);
  });
});

describe('autosave', () => {
  beforeEach(async () => {
    await mount(writing());
    vi.useFakeTimers();
  });

  it('saves 2 s after typing stops, not on every keystroke', async () => {
    await type('H');
    await type('Hi');
    await type('Hi Elena');
    expect(puts).toHaveLength(0);
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS - 1));
    expect(puts).toHaveLength(0);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(puts).toHaveLength(1);
    expect(puts[0]).toEqual({ url: `/v1/items/${ID}/draft`, body: JSON.stringify({ body: 'Hi Elena' }) });
    expect(text()).toMatch(/Saved \d\d:\d\d/);
  });

  it('keeps the status unchanged while typing after a save', async () => {
    await type('Hi');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    const before = container.querySelector('[role="status"][aria-live="polite"]:not(:empty)')?.textContent;
    await type('Hi Elena');
    expect(container.querySelector('[role="status"][aria-live="polite"]:not(:empty)')?.textContent).toBe(before);
  });

  it('saves at most every 10 s while the learner keeps typing', async () => {
    await type('a');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(puts).toHaveLength(1);
    await type('ab');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(puts).toHaveLength(1);
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS - AUTOSAVE_IDLE_MS));
    expect(puts).toHaveLength(2);
    expect(puts[1]?.body).toBe(JSON.stringify({ body: 'ab' }));
  });

  it('does not save unchanged text', async () => {
    await type('a');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    await type('ab');
    await type('a');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS));
    expect(puts).toHaveLength(1);
  });

  it('Save draft saves at once', async () => {
    await type('Quick');
    await click(button('Save draft'));
    expect(puts).toHaveLength(1);
    expect(text()).toMatch(/Saved \d\d:\d\d/);
  });

  it('keeps the text, says so and tries again when a save fails', async () => {
    putReply = () => new Response(null, { status: 500 });
    await type('Keep me');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(text()).toContain('Could not save your draft. Your text is still here.');
    expect(area().value).toBe('Keep me');
    putReply = () => Response.json({ draft_saved_at: '2026-11-28T10:50:00Z' });
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS));
    expect(puts).toHaveLength(2);
    expect(text()).toMatch(/Saved \d\d:\d\d/);
    expect(text()).not.toContain('Could not save');
  });

  it('waits Retry-After after a 429 on a draft', async () => {
    putReply = () => Response.json({}, { status: 429, headers: { 'Retry-After': '30' } });
    await type('Slow down');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_IDLE_MS));
    expect(puts).toHaveLength(1);
    putReply = () => Response.json({ draft_saved_at: '2026-11-28T10:50:00Z' });
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS));
    expect(puts).toHaveLength(1);
  });
});

describe('leaving and coming back', () => {
  it('seeds the editor from the newest saved text after the unmount save', async () => {
    await mount(writing());
    await type('A');
    await click(button('Save draft'));
    await type('AB');
    await act(async () => router.navigate('/path'));
    await settle();
    expect(puts.map((p) => p.body)).toEqual([JSON.stringify({ body: 'A' }), JSON.stringify({ body: 'AB' })]);
    expect(client.getQueryData<WritingItem>(itemKey(ID))?.work?.draft).toBe('AB');
    await act(async () => router.navigate(`/w/${ID}`));
    await settle();
    expect(area().value).toBe('AB');
    expect(text()).toMatch(/Saved \d\d:\d\d/);
  });

  it('puts a saved draft in the cached item, creating work when it was null', async () => {
    await mount(writing());
    await type('Hello');
    await click(button('Save draft'));
    const w = client.getQueryData<WritingItem>(itemKey(ID))?.work;
    expect(w?.draft).toBe('Hello');
    expect(w?.draft_saved_at).toBe('2026-11-28T10:42:00Z');
    expect(w?.review_state).toBeNull();
  });

  it('writes a submit that resolves after leaving into the cache and refreshes Today and Path', async () => {
    let release!: () => void;
    submitGate = new Promise<void>((r) => (release = r));
    await mount(writing());
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    await type('Hi Elena');
    await click(button('Ask for review'));
    await act(async () => router.navigate('/path'));
    await settle();
    release();
    await settle();
    expect(client.getQueryData<WritingItem>(itemKey(ID))?.work?.review_state).toBe('pending');
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toEqual([['today'], ['path']]);
  });
});

describe('limit and trimming', () => {
  beforeEach(() => mount(writing()));

  it('caps the textarea at 4,000 characters and counts live', async () => {
    expect(area().maxLength).toBe(4000);
    await type('a'.repeat(4000));
    expect(text()).toContain('4,000 of 4,000 characters');
    expect(text()).toContain('You reached the 4,000 character limit.');
  });

  it('does not enable Ask for review for spaces only, and sends nothing', async () => {
    await type('   \n  ');
    expect(button('Ask for review').disabled).toBe(true);
    await click(button('Ask for review'));
    expect(posts).toHaveLength(0);
  });
});

describe('submit and resubmit', () => {
  it('sends the text, shows the new review state and refreshes Today and Path', async () => {
    await mount(writing());
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    await type('Hi Elena, fixed.');
    await click(button('Ask for review'));
    expect(posts).toEqual([{ url: `/v1/items/${ID}/submit`, body: JSON.stringify({ body: 'Hi Elena, fixed.' }) }]);
    expect(text()).toContain('Carmen is reading it');
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toEqual([['today'], ['path']]);
    expect(client.getQueryData<WritingItem>(itemKey(ID))?.work?.review_state).toBe('pending');
  });

  it('does not save a draft for text that was just submitted, and saves later edits', async () => {
    await mount(writing());
    await type('First');
    await click(button('Ask for review'));
    vi.useFakeTimers();
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS * 2));
    expect(puts).toHaveLength(0);
    await type('First, and more');
    await act(async () => vi.advanceTimersByTimeAsync(AUTOSAVE_MIN_GAP_MS));
    expect(puts).toHaveLength(1);
  });

  it('resubmits edited text while a review is pending', async () => {
    await mount(writing({ work: work({ body: 'v1', submit_count: 1, review_state: 'pending' }) }));
    await type('v2');
    await click(button('Ask for review'));
    expect(posts).toHaveLength(1);
    expect(posts[0]?.body).toBe(JSON.stringify({ body: 'v2' }));
  });

  it('shows awarded marks with links to the celebration page', async () => {
    submitReply = () =>
      Response.json({
        item: writing({ done: true, work: work({ body: 'x', submit_count: 1, review_state: 'pending' }) }),
        awarded: [
          { kind: 'mark', inbox_id: 'inbox-1', slug: 'written-english' },
          { kind: 'belt', inbox_id: 'inbox-2' },
        ],
      });
    await mount(writing());
    await type('Hi');
    await click(button('Ask for review'));
    const links = [...container.querySelectorAll('a')].filter((a) => a.getAttribute('href')?.startsWith('/celebrate/'));
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/celebrate/inbox-1', '/celebrate/inbox-2']);
    expect(text()).toContain('You earned a belt mark: Written English for tickets.');
    expect(text()).toContain('You tied on a belt.');
    expect(container.querySelector('main [role="status"][aria-live="polite"] a')).not.toBeNull();
  });

  it('shows nothing about marks when none were awarded', async () => {
    await mount(writing());
    await type('Hi');
    await click(button('Ask for review'));
    expect(text()).not.toContain('belt mark');
  });
});

describe('submit errors', () => {
  const sendHi = async () => {
    await mount(writing());
    await type('Hi');
    await click(button('Ask for review'));
  };

  it('explains a 400 and keeps the text', async () => {
    submitReply = () => Response.json({ error: 'bad_request' }, { status: 400 });
    await sendHi();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Write something first.');
    expect(area().value).toBe('Hi');
  });

  it('explains a 404', async () => {
    submitReply = () => Response.json({ error: 'not_found' }, { status: 404 });
    await sendHi();
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('We could not find that task.');
  });

  it('blocks Ask for review until Retry-After on a 429', async () => {
    submitReply = () => Response.json({ error: 'rate_limited' }, { status: 429, headers: { 'Retry-After': '2' } });
    await mount(writing());
    await type('Hi');
    vi.useFakeTimers();
    await click(button('Ask for review'));
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Too many requests.');
    expect(button('Ask for review').disabled).toBe(true);
    expect(readBlockedUntil()).toBeGreaterThan(0);
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(button('Ask for review').disabled).toBe(false);
    expect(area().value).toBe('Hi');
  });

  it('says the work could not be sent on a server error', async () => {
    submitReply = () => new Response(null, { status: 500 });
    await sendHi();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not send your work. Your text is still here.');
    expect(button('Ask for review').disabled).toBe(false);
  });

  it('says the account is suspended on a 403 account_suspended', async () => {
    submitReply = () => Response.json({ error: 'account_suspended' }, { status: 403 });
    await sendHi();
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('This account is suspended.');
  });
});

describe('review states', () => {
  const withState = (review_state: WritingWork['review_state'], review = null as WritingWork['review']) =>
    writing({ work: work({ body: 'Hi', submit_count: 1, review_state, review }) });

  it('pending: says Carmen is reading it', async () => {
    await mount(withState('pending'));
    expect(text()).toContain('Carmen is reading it');
  });

  it('pending after an earlier review: keeps the old criteria and says so', async () => {
    await mount(withState('pending', REVIEW));
    expect(text()).toContain('Carmen is reading it');
    expect(text()).toContain('This is the review of your last submit.');
    expect(text()).toContain('What you found');
  });

  it('paused: says reviews are paused', async () => {
    await mount(withState('paused'));
    expect(text()).toContain('Reviews are paused, your work is saved');
  });

  it('capped: says no more reviews today', async () => {
    await mount(withState('capped'));
    expect(text()).toContain('No more reviews today. Your work is saved; ask for a review again tomorrow.');
    expect(button('Ask for review').disabled).toBe(false);
  });

  it('failed: says the review could not run', async () => {
    await mount(withState('failed'));
    expect(text()).toContain('The review could not run. Your work is saved; ask again.');
  });

  it('done: lists each criterion as passed or not yet, the evidence as text and the next step', async () => {
    await mount(withState('done', { ...REVIEW, criteria: [{ ...REVIEW.criteria[0]!, evidence: '<i>owns it</i>' }, REVIEW.criteria[1]!] }));
    const items = [...container.querySelectorAll('section[aria-label="Review"] li')];
    expect(items).toHaveLength(2);
    expect(items[0]?.textContent).toContain('Passed');
    expect(items[0]?.textContent).toContain('What you found: “<i>owns it</i>”');
    expect(items[0]?.querySelector('i')).toBeNull();
    expect(items[1]?.textContent).toContain('Not yet');
    expect(items[1]?.textContent).toContain('What she does now: Nothing tells her she can try again.');
    expect(container.querySelector('section[aria-label="Review"] .font-mono')?.textContent).toBe('1 of 2 criteria met1/2');
    expect(text()).toContain('Add one line: what Elena should do now.');
    expect(text()).not.toContain('Carmen is reading it');
    expect(text()).not.toContain('last submit');
  });

  it('done with every criterion met has no next step line', async () => {
    await mount(
      withState('done', { criteria: REVIEW.criteria.map((c) => ({ ...c, met: true, evidence: 'ok' })), next_step: '' }),
    );
    expect(container.querySelector('section[aria-label="Review"] .font-mono')?.textContent).toContain('2/2');
  });
});

describe('live review', () => {
  it('shows the finished review after the item is refetched', async () => {
    await mount(writing({ work: work({ body: 'Hi', submit_count: 1, review_state: 'pending' }) }));
    expect(text()).toContain('Carmen is reading it');
    current = writing({ done: true, work: work({ body: 'Hi', submit_count: 1, review_state: 'done', review: REVIEW }) });
    await act(async () => client.invalidateQueries({ queryKey: itemKey(ID) }));
    await settle();
    expect(text()).toContain('Add one line: what Elena should do now.');
    expect(text()).not.toContain('Carmen is reading it');
  });

  it('keeps what the learner is typing when the review arrives', async () => {
    await mount(writing({ work: work({ body: 'Hi', submit_count: 1, review_state: 'pending' }) }));
    await type('Hi, and more');
    current = writing({ work: work({ body: 'Hi', submit_count: 1, review_state: 'done', review: REVIEW }) });
    await act(async () => client.invalidateQueries({ queryKey: itemKey(ID) }));
    await settle();
    expect(area().value).toBe('Hi, and more');
  });
});
