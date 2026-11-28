import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import { badge, card, cardTitle, ghostBtn, ReplyEditor } from '../components/ReplyEditor';
import { Markdown } from '../lib/markdown';
import { ME_KEY, type Me } from '../lib/me';
import { READ_MESSAGES, ReadError, useItem, type ReadFailure, type WritingItem } from '../lib/read';
import { useLiveData } from '../lib/stream';

export { AUTOSAVE_IDLE_MS, AUTOSAVE_MIN_GAP_MS, MAX_CHARS } from '../components/ReplyEditor';

const ITEM_ID = /^[^/]+\/[^/]+$/;

const MESSAGES: Record<ReadFailure, string> = { ...READ_MESSAGES, not_found: 'We could not find that task.' };

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
  return <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px]">Writing task</h1>;
}

function BackLink() {
  return (
    <Link to="/path" className="inline-flex h-11 items-center gap-1 self-start text-sm font-bold text-fg no-underline md:hidden">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
        <path d="M15 18l-6-6 6-6" />
      </svg>
      Path
    </Link>
  );
}

function Detail({ item }: { item: WritingItem }) {
  return (
    <div className="flex flex-col gap-3.5 md:gap-6">
      <BackLink />
      <div className="flex flex-col gap-2.5">
        <div className="flex items-center gap-2.5">
          <span className={`${badge} bg-accentsoft text-accentink`}>Writing task</span>
          <span className="font-mono text-[13px] text-muted">{item.id}</span>
        </div>
        <Title text={item.title} />
      </div>
      <ReplyEditor
        key={item.id}
        item={item}
        lead={
          <section aria-label="The task" className={card}>
            <h2 className={cardTitle}>The task</h2>
            <Markdown source={item.prompt} />
          </section>
        }
      />
    </div>
  );
}

function Body({ id }: { id: string }) {
  useLiveData();
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
          Loading the task...
        </p>
      </div>
    );
  }
  if (item.isError || item.data.kind !== 'writing') {
    const reason: ReadFailure = item.isError ? (item.error instanceof ReadError ? item.error.reason : 'failed') : 'not_found';
    return (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <div role="alert" className="flex flex-col items-start gap-3">
          <p className="m-0 text-bad">{MESSAGES[reason]}</p>
          {reason === 'not_found' ? (
            <Link to="/path" className={ghostBtn}>
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

/** L9: one writing task. Autosaves a draft, asks for a review and shows it. */
export function WritingPage({ me }: { me: Me }) {
  const id = useParams()['*'] ?? '';
  return (
    <AppFrame me={me}>
      {ITEM_ID.test(id) ? (
        <Body key={id} id={id} />
      ) : (
        <div role="alert" className="flex flex-col items-start gap-3">
          <PlainTitle />
          <p className="m-0 text-bad">{MESSAGES.not_found}</p>
          <Link to="/path" className={ghostBtn}>
            Back to the path
          </Link>
        </div>
      )}
    </AppFrame>
  );
}
