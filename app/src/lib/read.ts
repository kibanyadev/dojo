import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import { errorCode } from './settings';

export type PathStatus = 'available' | 'planned';
export type TicketState = 'started' | 'passed' | 'revisit';
export type LabState = string;

export interface CatalogPath {
  slug: string;
  title: string;
  status: PathStatus;
  position: number;
}

export interface CatalogRole {
  slug: string;
  title: string;
  summary: string;
  kind: string;
  has_exam: boolean;
  advanced: boolean;
}

export interface CatalogItem {
  id: string;
  role: string;
  order: number;
  kind: string;
  title: string;
  est_minutes: number;
  tags: string[];
}

export interface CatalogMark {
  slug: string;
  belt: string;
  title: string;
  summary: string;
  position: number;
}

export interface Catalog {
  catalog_version: string | null;
  paths: CatalogPath[];
  roles: CatalogRole[];
  path_roles: { path: string; role: string; position: number }[];
  items: CatalogItem[];
  marks: CatalogMark[];
}

export interface PathItem {
  id: string;
  title: string;
  order: number;
  state: TicketState | null;
  lab_state: LabState | null;
}

export type BetweenKind = 'writing' | 'quiz' | 'story' | 'feedback';

/** A non-ticket item of a role in GET /v1/path (design 3.10). */
export interface BetweenItem {
  id: string;
  kind: BetweenKind;
  title: string;
  order: number;
  done: boolean;
}

/** The item GET /v1/today offers between tickets (design 3.10). */
export interface TodayBetween {
  id: string;
  kind: BetweenKind;
  role: string;
  title: string;
  est_minutes: number;
}

export interface PathRole {
  slug: string;
  title: string;
  summary: string;
  locked: boolean;
  passed: boolean;
  tickets_passed: number;
  tickets_total: number;
  items: PathItem[];
  between_items?: BetweenItem[];
}

export interface LearnerPath {
  path: string | null;
  chosen: boolean;
  roles: PathRole[];
}

export interface TodayTicket {
  id: string;
  title: string;
  state: TicketState | null;
  lab_state: LabState | null;
}

/** The learner's earliest first pass while they have not left "day one done" (design 3.9). */
export interface FirstRun {
  id: string;
  title: string;
  first_passed_at: string;
  passed_commit_state: 'pending' | 'verified' | 'unverified' | null;
}

export interface Today {
  first_run?: FirstRun | null;
  current: TodayTicket | null;
  next: TodayTicket | null;
  role: { slug: string; title: string; tickets_passed: number; tickets_total: number } | null;
  all_passed: boolean;
  between?: TodayBetween | null;
}

export interface TicketProgress {
  state: TicketState;
  lab: string | null;
  lab_state: LabState | null;
  check_count: number;
  last_check_ok: boolean | null;
  last_check_at: string | null;
  last_check_passed: number | null;
  last_check_total: number | null;
  /** Untrusted lab output: render escaped, never log it. */
  last_check_output: string | null;
  first_passed_at: string | null;
  passed_seconds: number | null;
  passed_commit: string | null;
  passed_commit_state?: 'pending' | 'verified' | 'unverified' | null;
  passed_commit_url?: string | null;
}

export interface Ticket {
  id: string;
  title: string;
  role: string | null;
  est_minutes: number;
  tags: string[];
  brief: string;
  hint_count: number;
  hints: { n: number; text: string }[];
  progress: TicketProgress | null;
}

export type Difficulty = 'easy' | 'medium' | 'hard';

/** One row of GET /v1/practice: practice scenarios first, then tickets of unlocked roles as revisits. */
export interface PracticeItem {
  id: string;
  kind: 'practice' | 'ticket';
  role: string | null;
  title: string;
  est_minutes: number;
  tags: string[];
  difficulty: Difficulty | null;
  state: TicketState | null;
  lab_state: LabState | null;
}

export interface ActivityDay {
  /** Calendar day `YYYY-MM-DD` in the zone the read was made with. */
  day: string;
  count: number;
}

