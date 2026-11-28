import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { Mentor } from './Mentor';
import { ME_KEY } from '../lib/me';
import {
  itemKey,
  PATH_KEY,
  READ_MESSAGES,
  saveDraft,
  submitWriting,
  TODAY_KEY,
  type Awarded,
  type Item,
  type Review,
  type WorkItem,
  type WriteFailure,
  type WritingWork,
} from '../lib/read';

export const MAX_CHARS = 4000;
export const AUTOSAVE_IDLE_MS = 2000;
export const AUTOSAVE_MIN_GAP_MS = 10_000;

export const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5';
export const cardTitle = 'm-0 text-base font-extrabold';
export const badge = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';
export const ghostBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] border border-ctl bg-surface px-3.5 text-sm font-bold text-fg no-underline md:h-9';
const primaryBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] bg-accent px-[18px] text-[15px] font-bold text-accentfg disabled:opacity-60';
const quietBtn = 'inline-flex h-11 items-center justify-center rounded-[10px] bg-transparent px-[18px] text-[15px] font-bold text-fg2 disabled:opacity-60';

/** The state copy of design 3.10. `done` has no line: the criteria speak. */
const STATE_COPY: Record<'pending' | 'paused' | 'capped' | 'failed', (reviewer: string) => string> = {
  pending: (reviewer) => `${reviewer} is reading it`,
  paused: () => 'Reviews are paused, your work is saved',
  capped: () => 'No more reviews today. Your work is saved; ask for a review again tomorrow.',
  failed: () => 'The review could not run. Your work is saved; ask again.',
};

const clock = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false });

function savedLabel(iso: string | null | undefined): string {
  if (!iso) return '';
  const at = new Date(iso);
  return Number.isNaN(at.getTime()) ? 'Saved' : `Saved ${clock.format(at)}`;
}

const formatCount = (n: number) => n.toLocaleString('en-US');

function Tick({ met }: { met: boolean }) {
  return (
    <span
      className={`inline-flex size-5 flex-none items-center justify-center rounded-full text-xs font-extrabold text-accentfg ${met ? 'bg-ok' : 'bg-bad'}`}
    >
      <span aria-hidden="true">{met ? '✓' : '✗'}</span>
      <span className="sr-only">{met ? 'Passed' : 'Not yet'}</span>
    </span>
  );
}

function ReviewCard({
  review,
  rubric,
  stale,
  reviewer,
}: {
  review: Review | null;
  rubric: NonNullable<WorkItem['rubric']>;
  stale: boolean;
  reviewer: string;
}) {
  if (!review) {
    return (
      <section aria-label="Review" className={card}>
        <h2 className={cardTitle}>Review</h2>
        <p className="m-0 text-sm leading-[1.55] text-fg2">{reviewer} reads your reply against these points. Ask for a review when you are ready.</p>
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {rubric.map((c) => (
            <li key={c.id} className="text-sm leading-[1.45] font-bold">
              {c.text}
            </li>
          ))}
        </ul>
      </section>
    );
  }
  const met = review.criteria.filter((c) => c.met).length;
  const total = review.criteria.length;
  return (
    <section aria-label="Review" className={card}>
      <div className="flex items-center justify-between gap-3">
        <h2 className={cardTitle}>Review</h2>
        <span className={`${badge} font-mono ${met === total ? 'bg-oksoft text-ok' : 'bg-badsoft text-bad'}`}>
          <span className="sr-only">{`${met} of ${total} criteria met`}</span>
          <span aria-hidden="true">{`${met}/${total}`}</span>
        </span>
      </div>
      {stale && <p className="m-0 text-[13px] text-muted">This is the review of your last submit.</p>}
      <ul className="m-0 flex list-none flex-col gap-3 p-0">
        {review.criteria.map((c) => {
          const detail = c.met ? c.evidence : c.note;
          return (
            <li key={c.id} className="flex items-start gap-2.5 text-sm leading-[1.45]">
              <Tick met={c.met} />
              <span>
                <b>{c.text}</b>
                {detail ? <>: {c.met ? `“${detail}”` : detail}</> : null}
              </span>
            </li>
          );
        })}
      </ul>
      {review.next_step && <p className="m-0 text-sm leading-[1.55] text-fg2">{review.next_step}</p>}
    </section>
  );
}

