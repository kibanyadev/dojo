import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import { ReplyEditor } from '../components/ReplyEditor';
import { Markdown } from '../lib/markdown';
import { ME_KEY, type Me } from '../lib/me';
import {
  chooseReply,
  itemKey,
  PATH_KEY,
  READ_MESSAGES,
  ReadError,
  TODAY_KEY,
  useItem,
  usePath,
  type Item,
  type PathRole,
  type ReadFailure,
  type StoryChosen,
  type StoryItem,
  type Verdict,
} from '../lib/read';
import { useLiveData } from '../lib/stream';

const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-[18px] md:p-5';
const cardTitle = 'm-0 text-base font-extrabold';
const badge = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';
const ghostBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] border border-ctl bg-surface px-3.5 text-sm font-bold text-fg md:h-9';
const ITEM_ID = /^[^/]+\/[^/]+$/;

const MESSAGES: Record<ReadFailure, string> = { ...READ_MESSAGES, not_found: 'We could not find that moment.' };

const VERDICT: Record<Verdict, { label: string; cls: string }> = {
  good: { label: 'good reply', cls: 'bg-oksoft text-ok' },
  okay: { label: 'okay reply', cls: 'bg-surface2 text-fg2' },
  poor: { label: 'poor reply', cls: 'bg-badsoft text-bad' },
};

function Title({ text }: { text: string }) {
  const words = text.split(' ');
  const head = words.length > 1 ? words.slice(0, -1).join(' ') + ' ' : '';
  const tail = words.length > 1 ? words.at(-1) : text;
  return (
    <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[44px] md:tracking-[-1.5px]">
      {head}
      <span style={{ background: 'var(--hlbg)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' }}>{tail}</span>
    </h1>
  );
}

function PlainTitle() {
  return <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px]">Story</h1>;
}

/** The sender's chat card: avatar, name and role, and the scene as one bubble. */
function Scene({ item }: { item: StoryItem }) {
  const name = item.from?.name ?? '';
  return (
    <section aria-label="The message" className={card}>
      <div className="flex items-center gap-2.5">
        <span
          className="inline-flex size-9 flex-none items-center justify-center rounded-full bg-accentsoft font-bold text-accentink uppercase"
          aria-hidden="true"
        >
          {name.slice(0, 1)}
        </span>
        <div className="flex flex-col">
          <span className="text-sm font-bold">{item.from ? `${item.from.name} · ${item.from.role}` : ''}</span>
          <span className="font-mono text-xs text-muted">chat</span>
        </div>
      </div>
      <div className="rounded-[14px_14px_14px_4px] bg-surface2 px-3.5 py-3">
        <Markdown source={item.scene ?? ''} />
      </div>
    </section>
  );
}

function Review({ chosen }: { chosen: StoryChosen }) {
  const v = VERDICT[chosen.verdict] ?? VERDICT.okay;
  return (
    <section aria-label="Tone review" className={card}>
      <div className="flex items-center justify-between gap-3">
        <h2 className={cardTitle}>Tone review</h2>
        <span className={`${badge} ${v.cls}`}>{v.label}</span>
      </div>
      <Markdown source={chosen.reaction} />
    </section>
  );
}

