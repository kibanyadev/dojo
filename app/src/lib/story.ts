import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import { getJSON, noteRateLimit, ReadError, retryAfterMs, type ReadFailure } from './read';
import { errorCode } from './settings';

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
  kind: string;
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

export const itemKey = (id: string) => ['item', id] as const;

const escapePath = (id: string) => id.split('/').map(encodeURIComponent).join('/');

export const fetchItem = (id: string) => getJSON<StoryItem>(`/v1/items/${escapePath(id)}`);

const retry = (count: number, err: Error) => !(err instanceof ReadError && err.reason !== 'failed') && count < 1;

export const useItem = (id: string) => useQuery({ queryKey: itemKey(id), queryFn: () => fetchItem(id), retry, staleTime: Infinity });

export type ChooseResult =
  | { ok: true; chosen: StoryChosen }
  | { ok: false; reason: 'already_chosen' | 'bad_choice' | ReadFailure; retryAfterMs?: number };

/** POST /v1/items/{id}/choice. The choice is final: a repeat of the same id is a 200, another one a 409. */
export async function chooseReply(id: string, choice: string): Promise<ChooseResult> {
  let res: Response;
  try {
    res = await apiFetch(`/v1/items/${escapePath(id)}/choice`, { method: 'POST', body: JSON.stringify({ choice }) });
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
    noteRateLimit(wait);
    return { ok: false, reason: 'rate_limited', retryAfterMs: wait };
  }
  if (res.status === 409 && (await errorCode(res)) === 'already_chosen') return { ok: false, reason: 'already_chosen' };
  if (res.status === 403 && (await errorCode(res)) === 'account_suspended') return { ok: false, reason: 'suspended' };
  return { ok: false, reason: 'failed' };
}