export interface ActivityRecent {
  ticket_id: string;
  title: string;
  at: string;
  /** Null until signed commits exist (M3). */
  commit: string | null;
}

/** GET /v1/activity: first passes of tickets over the 140 days ending today, plus the last three. */
export interface Activity {
  from: string;
  to: string;
  /** Only days with a count above 0. */
  days: ActivityDay[];
  /** Every first pass ever, not only the window. */
  total: number;
  recent: ActivityRecent[];
}

export type ReadFailure = 'signed_out' | 'suspended' | 'not_found' | 'rate_limited' | 'failed';

export const READ_MESSAGES: Record<ReadFailure, string> = {
  signed_out: 'Your session ended. Sign in again.',
  suspended: 'This account is suspended.',
  not_found: 'We could not find that ticket.',
  rate_limited: 'Too many requests. Waiting a moment before trying again.',
  failed: 'Could not load this. Please try again.',
};

export class ReadError extends Error {
  constructor(
    readonly reason: ReadFailure,
    /** Milliseconds from a 429 Retry-After, else 0. */
    readonly retryAfterMs = 0,
  ) {
    super(`read: ${reason}`);
  }
}

// 120 reads a minute per learner: after a 429 no read goes out before Retry-After has passed.
let blockedUntil = 0;

/** Epoch ms before which live refetches must wait (0 when none). */
export function readBlockedUntil(): number {
  return blockedUntil;
}

export function resetReadBlock(): void {
  blockedUntil = 0;
}

function retryAfterMs(res: Response): number {
  const seconds = Number(res.headers.get('Retry-After'));
  return Number.isFinite(seconds) && seconds > 0 ? Math.ceil(seconds) * 1000 : 1000;
}

async function getJSON<T>(path: string): Promise<T> {
  for (let wait = blockedUntil - Date.now(); wait > 0; wait = blockedUntil - Date.now()) {
    await new Promise((resolve) => setTimeout(resolve, wait));
  }
  let res: Response;
  try {
    res = await apiFetch(path);
  } catch {
    throw new ReadError('failed');
  }
  if (res.ok) {
    try {
      return (await res.json()) as T;
    } catch {
      throw new ReadError('failed');
    }
  }
  if (res.status === 401) throw new ReadError('signed_out');
  if (res.status === 403 && (await errorCode(res)) === 'account_suspended') throw new ReadError('suspended');
  if (res.status === 404) throw new ReadError('not_found');
  if (res.status === 429) {
    const wait = retryAfterMs(res);
    blockedUntil = Math.max(blockedUntil, Date.now() + wait);
    throw new ReadError('rate_limited', wait);
  }
  throw new ReadError('failed');
}

export const fetchCatalog = () => getJSON<Catalog>('/v1/catalog');
export const fetchPath = () => getJSON<LearnerPath>('/v1/path');
export const fetchToday = () => getJSON<Today>('/v1/today');
export const fetchPractice = () => getJSON<{ items: PracticeItem[] }>('/v1/practice');

/** The browser's IANA zone, or null when it cannot say (the read then omits tz and the server uses UTC). */
export function browserZone(): string | null {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || null;
  } catch {
    return null;
  }
}

export function fetchActivity(): Promise<Activity> {
  const tz = browserZone();
  return getJSON<Activity>(tz ? `/v1/activity?tz=${encodeURIComponent(tz)}` : '/v1/activity');
}

/** Ticket ids hold a slash (`support-l1/perms-03`); each segment is escaped, the slash stays. */
export function fetchTicket(id: string): Promise<Ticket> {
  const escaped = id.split('/').map(encodeURIComponent).join('/');
  return getJSON<Ticket>(`/v1/tickets/${escaped}`);
}

/** Per-question state of a quiz (technical-design 3.10); null until the question is answered. */
export interface AnswerState {
  first_choice: number;
  first_correct: boolean;
  last_choice: number;
  last_correct: boolean;
  answer: number;
  notes: string[];
  explain: string | null;
}

