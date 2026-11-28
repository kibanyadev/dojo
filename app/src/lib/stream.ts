import { useEffect } from 'react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { INBOX_KEY } from './inbox';
import type { CheckStatus } from './onboarding';
import { ACTIVITY_KEY, ITEM_KEY_PREFIX, itemKey, PATH_KEY, PRACTICE_KEY, readBlockedUntil, ticketKey, TICKET_KEY_PREFIX, TODAY_KEY, type Ticket, type TicketState } from './read';

export const STREAM_URL = '/v1/stream';
export const POLL_MS = 10_000;
export const IDLE_MS = 60_000;
export const REFUSED_RETRY_MS = 30_000;
export const PROGRESS_DEBOUNCE_MS = 500;

/** The `progress` event data: current state of one ticket, no check output. */
export interface ProgressEvent {
  item_id: string;
  state: TicketState | null;
  lab_state: string | null;
  lab: string | null;
  check_count: number | null;
  last_check_ok: boolean | null;
  last_check_at: string | null;
}

/** The `login` event (O2): a CLI signed in. The token itself never travels. */
export interface LoginEvent {
  device_name: string;
  os: string;
  cli_version: string;
  at: string;
}

/** The `doctor` event (O3): the ids a projection changed, without text or fix. */
export interface DoctorEvent {
  checks: { id: string; status: CheckStatus }[];
}

/** The `item` event (3.10): a non-ticket item was completed, here or on another device. */
export interface ItemEvent {
  item_id: string;
  kind: string;
}

/** The `review` event (3.10): the state of one item's review changed. */
export interface ReviewEvent {
  item_id: string;
  review_state: string;
}

export interface StreamHandlers {
  /** `ready` (every open, first included) and `resync`: refetch everything shown. */
  onSync: () => void;
  onProgress: (event: ProgressEvent) => void;
  /** Stream not holding: the 10 s polling fallback ticks. */
  onPoll: () => void;
  onLogin?: (event: LoginEvent) => void;
  onDoctor?: (event: DoctorEvent) => void;
  onItem?: (event: ItemEvent) => void;
  /** The `review` event (3.10): a review of this item finished or failed. */
  onReview?: (event: ReviewEvent) => void;
  /** The `inbox` event (3.10): the unread count may have changed; it carries no data. */
  onInbox?: () => void;
}

/** Opens GET /v1/stream; returns a close function. Reconnect, idle and polling rules: technical-design 3.8. */
export function openStream(h: StreamHandlers, url: string = STREAM_URL): () => void {
  let source: EventSource | null = null;
  let idle: ReturnType<typeof setTimeout> | undefined;
  let poll: ReturnType<typeof setInterval> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let closed = false;

  const startPoll = () => {
    poll ??= setInterval(h.onPoll, POLL_MS);
  };
  const stopPoll = () => {
    clearInterval(poll);
    poll = undefined;
  };
  const touch = () => {
    clearTimeout(idle);
    idle = setTimeout(() => {
      drop();
      connect();
    }, IDLE_MS);
  };
  const drop = () => {
    clearTimeout(idle);
    source?.close();
    source = null;
  };

  function connect() {
    if (closed) return;
    if (typeof EventSource === 'undefined') {
      startPoll();
      return;
    }
    const es = new EventSource(url);
    source = es;
    touch();
    es.addEventListener('ready', () => {
      touch();
      stopPoll();
      h.onSync();
    });
    es.addEventListener('resync', () => {
      touch();
      h.onSync();
    });
    es.addEventListener('heartbeat', touch);
    es.addEventListener('progress', (e) => {
      touch();
      try {
        h.onProgress(JSON.parse((e as MessageEvent<string>).data) as ProgressEvent);
      } catch {
        // A malformed message is ignored; the next ready or poll corrects the page.
      }
    });
    const listen = <T,>(name: string, handle: ((event: T) => void) | undefined) => {
      if (!handle) return;
      es.addEventListener(name, (e) => {
        touch();
        try {
          handle(JSON.parse((e as MessageEvent<string>).data) as T);
        } catch {
          // Ignored like a malformed progress message.
        }
      });
    };
    listen('login', h.onLogin);
    listen('doctor', h.onDoctor);
    listen('item', h.onItem);
    listen('review', h.onReview);
    listen('inbox', h.onInbox && (() => h.onInbox?.()));
    es.onerror = () => {
      startPoll();
      // CLOSED means a refused connect (429, 401): EventSource will not retry, so the page does after 30 s.
      if (es.readyState === 2 && source === es) {
        drop();
        retry = setTimeout(connect, REFUSED_RETRY_MS);
      }
    };
  }

  connect();
  return () => {
    closed = true;
    drop();
    stopPoll();
    clearTimeout(retry);
  };
}

