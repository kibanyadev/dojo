import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './api';
import { getJSON, retry } from './read';

/** One row of GET /v1/inbox (technical-design 3.10, N1). Title and body are plain text. */
export interface InboxItem {
  id: string;
  kind: 'mark' | 'belt' | 'review_ready' | 'story' | 'quiz' | 'check';
  ref: string;
  title: string;
  body: string;
  /** A server-written SPA path; never trusted: see `safeInboxUrl`. */
  url: string;
  read: boolean;
  at: string;
}

export interface InboxPage {
  unread: number;
  /** Opaque cursor for the next page, null on the last. */
  next: string | null;
  items: InboxItem[];
}

export const INBOX_KEY = ['inbox'] as const;
export const INBOX_UNREAD_KEY = ['inbox', 'unread'] as const;
export const INBOX_LIST_KEY = ['inbox', 'list'] as const;

export const INBOX_PAGE_SIZE = 20;

export function fetchInbox(opts: { limit: number; before?: string | null }): Promise<InboxPage> {
  const q = new URLSearchParams({ limit: String(opts.limit) });
  if (opts.before) q.set('before', opts.before);
  return getJSON<InboxPage>(`/v1/inbox?${q.toString()}`);
}

/** The unread count: a one-row read, so the bell and the tab badge stay cheap. */
export const useUnread = () =>
  useQuery({ queryKey: INBOX_UNREAD_KEY, queryFn: () => fetchInbox({ limit: 1 }), select: (page) => page.unread, retry, staleTime: Infinity });

/** The list, newest first, one cursor page at a time. */
export const useInboxList = () =>
  useInfiniteQuery({
    queryKey: INBOX_LIST_KEY,
    queryFn: ({ pageParam }) => fetchInbox({ limit: INBOX_PAGE_SIZE, before: pageParam }),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next,
    retry,
    staleTime: Infinity,
  });

/** Only a path on this app: one leading slash, not `//`, no backslash or control character. */
export function safeInboxUrl(url: string): string | null {
  // eslint-disable-next-line no-control-regex
  return /^\/(?![/\\])[^\\\u0000-\u001f\u007f]*$/.test(url) ? url : null;
}

async function post(path: string): Promise<boolean> {
  try {
    return (await apiFetch(path, { method: 'POST' })).status === 204;
  } catch {
    return false;
  }
}

/** The two writes of N1; each refetches the count and the open list when it lands. */
export function useInboxActions() {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: INBOX_KEY });
  return {
    markRead: async (id: string) => {
      if (await post(`/v1/inbox/${encodeURIComponent(id)}/read`)) await refresh();
    },
    markAllRead: async () => {
      if (await post('/v1/inbox/read-all')) await refresh();
    },
  };
}

export interface InboxGroup {
  label: 'Today' | 'Earlier';
  items: InboxItem[];
}

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

/** Today's rows, then the rest; the API already orders by recency, so each group keeps that order. */
export function groupInbox(items: InboxItem[], now: Date): InboxGroup[] {
  const today = items.filter((i) => sameDay(new Date(i.at), now));
  const earlier = items.filter((i) => !sameDay(new Date(i.at), now));
  return [
    ...(today.length ? [{ label: 'Today' as const, items: today }] : []),
    ...(earlier.length ? [{ label: 'Earlier' as const, items: earlier }] : []),
  ];
}

const DAY_MS = 86_400_000;

/** `10:42` for today, a weekday for the past week (`Mon`), else a date (`Nov 3`). */
export function formatInboxTime(at: string, now: Date): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return '';
  if (sameDay(d, now)) return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  if (now.getTime() - d.getTime() < 6 * DAY_MS) return d.toLocaleDateString('en-US', { weekday: 'short' });
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}