function Awards({ awarded, item }: { awarded: Awarded[]; item: WorkItem }) {
  return (
    <>
      {awarded.map((a) => (
        <p key={a.inbox_id} className="m-0 text-sm font-bold">
          {a.kind === 'belt' ? 'You tied on a belt. ' : `You earned a belt mark${item.awards_mark ? `: ${item.awards_mark.title}` : ''}. `}
          <Link to={`/celebrate/${encodeURIComponent(a.inbox_id)}`} className="text-accentink">
            {a.kind === 'belt' ? 'See your belt' : 'See your mark'}
          </Link>
        </p>
      ))}
    </>
  );
}

const submitMessage = (reason: WriteFailure): string =>
  reason === 'bad_request'
    ? `Write something first. Your reply can be up to ${formatCount(MAX_CHARS)} characters.`
    : reason === 'not_found'
      ? 'We could not find that task.'
      : reason === 'rate_limited'
        ? READ_MESSAGES.rate_limited
        : reason === 'suspended'
          ? READ_MESSAGES.suspended
          : 'Could not send your work. Your text is still here. Please try again.';

/** Keeps the cached item in step with the server's draft, so a remount seeds from the newest text. */
function patchDraft(client: QueryClient, id: string, body: string, savedAt: string) {
  client.setQueryData<Item>(itemKey(id), (old) => {
    if (!old || old.kind !== 'writing' && old.kind !== 'feedback') return old;
    // A late response from an older save never overwrites a newer cached draft.
    const cached = old.work?.draft_saved_at;
    if (cached && Date.parse(cached) > Date.parse(savedAt)) return old;
    const base: WritingWork = old.work ?? {
      draft: null,
      draft_saved_at: null,
      body: null,
      submitted_at: null,
      submit_count: 0,
      review_state: null,
      reviewed_at: null,
      review: null,
    };
    return { ...old, work: { ...base, draft: body, draft_saved_at: savedAt } };
  });
}