/** Patches the open ticket from a progress event; any other ticket is returned as is. */
export function patchTicket(ticket: Ticket, e: ProgressEvent): Ticket {
  if (ticket.id !== e.item_id || !ticket.progress) return ticket;
  const p = ticket.progress;
  // A new check's counts and output are not in the event: drop the old ones rather than mix two checks.
  const newCheck = e.last_check_at !== p.last_check_at;
  return {
    ...ticket,
    progress: {
      ...p,
      state: e.state ?? p.state,
      lab: e.lab ?? p.lab,
      lab_state: e.lab_state,
      check_count: e.check_count ?? p.check_count,
      last_check_ok: e.last_check_ok,
      last_check_at: e.last_check_at,
      ...(newCheck && { last_check_passed: null, last_check_total: null, last_check_output: null }),
    },
  };
}

/**
 * Keeps the shown path, today, activity and ticket queries live while mounted. Only active queries refetch,
 * so "whichever it shows" is whatever is mounted. A 429 delays the next read until Retry-After.
 */
export function useLiveData(): void {
  const client = useQueryClient();
  useEffect(() => subscribe(client), [client]);
}

type Key = readonly string[];

interface Live {
  refs: number;
  pending: Map<string, Key>;
  timer: ReturnType<typeof setTimeout> | undefined;
  handlers: StreamHandlers;
}

// One EventSource however many hooks are mounted (StrictMode included); one scheduler per client.
const lives = new Map<QueryClient, Live>();
let closeShared: (() => void) | undefined;

function makeLive(client: QueryClient): Live {
  const live: Live = { refs: 0, pending: new Map(), timer: undefined, handlers: undefined as unknown as StreamHandlers };
  const fire = () => {
    // A 429 that landed while this timer waited pushes it back to the deadline.
    const wait = readBlockedUntil() - Date.now();
    if (wait > 0) {
      live.timer = setTimeout(fire, wait);
      return;
    }
    live.timer = undefined;
    const batch = [...live.pending.values()];
    live.pending.clear();
    for (const queryKey of batch) void client.invalidateQueries({ queryKey });
  };
  // One timer: keys pile up, a debounced progress never delays a sync already waiting.
  const schedule = (keys: readonly Key[], ms: number) => {
    keys.forEach((k) => live.pending.set(JSON.stringify(k), k));
    if (live.timer && ms > 0) return;
    clearTimeout(live.timer);
    live.timer = setTimeout(fire, Math.max(ms, readBlockedUntil() - Date.now(), 0));
  };
  const all = [PATH_KEY, TODAY_KEY, ACTIVITY_KEY, PRACTICE_KEY, TICKET_KEY_PREFIX, ITEM_KEY_PREFIX, INBOX_KEY];
  live.handlers = {
    onInbox: () => schedule([INBOX_KEY], PROGRESS_DEBOUNCE_MS),
    // A completion changes what Today offers next and what Path marks done, so both refetch with the item.
    onItem: (e) => schedule([TODAY_KEY, PATH_KEY, itemKey(e.item_id)], PROGRESS_DEBOUNCE_MS),
    onReview: (e) => schedule([itemKey(e.item_id)], PROGRESS_DEBOUNCE_MS),
    onSync: () => schedule(all, 0),
    onPoll: () => schedule(all, 0),
    onProgress: (e) => {
      client.setQueriesData<Ticket>({ queryKey: TICKET_KEY_PREFIX }, (t) => (t ? patchTicket(t, e) : t));
      // Always refetch the ticket too: a null progress row and the check output are not in the event.
      schedule([PATH_KEY, TODAY_KEY, ACTIVITY_KEY, PRACTICE_KEY, ticketKey(e.item_id)], PROGRESS_DEBOUNCE_MS);
    },
  };
  return live;
}

function subscribe(client: QueryClient): () => void {
  let live = lives.get(client);
  if (!live) {
    live = makeLive(client);
    lives.set(client, live);
  }
  live.refs++;
  closeShared ??= openStream({
    onSync: () => lives.forEach((l) => l.handlers.onSync()),
    onPoll: () => lives.forEach((l) => l.handlers.onPoll()),
    onProgress: (e) => lives.forEach((l) => l.handlers.onProgress(e)),
    onItem: (e) => lives.forEach((l) => l.handlers.onItem?.(e)),
    onReview: (e) => lives.forEach((l) => l.handlers.onReview?.(e)),
    onInbox: () => lives.forEach((l) => l.handlers.onInbox?.()),
  });
  const mine = live;
  return () => {
    if (--mine.refs > 0) return;
    clearTimeout(mine.timer);
    lives.delete(client);
    if (lives.size === 0) {
      closeShared?.();
      closeShared = undefined;
    }
  };
}