export interface QuizQuestion {
  id: string;
  text: string;
  /** Texts only, inline markdown. */
  choices: string[];
  answer_state: AnswerState | null;
}

/** GET /v1/items/{id} for a quiz. Other kinds share the head and are not drawn by L11. */
export interface QuizItem {
  id: string;
  kind: 'quiz';
  role: string | null;
  title: string;
  est_minutes: number;
  tags: string[];
  done: boolean;
  answered: number;
  total: number;
  first_try_correct: number;
  questions: QuizQuestion[];
}

export type Verdict = 'good' | 'okay' | 'poor';

export interface StoryChoice {
  id: string;
  text: string;
}

export interface StoryChosen {
  id: string;
  verdict: Verdict;
  reaction: string;
  chosen_at: string;
}

/** GET /v1/items/{id} for a story or feedback item (design 3.10). Feedback has no `choices` or `chosen`. */
export interface StoryItem {
  id: string;
  kind: 'story' | 'feedback';
  role: string | null;
  title: string;
  est_minutes: number;
  tags: string[];
  done: boolean;
  from?: { name: string; role: string };
  scene?: string;
  choices?: StoryChoice[];
  chosen?: StoryChosen | null;
}

export interface OtherItem {
  id: string;
  kind: 'writing';
}

export type Item = QuizItem | StoryItem | OtherItem;

const escapeId = (id: string) => id.split('/').map(encodeURIComponent).join('/');

export const fetchItem = (id: string) => getJSON<Item>(`/v1/items/${escapeId(id)}`);

/** The 200 of POST /v1/items/{id}/answers. */
export interface AnswerResult {
  correct: boolean;
  answer: number;
  notes: string[];
  explain: string | null;
  done: boolean;
  answered: number;
  total: number;
  first_try_correct: number;
}

export type AnswerOutcome = { ok: true; result: AnswerResult } | { ok: false; reason: ReadFailure; retryAfterMs?: number };

/** POST /v1/items/{id}/answers: the server checks the choice; a wrong answer is still a 200. */
export async function postAnswer(id: string, question: string, choice: number): Promise<AnswerOutcome> {
  let res: Response;
  try {
    res = await apiFetch(`/v1/items/${escapeId(id)}/answers`, { method: 'POST', body: JSON.stringify({ question, choice }) });
  } catch {
    return { ok: false, reason: 'failed' };
  }
  if (res.ok) {
    try {
      return { ok: true, result: (await res.json()) as AnswerResult };
    } catch {
      return { ok: false, reason: 'failed' };
    }
  }
  if (res.status === 401) return { ok: false, reason: 'signed_out' };
  if (res.status === 404) return { ok: false, reason: 'not_found' };
  if (res.status === 429) {
    const wait = retryAfterMs(res);
    blockedUntil = Math.max(blockedUntil, Date.now() + wait);
    return { ok: false, reason: 'rate_limited', retryAfterMs: wait };
  }
  if (res.status === 403 && (await errorCode(res)) === 'account_suspended') return { ok: false, reason: 'suspended' };
  return { ok: false, reason: 'failed' };
}

export type ChooseResult =
  | { ok: true; chosen: StoryChosen }
  | { ok: false; reason: 'already_chosen' | 'bad_choice' | ReadFailure; retryAfterMs?: number };

