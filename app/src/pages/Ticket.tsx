import { useEffect, useState, useSyncExternalStore, type KeyboardEvent, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import { ReportDialog } from '../components/ReportDialog';
import { Inline, Markdown } from '../lib/markdown';
import { ME_KEY, type Me } from '../lib/me';
import { openHint, READ_MESSAGES, ReadError, ticketKey, useTicket, type Hint, type ReadFailure, type Ticket, type TicketProgress } from '../lib/read';
import { OFFLINE_WRITE_NOTE, useOffline } from '../lib/offline';
import { useLiveData } from '../lib/stream';
import { CopyCommand } from './Today';

const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-[18px] md:p-5';
const cardTitle = 'm-0 text-base font-extrabold';
const badge = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';
const mono = 'font-mono text-muted';
const chip = 'rounded-md bg-surface2 px-1.5 py-px font-mono text-[13px]';
const ghostBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] border border-ctl bg-surface px-3.5 text-sm font-bold text-fg disabled:opacity-45 md:h-9';
const TICKET_ID = /^[^/]+\/[^/]+$/;

function ago(iso: string | null, now = Date.now()): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return '';
  const min = Math.max(0, Math.round((now - t) / 60_000));
  if (min < 1) return 'just now';
  if (min < 60) return `${min} min ago`;
  const h = Math.round(min / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} days ago`;
}

export function duration(seconds: number): string {
  const min = Math.max(1, Math.round(seconds / 60));
  return min < 60 ? `about ${min} min` : `about ${Math.round(min / 60)} h`;
}

export function shortDate(iso: string): string {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? '' : new Date(t).toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
}

type View = 'new' | 'starting' | 'running' | 'failed' | 'stopped' | 'expired' | 'passed' | 'revisit';

export function viewOf(p: TicketProgress | null): View {
  if (!p) return 'new';
  if (p.state === 'passed') return 'passed';
  if (p.state === 'revisit') return 'revisit';
  if (p.lab_state === 'starting' || p.lab_state === 'failed' || p.lab_state === 'stopped' || p.lab_state === 'expired') return p.lab_state;
  return 'running';
}

const BADGE: Record<View, { label: string; cls: string }> = {
  new: { label: 'Not started', cls: 'bg-surface2 text-fg2' },
  starting: { label: 'Starting lab', cls: 'bg-accentsoft text-accentink' },
  running: { label: 'In progress', cls: 'bg-accentsoft text-accentink' },
  failed: { label: "Lab didn't start", cls: 'bg-surface2 text-bad' },
  stopped: { label: 'Lab stopped', cls: 'bg-surface2 text-fg2' },
  expired: { label: 'Lab expired', cls: 'bg-surface2 text-fg2' },
  passed: { label: 'Passed', cls: 'bg-oksoft text-ok' },
  revisit: { label: 'Passed', cls: 'bg-oksoft text-ok' },
};

/** The small detail after the id in the header, from data the API has. */
function detail(view: View, p: TicketProgress | null): string {
  switch (view) {
    case 'starting':
      return 'lab starting';
    case 'failed':
      return "lab didn't start";
    case 'stopped':
      return 'lab stopped';
    case 'passed':
      return p?.passed_seconds != null ? `passed in ${duration(p.passed_seconds)}` : 'passed';
    case 'revisit':
      return 'practice mode';
    default:
      return '';
  }
}

function Mark({ kind }: { kind: 'ok' | 'bad' | 'warn' }) {
  const cls = kind === 'ok' ? 'bg-ok text-accentfg' : kind === 'bad' ? 'bg-bad text-accentfg' : 'bg-accentsoft text-accentink';
  const glyph = kind === 'ok' ? '✓' : kind === 'bad' ? '✗' : '!';
  const name = kind === 'ok' ? 'Passed' : kind === 'bad' ? 'Not yet' : 'Notice';
  return (
    <span className={`inline-flex size-5 flex-none items-center justify-center rounded-full text-xs font-extrabold ${cls}`}>
      <span aria-hidden="true">{glyph}</span>
      <span className="sr-only">{name}</span>
    </span>
  );
}

function Cmd({ children }: { children: string }) {
  return <code className={chip}>{children}</code>;
}

function Terminal({ output, local }: { output: string; local: boolean }) {
  return (
    <figure className="m-0 overflow-hidden rounded-xl border border-termline bg-term font-mono">
      <div className="flex items-center gap-1.5 bg-termbar px-3 py-[9px]">
        <span className="flex gap-1.5" aria-hidden="true">
          <span className="size-2.5 rounded-full bg-termdot" />
          <span className="size-2.5 rounded-full bg-termdot" />
          <span className="size-2.5 rounded-full bg-termdot" />
        </span>
        <figcaption className="flex-1 truncate text-center text-xs text-termmute">{local ? 'last dojo check · from your laptop' : 'last dojo check'}</figcaption>
      </div>
      <pre tabIndex={0} aria-label="Check output" className="m-0 max-h-72 overflow-auto px-4 py-3.5 text-[13px] leading-[1.75] whitespace-pre-wrap text-termfg">
        {output}
      </pre>
    </figure>
  );
}

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-xl bg-surface2 px-3.5 py-3">
      <dt className="order-2 text-xs text-muted">{label}</dt>
      <dd className="order-1 m-0 text-xl font-extrabold">{value}</dd>
    </div>
  );
}

/** The state card: what the lab is doing and the one next step. */
function LabCard({ ticket, view, progress }: { ticket: Ticket; view: View; progress: TicketProgress | null }) {
  const start = `dojo start ${ticket.id}`;
  const doctor = (
    <p className="m-0 text-[13px] text-muted">
      If the CLI seems stuck, run <Cmd>dojo doctor</Cmd>.
    </p>
  );
  switch (view) {
    case 'new':
      return (
        <section aria-label="Your lab" className={card}>
          <h2 className={cardTitle}>Start this ticket</h2>
          <p className="m-0 text-sm leading-normal text-fg2">Run this in your terminal. It boots your lab and sets up the ticket in ~/dojo.</p>
          <CopyCommand command={start} />
        </section>
      );
    case 'starting':
      return (
        <section aria-label="Your lab" className={card}>
          <h2 className={cardTitle}>Starting your lab</h2>
          <p className="m-0 text-sm leading-normal text-fg2">Your lab is booting. This can take a few minutes the first time on this computer.</p>
        </section>
      );
    case 'failed':
      return (
        <section aria-label="Your lab" className={card}>
          <div className="flex items-center gap-2.5">
            <Mark kind="bad" />
            <h2 className={cardTitle}>The lab didn&apos;t start</h2>
          </div>
          <p className="m-0 text-sm leading-normal text-fg2">
            Run <Cmd>dojo doctor</Cmd> to see what is in the way, then start again.
          </p>
          <CopyCommand command={start} />
          <p className="m-0 text-[13px] text-muted">An online lab is not available yet.</p>
        </section>
      );
    case 'expired':
      return (
        <section aria-label="Your lab" className={card}>
          <div className="flex items-center gap-2.5">
            <Mark kind="warn" />
            <h2 className={cardTitle}>Your lab expired</h2>
          </div>
          <p className="m-0 text-sm leading-normal text-fg2">
            Your lab was marked expired after 7 days without a report from the CLI. Run <Cmd>dojo start</Cmd> again.
          </p>
          <CopyCommand command={start} />
          {doctor}
        </section>
      );
    case 'stopped':
      return (
        <section aria-label="Your lab" className={card}>
          <h2 className={cardTitle}>Your lab is stopped</h2>
          <p className="m-0 text-sm leading-normal text-fg2">Work stays in your repo. Start it again and continue where the ticket is.</p>
          <CopyCommand command={start} />
        </section>
      );
    case 'passed': {
      const p = progress;
      const verified = p?.passed_commit_state === 'verified' && !!p.passed_commit;
      return (
        <section aria-label="Your lab" className={card}>
          <div className="flex items-center justify-between gap-3">
            <h2 className={cardTitle}>Ticket passed</h2>
            {verified && <span className={`${badge} bg-oksoft text-ok`}>signed</span>}
          </div>
          <p className="m-0 text-sm leading-normal text-fg2">
            {verified ? 'Your fix is a signed commit.' : p?.passed_seconds != null ? `You fixed it in ${duration(p.passed_seconds)}.` : 'You fixed it.'}
          </p>
          {(p?.passed_seconds != null || (p?.last_check_passed != null && p.last_check_total != null)) && (
            <dl className="m-0 grid grid-cols-2 gap-2.5">
              {p.passed_seconds != null && <Stat value={duration(p.passed_seconds)} label="time" />}
              {p.last_check_passed != null && p.last_check_total != null && <Stat value={`${p.last_check_passed}/${p.last_check_total}`} label="checks pass" />}
            </dl>
          )}
          {p?.passed_commit && <span className={`${mono} text-xs`}>{p.passed_commit.slice(0, 7)}</span>}
          <Link to="/path" className={`${ghostBtn} self-start no-underline`}>
            Back to the path
          </Link>
        </section>
      );
    }
    case 'revisit': {
      const p = progress;
      const when = p?.first_passed_at ? shortDate(p.first_passed_at) : '';
      return (
        <section aria-label="Your lab" className={card}>
          <h2 className={cardTitle}>Practice mode</h2>
          <p className="m-0 text-sm leading-normal text-fg2">
            {`You passed this ticket${when ? ` on ${when}` : ''}${p?.passed_seconds != null ? ` in ${duration(p.passed_seconds)}` : ''}. Run it again whenever you want; hints are all open in practice.`}
          </p>
          {ticket.hint_count > 0 && (
            <p className="m-0 flex items-center gap-2.5 text-sm text-fg2">
              <Mark kind="ok" />
              {Array.from({ length: ticket.hint_count }, (_, i) => `hint ${i + 1}`).join(' · ')} open
            </p>
          )}
          <CopyCommand command={start} />
          <p className="m-0 text-[13px] text-muted">Running it again restarts the same ticket from the broken state. It doesn&apos;t change your progress.</p>
        </section>
      );
    }
    default:
      return (
        <section aria-label="Your lab" className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <CopyCommand command="dojo check" />
          <p className="m-0 text-[13px] text-muted">
            Fix it on your computer, then run <Cmd>dojo check</Cmd>. Results show here.
          </p>
        </section>
      );
  }
}

function LastCheck({ progress }: { progress: TicketProgress }) {
  if (progress.check_count === 0 || progress.last_check_ok === null) return null;
  const ok = progress.last_check_ok;
  const counts =
    progress.last_check_passed !== null && progress.last_check_total !== null ? `${progress.last_check_passed} of ${progress.last_check_total} checks pass` : '';
  const when = ago(progress.last_check_at);
  return (
    <>
      <section aria-label="Last check" className={card}>
        <div className="flex items-center justify-between gap-3">
          <h2 className={cardTitle}>Last dojo check</h2>
          <span className={`${badge} ${ok ? 'bg-oksoft text-ok' : 'bg-badsoft text-bad'}`}>{ok ? 'Passed' : 'Not yet'}</span>
        </div>
        <p role="status" className="m-0 text-sm text-fg2">
          {[ok ? 'Passed' : 'Not yet', counts, when].filter(Boolean).join(' · ')}
        </p>
      </section>
      {progress.last_check_output && <Terminal output={progress.last_check_output} local={progress.lab === 'local'} />}
    </>
  );
}

const HINT_ERRORS: Partial<Record<'hint_locked' | ReadFailure, string>> = {
  hint_locked: 'Open the earlier hint first.',
  rate_limited: READ_MESSAGES.rate_limited,
  suspended: READ_MESSAGES.suspended,
  failed: 'Could not open the hint. Please try again.',
};

function Hints({ ticket }: { ticket: Ticket }) {
  const offline = useOffline();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const open = new Map(ticket.hints.map((h) => [h.n, h.text]));
  const next = Array.from({ length: ticket.hint_count }, (_, i) => i + 1).find((n) => !open.has(n));
  if (ticket.hint_count === 0) return null;

  async function show(n: number) {
    setBusy(true);
    setError('');
    const res = await openHint(ticket.id, n);
    if (!res.ok && res.reason === 'rate_limited') {
      setTimeout(() => setBusy(false), res.retryAfterMs ?? 1000);
    } else {
      setBusy(false);
    }
    if (res.ok) {
      queryClient.setQueryData<Ticket>(ticketKey(ticket.id), (t) => {
        if (!t) return t;
        const hints: Hint[] = [...t.hints.filter((h) => h.n !== res.hint.n), res.hint].sort((a, b) => a.n - b.n);
        return { ...t, hints };
      });
      return;
    }
    if (res.reason === 'hint_locked') void queryClient.invalidateQueries({ queryKey: ticketKey(ticket.id) });
    setError(HINT_ERRORS[res.reason] ?? HINT_ERRORS.failed ?? '');
  }

  return (
    <section aria-label="Hints" className={card}>
      <div className="flex items-center justify-between gap-3">
        <h2 className={cardTitle}>Hints</h2>
        <span className={`${badge} bg-surface2 font-mono text-fg2`}>
          {open.size} of {ticket.hint_count} open
        </span>
      </div>
      <ol className="m-0 flex list-none flex-col gap-2.5 p-0">
        {Array.from({ length: ticket.hint_count }, (_, i) => i + 1).map((n) => {
          const text = open.get(n);
          return (
            <li key={n}>
              {text !== undefined ? (
                <div className="rounded-xl border border-line bg-surface px-3 py-2.5 text-sm leading-normal text-fg2">
                  <div className="mb-1 font-mono text-xs font-bold text-accentink">hint {n}</div>
                  <Inline text={text} />
                </div>
              ) : (
                <div className="flex items-center justify-between gap-3 rounded-xl border border-dashed border-ctl px-3 py-2.5">
                  <div className="flex flex-col gap-0.5">
                    <span className={`${mono} text-xs font-bold`}>hint {n}</span>
                    <span className="text-[13px] text-muted">{n === next ? 'a nudge, never the command' : `unlocks after hint ${n - 1} · never the command`}</span>
                  </div>
                  {n === next && (
                    <button
                      type="button"
                      aria-label={`Show hint ${n}`}
                      disabled={busy || offline}
                      onClick={() => void show(n)}
                      className="inline-flex h-11 min-w-16 items-center justify-center rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold text-fg disabled:opacity-45 md:h-9"
                    >
                      Show
                    </button>
                  )}
                </div>
              )}
            </li>
          );
        })}
      </ol>
      <p role="status" className="m-0 text-xs font-bold text-bad empty:hidden">
        {error}
      </p>
      <p className="m-0 text-[13px] text-muted">{offline ? OFFLINE_WRITE_NOTE : 'Hints never block you. They are only counted for you.'}</p>
    </section>
  );
}

type Tab = 'ticket' | 'hints' | 'check';
const TABS: { id: Tab; label: string }[] = [
  { id: 'ticket', label: 'Ticket' },
  { id: 'hints', label: 'Hints' },
  { id: 'check', label: 'Check' },
];

/** Phone only: three panels as tabs. Desktop shows every panel at once. */
function Tabs({ tab, setTab, list }: { tab: Tab; setTab: (t: Tab) => void; list: typeof TABS }) {
  function onKey(e: KeyboardEvent<HTMLButtonElement>) {
    const i = list.findIndex((t) => t.id === tab);
    const to = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : null;
    if (to === null) return;
    e.preventDefault();
    const target = list[(to + list.length) % list.length]!.id;
    setTab(target);
    document.getElementById(`tab-${target}`)?.focus();
  }
  return (
    <div role="tablist" aria-label="Ticket sections" className="grid grid-cols-3 gap-1 rounded-xl border border-line bg-surface2 p-1 md:hidden">
      {list.map((t) => (
        <button
          key={t.id}
          id={`tab-${t.id}`}
          type="button"
          role="tab"
          aria-selected={tab === t.id}
          aria-controls={`panel-${t.id}`}
          tabIndex={tab === t.id ? 0 : -1}
          onClick={() => setTab(t.id)}
          onKeyDown={onKey}
          className={`h-11 rounded-[10px] text-[13px] font-bold ${tab === t.id ? 'bg-surface text-fg' : 'text-muted'}`}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

function Panel({ id, tabs, tab, className = '', children }: { id: Tab; tabs: boolean; tab: Tab; className?: string; children: ReactNode }) {
  const mobile = useMobile();
  if (!tabs) return <div className={`${className} empty:hidden`}>{children}</div>;
  const hidden = tab !== id ? 'max-md:hidden' : '';
  // The tablist only exists on a phone: from md up the panels are plain sections.
  if (!mobile) return <div className={`${className} empty:hidden`}>{children}</div>;
  return (
    <div id={`panel-${id}`} role="tabpanel" tabIndex={0} aria-labelledby={`tab-${id}`} className={`${className} ${hidden}`}>
      {children}
    </div>
  );
}

const PHONE = '(max-width: 767.98px)';
const hasMedia = () => typeof window !== 'undefined' && typeof window.matchMedia === 'function';

function subscribePhone(on: () => void): () => void {
  if (!hasMedia()) return () => {};
  const query = window.matchMedia(PHONE);
  query.addEventListener('change', on);
  return () => query.removeEventListener('change', on);
}

const isPhone = () => (hasMedia() ? window.matchMedia(PHONE).matches : true);

function useMobile(): boolean {
  return useSyncExternalStore(subscribePhone, isPhone, isPhone);
}

function TicketTitle({ text }: { text: string }) {
  const words = text.split(' ');
  const head = words.length > 3 ? words.slice(0, -2).join(' ') + ' ' : '';
  const tail = words.length > 3 ? words.slice(-2).join(' ') : text;
  return (
    <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[44px] md:tracking-[-1.5px]">
      {head}
      <span style={{ background: 'var(--hlbg)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' }}>{tail}</span>
    </h1>
  );
}

function Detail({ ticket }: { ticket: Ticket }) {
  const [tab, setTab] = useState<Tab>('ticket');
  const [reporting, setReporting] = useState(false);
  const offline = useOffline();
  const queryClient = useQueryClient();
  const view = viewOf(ticket.progress);
  const p = ticket.progress;
  const b = BADGE[view];
  const tabs = view === 'new' || view === 'running';
  const inline = tabs; // the command row lives in the left column only while the learner works
  const small = detail(view, p);
  const label = view === 'revisit' && p?.first_passed_at ? `Passed ${ago(p.first_passed_at)}` : b.label;
  const col = 'flex min-w-0 flex-col gap-[18px]';
  return (
    <div className="flex flex-col gap-3.5 md:gap-6">
      <Link to="/path" className="inline-flex h-11 items-center gap-1 self-start text-sm font-bold text-fg no-underline md:hidden">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
          <path d="M15 18l-6-6 6-6" />
        </svg>
        Path
      </Link>
      <div className="flex flex-col gap-2.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <span className={`${badge} ${b.cls}`}>{label}</span>
          <span className={`${mono} text-[13px]`}>{[ticket.id, small].filter(Boolean).join(' · ')}</span>
        </div>
        <TicketTitle text={ticket.title} />
      </div>
      {tabs && <Tabs tab={tab} setTab={setTab} list={ticket.hint_count > 0 ? TABS : TABS.filter((t) => t.id !== 'hints')} />}
      <div className="flex flex-col gap-3.5 md:grid md:grid-cols-[1fr_420px] md:items-start md:gap-6">
        <div className={col}>
          <Panel id="ticket" tabs={tabs} tab={tab} className={col}>
            {ticket.brief && (
              <section aria-label="The ticket" className={card}>
                <Markdown source={ticket.brief} />
              </section>
            )}
          </Panel>
          <Panel id="check" tabs={tabs} tab={tab} className={col}>
            {p && <LastCheck progress={p} />}
            {inline && <LabCard ticket={ticket} view={view} progress={p} />}
          </Panel>
        </div>
        <div className="contents md:flex md:min-w-0 md:flex-col md:gap-[18px]">
          {!inline && (
            <div className="max-md:order-first">
              <LabCard ticket={ticket} view={view} progress={p} />
            </div>
          )}
          <Panel id="hints" tabs={tabs} tab={tab}>
            <Hints ticket={ticket} />
          </Panel>
        </div>
      </div>
      <button type="button" disabled={offline} onClick={() => setReporting(true)} className={`${ghostBtn} self-start`}>
        Report a broken ticket
      </button>
      {reporting && (
        <ReportDialog
          kind="broken_ticket"
          itemId={ticket.id}
          onClose={() => setReporting(false)}
          onSignedOut={() => queryClient.setQueryData(ME_KEY, null)}
        />
      )}
    </div>
  );
}

function PlainTitle() {
  return <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px]">Ticket</h1>;
}

function Body({ id }: { id: string }) {
  useLiveData();
  const ticket = useTicket(id);
  const queryClient = useQueryClient();
  const signedOut = ticket.isError && ticket.error instanceof ReadError && ticket.error.reason === 'signed_out';
  useEffect(() => {
    if (signedOut) queryClient.setQueryData(ME_KEY, null);
  }, [signedOut, queryClient]);
  if (signedOut) return null;
  if (ticket.isPending) {
    return (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <p role="status" className="m-0 text-muted">
          Loading the ticket...
        </p>
      </div>
    );
  }
  if (ticket.isError) {
    const reason = ticket.error instanceof ReadError ? ticket.error.reason : 'failed';
    return (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <div role="alert" className="flex flex-col items-start gap-3">
          <p className="m-0 text-bad">{READ_MESSAGES[reason]}</p>
          {reason === 'not_found' ? (
            <Link to="/path" className={ghostBtn}>
              Back to the path
            </Link>
          ) : (
            reason !== 'suspended' && (
              <button type="button" onClick={() => void ticket.refetch()} className={ghostBtn}>
                Retry
              </button>
            )
          )}
        </div>
      </div>
    );
  }
  return <Detail ticket={ticket.data} />;
}

/** L3: one ticket with its brief, lab state, last check and written hints. */
export function TicketPage({ me }: { me: Me }) {
  const id = useParams()['*'] ?? '';
  return (
    <AppFrame me={me}>
      {TICKET_ID.test(id) ? (
        <Body key={id} id={id} />
      ) : (
        <div role="alert" className="flex flex-col items-start gap-3">
          <PlainTitle />
          <p className="m-0 text-bad">{READ_MESSAGES.not_found}</p>
          <Link to="/path" className={ghostBtn}>
            Back to the path
          </Link>
        </div>
      )}
    </AppFrame>
  );
}