/** The autosaved reply box and its review, shared by the writing task (L9) and the feedback round (L4). `lead` is the task or the message. */
export function ReplyEditor({ item, lead }: { item: WorkItem; lead: ReactNode }) {
  const queryClient = useQueryClient();
  const reviewer = ('from' in item && item.from?.name) || 'Carmen';
  const work = item.work;
  const initial = work?.draft ?? work?.body ?? '';
  const [text, setText] = useState(initial);
  const [saveStatus, setSaveStatus] = useState(work?.draft_saved_at ? savedLabel(work.draft_saved_at) : '');
  const [saveFailed, setSaveFailed] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const [notice, setNotice] = useState('');
  const [awarded, setAwarded] = useState<Awarded[]>([]);

  const textRef = useRef(initial);
  const savedRef = useRef(initial);
  const lastSaveAt = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const saving = useRef<Promise<void> | null>(null);
  const busy = useRef(false);
  const unblock = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  const flushRef = useRef<() => Promise<void>>(async () => {});

  function schedule(delay?: number) {
    clearTimeout(timer.current);
    const wait = delay ?? Math.max(AUTOSAVE_IDLE_MS, lastSaveAt.current + AUTOSAVE_MIN_GAP_MS - Date.now());
    timer.current = setTimeout(() => void flushRef.current(), wait);
  }

  async function flush(): Promise<void> {
    clearTimeout(timer.current);
    if (saving.current) {
      await saving.current;
      return;
    }
    const sent = textRef.current;
    if (busy.current || sent === savedRef.current || sent.length > MAX_CHARS) return;
    lastSaveAt.current = Date.now();
    const run = (async () => {
      const res = await saveDraft(item.id, sent);
      if (res.ok) {
        savedRef.current = sent;
        patchDraft(queryClient, item.id, sent, res.savedAt);
      }
      if (!mounted.current) return;
      if (res.ok) {
        setSaveStatus(savedLabel(res.savedAt));
        setSaveFailed(false);
        if (textRef.current !== sent) schedule();
        return;
      }
      if (res.reason === 'signed_out') {
        queryClient.setQueryData(ME_KEY, null);
        return;
      }
      setSaveFailed(true);
      schedule(Math.max(AUTOSAVE_MIN_GAP_MS, res.retryAfterMs ?? 0));
    })();
    saving.current = run;
    try {
      await run;
    } finally {
      saving.current = null;
    }
  }
  useEffect(() => {
    flushRef.current = flush;
  });

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
      clearTimeout(unblock.current);
      // Leaving with unsaved text: one last best-effort save, after any save in flight, and the cache learns it.
      const last = async () => {
        await saving.current;
        const body = textRef.current;
        if (busy.current || body === savedRef.current || body.length > MAX_CHARS) return;
        const res = await saveDraft(item.id, body);
        if (res.ok) {
          savedRef.current = body;
          patchDraft(queryClient, item.id, body, res.savedAt);
        }
      };
      void last();
    };
  }, [item.id, queryClient]);

  function edit(value: string) {
    textRef.current = value;
    setText(value);
    setNotice('');
    schedule();
  }

  async function submit() {
    if (busy.current || blocked) return;
    const body = textRef.current;
    if (body.trim() === '' || body.trim().length > MAX_CHARS) {
      setNotice(submitMessage('bad_request'));
      return;
    }
    clearTimeout(timer.current);
    if (saving.current) await saving.current;
    busy.current = true;
    setSubmitting(true);
    setNotice('');
    setAwarded([]);
    const res = await submitWriting(item.id, body);
    busy.current = false;
    if (res.ok) {
      savedRef.current = body;
      queryClient.setQueryData(itemKey(item.id), res.item);
      void queryClient.invalidateQueries({ queryKey: TODAY_KEY });
      void queryClient.invalidateQueries({ queryKey: PATH_KEY });
    }
    if (!mounted.current) return;
    setSubmitting(false);
    if (res.ok) {
      setSaveFailed(false);
      setSaveStatus(savedLabel(res.item.work?.draft_saved_at ?? res.item.work?.submitted_at));
      setAwarded(res.awarded);
      if (textRef.current !== body) schedule();
      return;
    }
    if (res.reason === 'signed_out') {
      queryClient.setQueryData(ME_KEY, null);
      return;
    }
    setNotice(submitMessage(res.reason));
    if (res.reason === 'rate_limited') {
      setBlocked(true);
      unblock.current = setTimeout(() => setBlocked(false), res.retryAfterMs ?? 1000);
    }
    if (textRef.current !== savedRef.current) schedule();
  }

  const state = work?.review_state ?? null;
  const review = work?.review ?? null;
  const trimmedEmpty = text.trim() === '';
  const atLimit = text.length >= MAX_CHARS;

  return (
    <div className="flex flex-col gap-3.5 md:grid md:grid-cols-[1fr_420px] md:items-start md:gap-6">
      <div className="flex min-w-0 flex-col gap-[18px]">
        {lead}
        <section aria-labelledby="reply-title" className={card}>
          <div className="flex items-center justify-between gap-3">
            <h2 id="reply-title" className={cardTitle}>
              Your reply
            </h2>
            <span className="font-mono text-xs text-muted">AI help allowed · you defend every line</span>
          </div>
          <textarea
            aria-labelledby="reply-title"
            aria-describedby="reply-count"
            maxLength={MAX_CHARS}
            value={text}
            onChange={(e) => edit(e.target.value)}
            readOnly={submitting}
            className="min-h-[220px] w-full resize-y rounded-[10px] border border-ctl bg-surface p-3.5 text-[15px] leading-[1.6] text-fg"
          />
          <div className="flex flex-wrap items-center justify-between gap-2 text-[13px] text-muted">
            <span id="reply-count">{`${formatCount(text.length)} of ${formatCount(MAX_CHARS)} characters`}</span>
            <span role="status" aria-live="polite" className={saveFailed ? 'font-bold text-bad' : ''}>
              {saveFailed ? 'Could not save your draft. Your text is still here.' : saveStatus}
            </span>
          </div>
          <div role="status" aria-live="polite" className="m-0 text-[13px] font-bold text-bad empty:hidden">
            {atLimit ? `You reached the ${formatCount(MAX_CHARS)} character limit.` : ''}
          </div>
          <div className="flex flex-wrap gap-2.5">
            <button type="button" onClick={() => void submit()} disabled={submitting || blocked || trimmedEmpty} className={primaryBtn}>
              {submitting ? 'Sending...' : 'Ask for review'}
            </button>
            <button type="button" onClick={() => void flush()} disabled={submitting} className={quietBtn}>
              Save draft
            </button>
          </div>
          <p role="alert" className="m-0 text-[13px] font-bold text-bad empty:hidden">
            {notice}
          </p>
        </section>
      </div>
      <div className="flex min-w-0 flex-col gap-[18px]">
        <div role="status" aria-live="polite" className="flex flex-col gap-2 empty:hidden">
          {state && state !== 'done' && <p className="m-0 text-sm font-bold">{STATE_COPY[state](reviewer)}</p>}
          <Awards awarded={awarded} item={item} />
        </div>
        <ReviewCard reviewer={reviewer} review={review} rubric={item.rubric ?? []} stale={state !== null && state !== 'done' && review !== null} />
        <Mentor>Write for the person reading, not for the grader.</Mentor>
      </div>
    </div>
  );
}
