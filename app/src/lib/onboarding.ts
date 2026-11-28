import { useQuery } from '@tanstack/react-query';
import { apiFetch } from './api';
import type { TicketState } from './read';
import { errorCode } from './settings';

export type CommitState = 'pending' | 'verified' | 'unverified';

export type StepId = 'o0' | 'o2' | 'o3' | 'o4' | 'o5';
export type NextStep = StepId | 'done';

export type CheckStatus = 'ok' | 'warn' | 'fail' | 'unknown';

/** One `dojo doctor` result as GET /v1/onboarding returns it (text and fix are untrusted, render them as text). */
export interface DoctorCheck {
  id: string;
  group?: string;
  status: CheckStatus;
  text: string;
  fix: string;
  /** `cli` for a `dojo doctor` result, `learner` for a step the learner confirmed on the page. */
  source?: string;
  /** When the check last ran (RFC 3339). */
  checked_at?: string;
}

export interface Onboarding {
  next_step: NextStep;
  env?: string;
  /** The origin the install line points at (section 3.9). */
  public_origin?: string;
  hosted_default?: boolean;
  checks?: DoctorCheck[];
  /** The AGENTS.md text of O4's copy block (section 3.9), the same file `dojo start` writes. */
  agents_md?: string;
  path: { slug: string | null; chosen: boolean };
  cli_connected: boolean;
  /** Sorted slugs of the planned paths the learner asked to hear about; a missing field reads as empty. */
  waitlist?: string[];
  /** The path's first ticket, stable once chosen; `state` is the learner's (null: not started), `commit_state` that of a pass's signed commit. */
  first_ticket: { id: string; title: string; state?: TicketState | null; commit_state?: CommitState | null } | null;
}

export const ONBOARDING_KEY = ['onboarding'] as const;

/** The route each unfinished step lives on (router paths, under the /app basename). */
export const STEP_ROUTES: Record<StepId, string> = {
  o0: '/start',
  o2: '/start/install',
  o3: '/start/doctor',
  o4: '/start/key',
  o5: '/start/first',
};

const KNOWN_STEPS = new Set<string>(['o0', 'o2', 'o3', 'o4', 'o5', 'done']);

/** Where a learner with this onboarding state belongs, or null when onboarding is finished or unknown. */
export function onboardingRedirect(state: Onboarding | null): string | null {
  if (!state || state.next_step === 'done') return null;
  return STEP_ROUTES[state.next_step];
}

/**
 * GET /v1/onboarding. Anything but a clear answer is null: a failed read never traps a working learner in
 * onboarding, it just skips the redirect.
 */
export async function fetchOnboarding(): Promise<Onboarding | null> {
  let res: Response;
  try {
    res = await apiFetch('/v1/onboarding');
  } catch {
    return null;
  }
  if (!res.ok) return null;
  try {
    const body = (await res.json()) as Onboarding;
    return KNOWN_STEPS.has(body.next_step) ? body : null;
  } catch {
    return null;
  }
}

export function useOnboarding(enabled = true) {
  return useQuery({ queryKey: ONBOARDING_KEY, queryFn: fetchOnboarding, retry: false, staleTime: 0, enabled });
}

export interface PathOption {
  slug: string;
  title: string;
  destination: string;
  steps: string;
  available: boolean;
}

// Must match the catalog: O0 is a public page and the catalog read needs a session.
export const PATHS: PathOption[] = [
  {
    slug: 'systems-devops',
    title: 'Systems and DevOps',
    destination: 'Junior DevOps',
    steps: 'Foundations · Support L1 · Systems L2 · Junior DevOps',
    available: true,
  },
  { slug: 'security', title: 'Security', destination: 'SOC L1 analyst', steps: 'Foundations · Support L1 · SOC L1', available: false },
  {
    slug: 'backend',
    title: 'Backend',
    destination: 'Junior Backend',
    steps: 'Foundations · Dev fundamentals · Junior Backend · Java, Python, Go or Node',
    available: false,
  },
  {
    slug: 'frontend',
    title: 'Frontend',
    destination: 'Junior Frontend',
    steps: 'Foundations · Dev fundamentals · Junior Frontend',
    available: false,
  },
];

export const DEFAULT_PATH = 'systems-devops';

export type WriteResult = { ok: true } | { ok: false; reason: 'planned' | 'available' | 'limit' | 'rate_limited' | 'failed' };

async function send(path: string, method: 'POST' | 'DELETE', body?: object): Promise<WriteResult> {
  let res: Response;
  try {
    res = await apiFetch(path, { method, body: body ? JSON.stringify(body) : undefined });
  } catch {
    return { ok: false, reason: 'failed' };
  }
  if (res.status === 204 || res.ok) return { ok: true };
  if (res.status === 429) return { ok: false, reason: 'rate_limited' };
  const code = await errorCode(res);
  if (res.status === 409 && code === 'path_planned') return { ok: false, reason: 'planned' };
  if (res.status === 409 && code === 'path_available') return { ok: false, reason: 'available' };
  if (res.status === 400) return { ok: false, reason: 'limit' };
  return { ok: false, reason: 'failed' };
}

/** POST /v1/onboarding/path. Signed out the API sets the short-lived dojo_path cookie; signed in it sets the choice. */
export const choosePath = (slug: string) => send('/v1/onboarding/path', 'POST', { path: slug });

/** "Tell me when": POST /v1/onboarding/waitlist for a planned path. */
export const joinWaitlist = (slug: string) => send('/v1/onboarding/waitlist', 'POST', { path: slug });

export const leaveWaitlist = (slug: string) => send(`/v1/onboarding/waitlist/${encodeURIComponent(slug)}`, 'DELETE');

export const WRITE_MESSAGES: Record<Exclude<WriteResult, { ok: true }>['reason'], string> = {
  planned: 'That path is not open yet.',
  available: 'That path is open, so there is no list to join.',
  limit: 'You can join up to three lists.',
  rate_limited: 'Too many tries. Wait a minute and try again.',
  failed: 'Could not save that. Please try again.',
};
