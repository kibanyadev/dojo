import { useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import { Inline, Markdown } from '../lib/markdown';
import { ME_KEY, type Me } from '../lib/me';
import {
  itemKey,
  postAnswer,
  READ_MESSAGES,
  ReadError,
  usePath,
  useItem,
  type AnswerResult,
  type Item,
  type QuizItem,
  type QuizQuestion,
  type ReadFailure,
} from '../lib/read';
import { useLiveData } from '../lib/stream';

const QUIZ_ID = /^[^/]+\/[^/]+$/;

const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5';
const cardTitle = 'm-0 text-base font-extrabold';
const body = 'm-0 text-sm leading-[1.55] text-fg2';
const ghostBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] border border-line bg-surface px-[18px] text-[15px] font-bold text-fg no-underline';
const primaryBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] bg-accent px-[18px] text-[15px] font-bold text-accentfg no-underline disabled:opacity-60';
const gradient = { background: 'var(--hlbg)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' } as const;

/** Folds a 200 of the answers route into the cached quiz: the first answer is kept, the last one moves. */
export function applyAnswer(item: QuizItem, questionId: string, choice: number, r: AnswerResult): QuizItem {
  return {
    ...item,
    done: r.done,
    answered: r.answered,
    total: r.total,
    first_try_correct: r.first_try_correct,
    questions: item.questions.map((q) => {
      if (q.id !== questionId) return q;
      const prev = q.answer_state;
      return {
        ...q,
        answer_state: {
          first_choice: prev?.first_choice ?? choice,
          first_correct: prev?.first_correct ?? r.correct,
          last_choice: choice,
          last_correct: r.correct,
          answer: r.answer,
          notes: r.notes,
          explain: r.explain,
        },
      };
    }),
  };
}

const unanswered = (item: QuizItem) => item.questions.filter((q) => !q.answer_state).map((q) => q.id);
const missed = (item: QuizItem) => item.questions.filter((q) => q.answer_state && !q.answer_state.last_correct);

function Progress({ segments, label }: { segments: ('on' | 'off' | 'ok' | 'bad')[]; label: string }) {
  const cls = { on: 'bg-accent', off: 'bg-line', ok: 'bg-ok', bad: 'bg-bad' };
  return (
    <div role="img" aria-label={label} className="flex gap-1">
      {segments.map((s, i) => (
        <span key={i} className={`h-1.5 flex-1 rounded-full ${cls[s]}`} />
      ))}
    </div>
  );
}

function Meta({ item }: { item: QuizItem }) {
  const path = usePath();
  const roleTitle = path.data?.roles.find((r) => r.slug === item.role)?.title ?? item.role;
  const slug = item.id.split('/').at(-1);
  return <span className="font-mono text-[13px] text-muted">{[roleTitle, slug, item.title].filter(Boolean).join(' · ')}</span>;
}

function Heading({ children, refEl }: { children: ReactNode; refEl: RefObject<HTMLHeadingElement | null> }) {
  return (
    <h1 ref={refEl} tabIndex={-1} className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] outline-none md:text-[46px] md:tracking-[-1.5px]">
      {children}
    </h1>
  );
}

interface Feedback {
  qid: string;
  choice: number;
  result: AnswerResult;
  /** A first answer that was wrong: the question comes back later in the run. */
  returns: boolean;
}

function Choices({ q, picked, setPicked, feedback }: { q: QuizQuestion; picked: number | null; setPicked: (n: number) => void; feedback: Feedback | null }) {
  const labelId = `q-${q.id}`;
  return (
    <div className={card}>
      <div id={labelId} className="text-lg leading-[1.35] font-extrabold text-fg [&>div]:text-lg [&>div]:font-extrabold [&>div]:text-fg">
        <Markdown source={q.text} />
      </div>
      <div role="radiogroup" aria-labelledby={labelId} className="flex flex-col gap-2">
        {q.choices.map((text, i) => {
          const answered = feedback !== null;
          const isPick = (answered ? feedback.choice : picked) === i;
          const isRight = answered && feedback.result.answer === i;
          const wrongPick = answered && isPick && !isRight;
          const tone = isRight ? 'border-2 border-ok bg-oksoft' : wrongPick ? 'border-2 border-bad bg-badsoft' : isPick ? 'border-2 border-accent bg-accentsoft' : 'border border-ctl bg-surface';
          const note = answered ? feedback.result.notes[i] : undefined;
          return (
            <label key={i} className={`flex min-h-11 items-center gap-3 rounded-xl px-3.5 py-3 ${tone} ${answered ? '' : 'cursor-pointer'}`}>
              <input
                type="radio"
                name={labelId}
                checked={isPick}
                disabled={answered}
                onChange={() => setPicked(i)}
                className="size-[18px] flex-none accent-accentink"
              />
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="font-mono text-sm font-semibold">
                  <Inline text={text} />
                </span>
                {note && (
                  <span className="text-[13px] text-muted">
                    <Inline text={note} />
                  </span>
                )}
              </span>
              {isRight && (
                <span className="ml-auto text-[13px] font-extrabold whitespace-nowrap text-ok">
                  <span aria-hidden="true">✓ </span>correct
                </span>
              )}
              {wrongPick && (
                <span className="ml-auto text-[13px] font-extrabold whitespace-nowrap text-bad">
                  <span aria-hidden="true">✗ </span>yours<span className="sr-only"> (not correct)</span>
                </span>
              )}
            </label>
          );
        })}
      </div>
    </div>
  );
}

