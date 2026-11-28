// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ACTIVITY_KEY, fetchToday, itemKey, PATH_KEY, PRACTICE_KEY, resetReadBlock, TODAY_KEY, ticketKey, type Ticket } from './read';
import { PROGRESS_DEBOUNCE_MS, useLiveData } from './stream';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Listener = (e: MessageEvent<string>) => void;
const sources: { emit: (name: string, data?: string) => void }[] = [];
let closed = 0;

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
  close() {
    closed++;
  }
  emit(name: string, data = '{}') {
    for (const fn of this.listeners.get(name) ?? []) fn({ data } as MessageEvent<string>);
  }
}

function Probe() {
  useLiveData();
  return null;
}

let root: Root;
let client: QueryClient;

beforeEach(() => {
  vi.useFakeTimers();
  sources.length = 0;
  closed = 0;
  vi.stubGlobal('EventSource', FakeES);
  client = new QueryClient();
  const el = document.createElement('div');
  root = createRoot(el);
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    );
  });
});
afterEach(() => {
  act(() => root.unmount());
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetReadBlock();
});

describe('useLiveData', () => {
  it('patches an open ticket at once and refetches path, today and activity once for a burst, 500 ms later', async () => {
    const key = ticketKey('a/b');
    client.setQueryData<Ticket>(key, {
      id: 'a/b', title: 't', role: 'a', est_minutes: 1, tags: [], brief: '', hint_count: 0, hints: [],
      progress: { state: 'started', lab: 'local', lab_state: 'starting', check_count: 0, last_check_ok: null, last_check_at: null, last_check_passed: null, last_check_total: null, last_check_output: null, first_passed_at: null, passed_seconds: null, passed_commit: null },
    });
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const ev = { item_id: 'a/b', state: 'started', lab_state: 'running', lab: 'local', check_count: 1, last_check_ok: false, last_check_at: null };
    await act(async () => {
      sources[0]!.emit('progress', JSON.stringify(ev));
      sources[0]!.emit('progress', JSON.stringify({ ...ev, check_count: 2 }));
    });
    expect(client.getQueryData<Ticket>(key)?.progress?.check_count).toBe(2);
    expect(invalidate).not.toHaveBeenCalled();
    await act(async () => {
      vi.advanceTimersByTime(PROGRESS_DEBOUNCE_MS);
    });
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toEqual([PATH_KEY, TODAY_KEY, ACTIVITY_KEY, PRACTICE_KEY, key]);
  });

  it('refetches the ticket of a progress event even when it has no progress row yet', async () => {
    client.setQueryData(ticketKey('a/b'), { id: 'a/b', progress: null });
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const ev = { item_id: 'a/b', state: 'started', lab_state: null, lab: null, check_count: 1, last_check_ok: true, last_check_at: null };
    await act(async () => {
      sources[0]!.emit('progress', JSON.stringify(ev));
      vi.advanceTimersByTime(PROGRESS_DEBOUNCE_MS);
    });
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toContainEqual(ticketKey('a/b'));
  });

  it('pushes a pending refetch back when a 429 lands while it waits', async () => {
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const ev = { item_id: 'a/b', state: 'started', lab_state: null, lab: null, check_count: 1, last_check_ok: true, last_check_at: null };
    await act(async () => {
      sources[0]!.emit('progress', JSON.stringify(ev));
      vi.advanceTimersByTime(100);
    });
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({}, { status: 429, headers: { 'Retry-After': '5' } })));
    await act(async () => {
      await fetchToday().catch(() => undefined);
      vi.advanceTimersByTime(PROGRESS_DEBOUNCE_MS);
    });
    expect(invalidate).not.toHaveBeenCalled();
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });
    expect(invalidate).toHaveBeenCalled();
  });

  it('opens one EventSource for two mounted hooks and closes it with the last', () => {
    const el = document.createElement('div');
    const second = createRoot(el);
    act(() => {
      second.render(
        <QueryClientProvider client={client}>
          <Probe />
        </QueryClientProvider>,
      );
    });
    expect(sources).toHaveLength(1);
    act(() => second.unmount());
    expect(closed).toBe(0);
    act(() => root.unmount());
    expect(closed).toBe(1);
    root = createRoot(document.createElement('div'));
  });

  it('refetches everything shown on ready', async () => {
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    await act(async () => {
      sources[0]!.emit('ready');
      vi.advanceTimersByTime(0);
    });
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toEqual([PATH_KEY, TODAY_KEY, ACTIVITY_KEY, PRACTICE_KEY, ['ticket'], ['item']]);
  });

  it('refetches today, path and that item when an item completes', async () => {
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    await act(async () => {
      sources[0]!.emit('item', JSON.stringify({ item_id: 'support-l1/quiz-02', kind: 'quiz' }));
      vi.advanceTimersByTime(PROGRESS_DEBOUNCE_MS);
    });
    expect(invalidate.mock.calls.map((c) => c[0]?.queryKey)).toEqual([TODAY_KEY, PATH_KEY, itemKey('support-l1/quiz-02')]);
  });

  it('ignores a malformed item event', async () => {
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    await act(async () => {
      sources[0]!.emit('item', 'not json');
      vi.advanceTimersByTime(PROGRESS_DEBOUNCE_MS);
    });
    expect(invalidate).not.toHaveBeenCalled();
  });
});