function Replies({ item }: { item: StoryItem }) {
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<string | null>(null);
  const [notice, setNotice] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const inFlight = useRef(false);
  useEffect(() => () => clearTimeout(timer.current), []);

  const choices = item.choices ?? [];
  const chosenId = item.chosen?.id ?? null;
  const locked = pending !== null || chosenId !== null;

  async function choose(id: string) {
    if (inFlight.current || locked) return;
    inFlight.current = true;
    setPending(id);
    setNotice('');
    const res = await chooseReply(item.id, id);
    if (res.ok) {
      queryClient.setQueryData<Item>(itemKey(item.id), (t) => (t && t.kind === 'story' ? { ...t, done: true, chosen: res.chosen } : t));
      void queryClient.invalidateQueries({ queryKey: TODAY_KEY });
      void queryClient.invalidateQueries({ queryKey: PATH_KEY });
      inFlight.current = false;
      setPending(null);
      return;
    }
    if (res.reason === 'rate_limited') {
      setNotice(MESSAGES.rate_limited);
      timer.current = setTimeout(() => {
        inFlight.current = false;
        setPending(null);
      }, res.retryAfterMs ?? 1000);
      return;
    }
    if (res.reason === 'already_chosen' || res.reason === 'bad_choice') {
      setNotice(
        res.reason === 'already_chosen'
          ? 'You already replied to this message. Showing your reply.'
          : 'That reply is no longer available. Showing the current ones.',
      );
      // Stay locked until the refetch settles, so a stale button cannot post again.
      try {
        await queryClient.invalidateQueries({ queryKey: itemKey(item.id) });
      } finally {
        inFlight.current = false;
        setPending(null);
      }
      return;
    }
    inFlight.current = false;
    setPending(null);
    if (res.reason === 'signed_out') {
      queryClient.setQueryData(ME_KEY, null);
      return;
    }
    setNotice(res.reason === 'suspended' ? MESSAGES.suspended : 'Could not send your reply. Please try again.');
  }

  return (
    <section aria-labelledby="replies-title" className={card}>
      <h2 id="replies-title" className={cardTitle}>
        How do you reply?
      </h2>
      <div role="group" aria-labelledby="replies-title" className="flex flex-col gap-3">
        {choices.map((c) => {
          const mine = c.id === chosenId;
          return (
            <button
              key={c.id}
              type="button"
              aria-pressed={mine}
              aria-disabled={locked}
              onClick={() => void choose(c.id)}
              className={`flex min-h-11 flex-col items-start gap-1 rounded-[10px] border px-3.5 py-3 text-left text-sm leading-normal font-medium text-fg ${
                mine ? 'border-accent bg-accentsoft' : 'border-line bg-surface'
              } ${locked && !mine ? 'opacity-70' : ''}`}
            >
              <span>{c.text}</span>
              {mine && <span className="font-mono text-xs font-bold text-accentink">✓ Your reply</span>}
            </button>
          );
        })}
      </div>
      {chosenId === null && <p className="m-0 text-[13px] text-muted">Your reply is final once you choose it.</p>}
      <p role="status" className="m-0 text-[13px] font-bold text-bad empty:hidden">
        {notice}
      </p>
    </section>
  );
}

function Mark({ done, current }: { done: boolean; current: boolean }) {
  if (done) {
    return (
      <span className="inline-flex size-5 flex-none items-center justify-center rounded-full bg-ok text-xs font-extrabold text-accentfg">
        <span aria-hidden="true">✓</span>
        <span className="sr-only">Done</span>
      </span>
    );
  }
  if (current) {
    return (
      <span className="inline-flex size-5 flex-none items-center justify-center rounded-full bg-accentsoft text-xs font-extrabold text-accentink">
        <span aria-hidden="true">!</span>
        <span className="sr-only">You are here</span>
      </span>
    );
  }
  return (
    <span className="inline-flex size-5 flex-none items-center justify-center rounded-full border-2 border-muted">
      <span className="sr-only">Not started yet</span>
    </span>
  );
}

/** The role's progress: tickets and between items in order, from GET /v1/path. */
function RoleProgress({ role, currentId }: { role: PathRole; currentId: string }) {
  const rows = [
    ...role.items.map((i) => ({ id: i.id, title: i.title, order: i.order, done: i.state === 'passed' || i.state === 'revisit' })),
    ...(role.between_items ?? []).map((i) => ({ id: i.id, title: i.title, order: i.order, done: i.done })),
  ].sort((a, b) => a.order - b.order);
  return (
    <section aria-label={role.title} className={`${card} max-md:hidden`}>
      <h2 className={cardTitle}>{role.title}</h2>
      <ol className="m-0 flex list-none flex-col gap-3 p-0">
        {rows.map((r, i) => (
          <li key={r.id} aria-current={r.id === currentId ? 'step' : undefined} className="flex items-start gap-2.5 text-sm leading-[1.45]">
            <Mark done={r.done} current={r.id === currentId} />
            <span className={r.id === currentId ? 'font-bold' : ''}>
              {i + 1} · {r.title}
            </span>
          </li>
        ))}
      </ol>
    </section>
  );
}