function errorText(reason: ReadFailure, retryAfterMs: number): string {
  if (reason === 'rate_limited') return `Too many requests. Try again in ${Math.max(1, Math.ceil(retryAfterMs / 1000))} s.`;
  if (reason === 'not_found' || reason === 'failed') return 'Could not check that answer. Please try again.';
  return READ_MESSAGES[reason];
}

function Done({ item, onRetry, headingRef }: { item: QuizItem; onRetry: () => void; headingRef: RefObject<HTMLHeadingElement | null> }) {
  const again = missed(item);
  const rows = item.questions.map((q, i) => ({ q, n: i + 1 }));
  return (
    <>
      <div className="flex flex-col gap-3">
        <Meta item={item} />
        <Heading refEl={headingRef}>
          Quiz done. <span style={gradient}>{item.first_try_correct} of {item.total}</span>
          <span className="-ml-[0.12em]">.</span>
        </Heading>
        <Progress
          label={`${item.first_try_correct} of ${item.total} correct on the first try`}
          segments={item.questions.map((q) => (q.answer_state?.first_correct ? 'ok' : 'bad'))}
        />
      </div>
      {rows
        .filter(({ q }) => q.answer_state && !q.answer_state.last_correct)
        .map(({ q, n }) => (
          <section key={q.id} aria-label={`Question ${n}, look again`} className={card}>
            <div className="flex items-center justify-between gap-3">
              <h2 className={cardTitle}>The one to look at again</h2>
              <span className="inline-flex h-[26px] flex-none items-center rounded-md bg-badsoft px-2.5 font-mono text-xs font-bold whitespace-nowrap text-bad">question {n}</span>
            </div>
            <p className={body}>
              <Inline text={q.text} />
              {q.answer_state && (
                <>
                  {' '}
                  <Inline text={q.choices[q.answer_state.answer] ?? ''} />
                </>
              )}
            </p>
            {q.answer_state?.explain && <Markdown source={q.answer_state.explain} />}
          </section>
        ))}
      {item.first_try_correct > 0 && (
        <section aria-label="Solid" className={card}>
          <h2 className={cardTitle}>Solid</h2>
          <p className={body}>
            {item.first_try_correct} of {item.total} questions right on the first try.
          </p>
        </section>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        {again.length > 0 ? (
          <button type="button" onClick={onRetry} className={ghostBtn}>
            {again.length === 1 ? 'Retry the one you missed' : `Retry the ${again.length} you missed`}
          </button>
        ) : (
          <span />
        )}
        <Link to="/path" className={primaryBtn}>
          Back to the path
        </Link>
      </div>
    </>
  );
}

function Run({ item }: { item: QuizItem }) {
  const queryClient = useQueryClient();
  const [qid, setQid] = useState<string | null>(() => unanswered(item)[0] ?? null);
  const [queue, setQueue] = useState<string[]>(() => unanswered(item).slice(1));
  const [picked, setPicked] = useState<number | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Bumped on every new presentation, so a missed question shown again still takes focus.
  const [step, setStep] = useState(0);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const nextRef = useRef<HTMLButtonElement>(null);
  const first = useRef(true);

  // After the first render, a new question or the done screen takes focus, so the keyboard continues where the eye does.
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    headingRef.current?.focus();
  }, [qid, step]);
  useEffect(() => {
    if (feedback) nextRef.current?.focus();
  }, [feedback]);

  const q = item.questions.find((x) => x.id === qid);
  if (!q) return <Done item={item} headingRef={headingRef} onRetry={retry} />;

  function retry() {
    const ids = missed(item).map((x) => x.id);
    setQid(ids[0] ?? null);
    setStep((s) => s + 1);
    setQueue(ids.slice(1));
    setPicked(null);
    setFeedback(null);
    setProblem(null);
  }

  async function check() {
    if (!q || busy) return;
    if (picked === null) {
      setProblem('Pick an answer first.');
      return;
    }
    setBusy(true);
    setProblem(null);
    const out = await postAnswer(item.id, q.id, picked);
    setBusy(false);
    if (!out.ok) {
      if (out.reason === 'signed_out') queryClient.setQueryData(ME_KEY, null);
      else setProblem(errorText(out.reason, out.retryAfterMs ?? 0));
      return;
    }
    const returns = !q.answer_state && !out.result.correct;
    queryClient.setQueryData<Item>(itemKey(item.id), (cur) => (cur && cur.kind === 'quiz' ? applyAnswer(cur, q.id, picked, out.result) : cur));
    if (returns) setQueue((cur) => [...cur, q.id]);
    setFeedback({ qid: q.id, choice: picked, result: out.result, returns });
  }

  function next() {
    const [head, ...rest] = queue;
    setQid(head ?? null);
    setStep((s) => s + 1);
    setQueue(rest);
    setPicked(null);
    setFeedback(null);
    setProblem(null);
  }

  const n = item.questions.indexOf(q) + 1;
  const right = feedback?.result.correct ?? false;
  const last = queue.length === 0;
  return (
    <>
      <div className="flex flex-col gap-3">
        <Meta item={item} />
        <Heading refEl={headingRef}>
          Question {n} <span style={gradient}>of {item.total}</span>
        </Heading>
        <Progress
          label={`Progress: question ${n} of ${item.total}`}
          segments={item.questions.map((x) => (x.answer_state ? 'on' : 'off'))}
        />
      </div>
      <Choices q={q} picked={picked} setPicked={(i) => { setPicked(i); setProblem(null); }} feedback={feedback} />
      <div id="quiz-feedback" role="status" aria-live="polite" className="empty:hidden">
        {feedback && (
          <section aria-label="Answer feedback" className={`${card} ${right ? 'border-ok' : 'border-bad'}`}>
            <h2 className={cardTitle}>{right ? 'Correct' : 'Not quite'}</h2>
            {feedback.result.explain && <Markdown source={feedback.result.explain} />}
            {feedback.returns && <p className={body}>This question comes back later in the quiz.</p>}
            <div className="flex justify-end">
              <button ref={nextRef} type="button" onClick={next} className={primaryBtn}>
                {last ? 'See your result' : 'Next question'}
              </button>
            </div>
          </section>
        )}
      </div>
      {problem && (
        <p role="alert" className="m-0 text-sm font-semibold text-bad">
          {problem}
        </p>
      )}
      {!feedback && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className={`${body} min-w-0 flex-1`}>Quizzes check the ideas; tickets check your hands. A wrong answer never blocks you.</p>
          <button type="button" onClick={() => void check()} disabled={busy} className={primaryBtn}>
            {busy ? 'Checking...' : 'Check answer'}
          </button>
        </div>
      )}
    </>
  );
}