/** POST /v1/items/{id}/choice. The choice is final: a repeat of the same id is a 200, another one a 409. */
export async function chooseReply(id: string, choice: string): Promise<ChooseResult> {
  let res: Response;
  try {
    res = await apiFetch(`/v1/items/${escapeId(id)}/choice`, { method: 'POST', body: JSON.stringify({ choice }) });
  } catch {
    return { ok: false, reason: 'failed' };
  }
  if (res.ok) {
    try {
      const body = (await res.json()) as { chosen?: StoryChosen };
      return body.chosen ? { ok: true, chosen: body.chosen } : { ok: false, reason: 'failed' };
    } catch {
      return { ok: false, reason: 'failed' };
    }
  }
  if (res.status === 401) return { ok: false, reason: 'signed_out' };
  if (res.status === 404) return { ok: false, reason: 'bad_choice' };
  if (res.status === 429) {
    const wait = retryAfterMs(res);
    blockedUntil = Math.max(blockedUntil, Date.now() + wait);
    return { ok: false, reason: 'rate_limited', retryAfterMs: wait };
  }
  if (res.status === 409 && (await errorCode(res)) === 'already_chosen') return { ok: false, reason: 'already_chosen' };
  if (res.status === 403 && (await errorCode(res)) === 'account_suspended') return { ok: false, reason: 'suspended' };
  return { ok: false, reason: 'failed' };
}

export interface Hint {
  n: number;
  text: string;
}

export type HintResult = { ok: true; hint: Hint } | { ok: false; reason: 'hint_locked' | ReadFailure; retryAfterMs?: number };

/** POST /v1/tickets/{id}/hints/{n}: opens one written hint, in order. */
export async function openHint(id: string, n: number): Promise<HintResult> {
  const escaped = id.split('/').map(encodeURIComponent).join('/');
  let res: Response;
  try {
    res = await apiFetch(`/v1/tickets/${escaped}/hints/${n}`, { method: 'POST', body: '{}' });
  } catch {
    return { ok: false, reason: 'failed' };
  }
  if (res.ok) {
    try {
      return { ok: true, hint: (await res.json()) as Hint };
    } catch {
      return { ok: false, reason: 'failed' };
    }
  }
  if (res.status === 401) return { ok: false, reason: 'signed_out' };
  if (res.status === 404) return { ok: false, reason: 'not_found' };
  if (res.status === 429) {
    const wait = retryAfterMs(res);
    blockedUntil = Math.max(blockedUntil, Date.now() + wait);
    return { ok: false, reason: 'rate_limited', retryAfterMs: wait };
  }
  if (res.status === 409 && (await errorCode(res)) === 'hint_locked') return { ok: false, reason: 'hint_locked' };
  if (res.status === 403 && (await errorCode(res)) === 'account_suspended') return { ok: false, reason: 'suspended' };
  return { ok: false, reason: 'failed' };
}

export const CATALOG_KEY = ['catalog'] as const;
export const PATH_KEY = ['path'] as const;
export const TODAY_KEY = ['today'] as const;
export const ACTIVITY_KEY = ['activity'] as const;
export const PRACTICE_KEY = ['practice'] as const;
export const ticketKey = (id: string) => ['ticket', id] as const;
export const TICKET_KEY_PREFIX = ['ticket'] as const;
export const itemKey = (id: string) => ['item', id] as const;
export const ITEM_KEY_PREFIX = ['item'] as const;

// Data is current until the stream or the poll says otherwise; retry only a transient failure.
const retry = (count: number, err: Error) => !(err instanceof ReadError && err.reason !== 'failed') && count < 1;

export const useCatalog = () => useQuery({ queryKey: CATALOG_KEY, queryFn: fetchCatalog, retry, staleTime: 5 * 60_000 });
export const usePath = () => useQuery({ queryKey: PATH_KEY, queryFn: fetchPath, retry, staleTime: Infinity });
export const useToday = () => useQuery({ queryKey: TODAY_KEY, queryFn: fetchToday, retry, staleTime: Infinity });
export const usePractice = () => useQuery({ queryKey: PRACTICE_KEY, queryFn: fetchPractice, retry, staleTime: Infinity });
export const useTicket = (id: string) =>
  useQuery({ queryKey: ticketKey(id), queryFn: () => fetchTicket(id), retry, staleTime: Infinity });
export const useItem = (id: string) => useQuery({ queryKey: itemKey(id), queryFn: () => fetchItem(id), retry, staleTime: Infinity });
export const useActivity = () => useQuery({ queryKey: ACTIVITY_KEY, queryFn: fetchActivity, retry, staleTime: Infinity });