function Detail({ item }: { item: StoryItem }) {
  const path = usePath();
  const role = path.data?.roles.find((r) => r.slug === item.role) ?? null;
  const moments = role?.between_items?.filter((i) => i.kind === 'story').sort((a, b) => a.order - b.order) ?? [];
  const at = moments.findIndex((m) => m.id === item.id);
  const where = role ? (item.kind === 'story' && at >= 0 ? `${role.title} · moment ${at + 1} of ${moments.length}` : role.title) : item.id;
  return (
    <div className="flex flex-col gap-3.5 md:gap-6">
      <Link to="/path" className="inline-flex h-11 items-center gap-1 self-start text-sm font-bold text-fg no-underline md:hidden">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
          <path d="M15 18l-6-6 6-6" />
        </svg>
        Path
      </Link>
      <div className="flex flex-col gap-2">
        <span className="font-mono text-xs text-muted">{where}</span>
        <Title text={item.title} />
      </div>
      {item.kind === 'story' ? (
        <div className="flex flex-col gap-3.5 md:grid md:grid-cols-[1fr_380px] md:items-start md:gap-6">
          <div className="flex min-w-0 flex-col gap-[18px]">
            <Scene item={item} />
            <Replies item={item} />
            <div role="status" aria-live="polite" className="empty:hidden">
              {item.chosen && <Review chosen={item.chosen} />}
            </div>
          </div>
          {role && (
            <div className="flex min-w-0 flex-col gap-[18px]">
              <RoleProgress role={role} currentId={item.id} />
            </div>
          )}
        </div>
      ) : (
        <Feedback item={item} />
      )}
    </div>
  );
}

/** The feedback round: the message from `from`, then the reply box and its review, as the writing page. */
function Feedback({ item }: { item: StoryItem }) {
  useLiveData();
  return <ReplyEditor key={item.id} item={item} lead={<Scene item={item} />} />;
}

function Body({ id }: { id: string }) {
  const item = useItem(id);
  const queryClient = useQueryClient();
  const signedOut = item.isError && item.error instanceof ReadError && item.error.reason === 'signed_out';
  useEffect(() => {
    if (signedOut) queryClient.setQueryData(ME_KEY, null);
  }, [signedOut, queryClient]);
  if (signedOut) return null;
  if (item.isPending) {
    return (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <p role="status" className="m-0 text-muted">
          Loading the message...
        </p>
      </div>
    );
  }
  if (item.isError || (item.data.kind !== 'story' && item.data.kind !== 'feedback')) {
    const reason: ReadFailure = item.isError ? (item.error instanceof ReadError ? item.error.reason : 'failed') : 'not_found';
    return (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <div role="alert" className="flex flex-col items-start gap-3">
          <p className="m-0 text-bad">{MESSAGES[reason]}</p>
          {reason === 'not_found' ? (
            <Link to="/path" className={`${ghostBtn} no-underline`}>
              Back to the path
            </Link>
          ) : (
            reason !== 'suspended' && (
              <button type="button" onClick={() => void item.refetch()} className={ghostBtn}>
                Retry
              </button>
            )
          )}
        </div>
      </div>
    );
  }
  return <Detail item={item.data} />;
}

/** L4: one story moment, or a feedback round with its reply box. */
export function StoryPage({ me }: { me: Me }) {
  const id = useParams()['*'] ?? '';
  return (
    <AppFrame me={me}>
      {ITEM_ID.test(id) ? (
        <Body key={id} id={id} />
      ) : (
        <div role="alert" className="flex flex-col items-start gap-3">
          <PlainTitle />
          <p className="m-0 text-bad">{MESSAGES.not_found}</p>
          <Link to="/path" className={`${ghostBtn} no-underline`}>
            Back to the path
          </Link>
        </div>
      )}
    </AppFrame>
  );
}