function Notice({ message, action }: { message: string; action: ReactNode }) {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[46px]">Quiz</h1>
      <div role="alert" className="flex flex-col items-start gap-3">
        <p className="m-0 text-bad">{message}</p>
        {action}
      </div>
    </div>
  );
}

const backToPath = (
  <Link to="/path" className={ghostBtn}>
    Back to the path
  </Link>
);

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
        <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[46px]">Quiz</h1>
        <p role="status" className="m-0 text-muted">
          Loading the quiz...
        </p>
      </div>
    );
  }
  if (item.isError) {
    const reason = item.error instanceof ReadError ? item.error.reason : 'failed';
    if (reason === 'not_found') return <Notice message="We could not find that quiz." action={backToPath} />;
    return (
      <Notice
        message={READ_MESSAGES[reason]}
        action={
          reason !== 'suspended' && (
            <button type="button" onClick={() => void item.refetch()} className={ghostBtn}>
              Retry
            </button>
          )
        }
      />
    );
  }
  if (item.data.kind !== 'quiz') return <Notice message="We could not find that quiz." action={backToPath} />;
  return <Run key={item.data.id} item={item.data} />;
}

/** L11: one quiz, a question at a time, then the result. Never blocks; a missed question comes back. */
export function QuizPage({ me }: { me: Me }) {
  const id = useParams()['*'] ?? '';
  return (
    <AppFrame me={me}>
      <div className="mx-auto flex max-w-[760px] flex-col gap-4 md:gap-5">
        <Link to="/path" className="inline-flex h-11 items-center gap-1 self-start text-sm font-bold text-fg no-underline md:hidden">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
            <path d="M15 18l-6-6 6-6" />
          </svg>
          Path
        </Link>
        {QUIZ_ID.test(id) ? <Body key={id} id={id} /> : <Notice message="We could not find that quiz." action={backToPath} />}
      </div>
    </AppFrame>
  );
}
