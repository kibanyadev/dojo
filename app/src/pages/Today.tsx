import { useEffect, useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import { EmailPrompt } from '../components/EmailPrompt';
import { ME_KEY, type Me } from '../lib/me';
import {
  fetchTicket,
  READ_MESSAGES,
  ReadError,
  ticketKey,
  useActivity,
  useToday,
  TODAY_KEY,
  type Activity,
  type FirstRun,
  type Ticket,
  type Today as TodayData,
  type TodayBetween,
  type TodayTicket,
} from '../lib/read';
import { patchMe } from '../lib/settings';
import { useLiveData } from '../lib/stream';
import { ago, BETWEEN_LABEL, buildHeatGrid, capitalize, heatSummary, initial, itemHref, parseBrief, shortCommit, type BriefMessage } from '../lib/today';

const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5';
const badge = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';
const primaryBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] bg-accent px-[18px] text-[15px] font-bold text-accentfg no-underline';
const ghostBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] px-[18px] text-[15px] font-bold text-fg2 no-underline hover:text-fg';
const outlineBtn =
  'inline-flex h-11 items-center justify-center rounded-[10px] border border-line bg-surface px-[18px] text-[15px] font-bold text-fg no-underline';
const mono = 'font-mono text-muted';
const grid2 = 'grid items-start gap-6 md:grid-cols-[1fr_340px] md:gap-7';

/** Ticket ids hold a slash (`support-l1/perms-03`); each segment is escaped, the slash stays. */
export const ticketHref = (id: string) => `/t/${id.split('/').map(encodeURIComponent).join('/')}`;

const shortId = (id: string) => id.slice(id.indexOf('/') + 1);

/** The title with its last two words picked out by the accent gradient, as the board does. */
export function Headline({ text }: { text: string }) {
  const words = text.split(' ');
  const head = words.length > 3 ? words.slice(0, -2).join(' ') + ' ' : '';
  const tail = words.length > 3 ? words.slice(-2).join(' ') : text;
  return (
    <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px] md:leading-[1.05] md:tracking-[-1.4px]">
      {head}
      <span style={{ background: 'var(--hlbg)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' }}>{tail}</span>
    </h1>
  );
}

export function CopyCommand({ command }: { command: string }) {
  const [notice, setNotice] = useState('');
  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setNotice('Copied');
    } catch {
      setNotice('Copy failed, select the command instead');
    }
  }
  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        onClick={() => void copy()}
        aria-label={`Copy command: ${command}`}
        className="flex h-11 items-center gap-2.5 rounded-[10px] border border-ctl bg-surface pr-2 pl-3.5 font-mono text-sm font-semibold text-fg"
      >
        <span className="text-muted">$</span>
        {command}
        <span className="ml-auto inline-flex size-[30px] items-center justify-center rounded-lg bg-surface2 text-fg2" aria-hidden="true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <rect x="9" y="9" width="12" height="12" rx="2" />
            <path d="M5 15V5a2 2 0 0 1 2-2h10" />
          </svg>
        </span>
      </button>
      <p role="status" className="m-0 text-xs font-bold text-fg2 empty:hidden">
        {notice}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------- data

// Reads of one ticket, on the same cache keys and rules as the ticket page; a failure just leaves the section out.
const ticketQuery = (id: string) => ({
  queryKey: ticketKey(id),
  queryFn: () => fetchTicket(id),
  retry: (count: number, err: Error) => !(err instanceof ReadError && err.reason !== 'failed') && count < 1,
  staleTime: Infinity,
});

function useTicketIf(id: string | null) {
  return useQuery({ ...ticketQuery(id ?? ''), enabled: id !== null }).data ?? null;
}

/** The latest pass, from the one activity read; null while it loads, when it failed or when nothing is passed. */
function lastPass(activity: Activity | undefined) {
  return activity?.recent[0] ?? null;
}

// ---------------------------------------------------------------- pieces

function RoleProgress({ role }: { role: NonNullable<TodayData['role']> }) {
  const { tickets_passed: passed, tickets_total: total } = role;
  return (
    <div
      role="progressbar"
      aria-label={`${role.title}, ${passed} of ${total} tickets`}
      aria-valuenow={passed}
      aria-valuemin={0}
      aria-valuemax={total}
      className="h-2.5 overflow-hidden rounded-full bg-surface2"
    >
      <span className="block h-full bg-accent" style={{ width: `${total > 0 ? Math.min(100, (passed / total) * 100) : 0}%` }} />
    </div>
  );
}

const ticketsToGo = (role: NonNullable<TodayData['role']>) => {
  const left = role.tickets_total - role.tickets_passed;
  return left <= 0 ? 'Every ticket is passed.' : `${left} ${left === 1 ? 'ticket' : 'tickets'} to go.`;
};

/** Desktop role panel: progress, then the belt, which has no served state yet and so claims no mark. */
function RoleCard({ role }: { role: NonNullable<TodayData['role']> }) {
  return (
    <section aria-label={role.title} className="flex flex-col gap-3.5 rounded-[20px] border border-line bg-surface p-5 md:p-[22px]">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="m-0 text-xl font-extrabold md:text-[22px]">{role.title}</h2>
        <span className={`${mono} text-[13px]`}>
          {role.tickets_passed} of {role.tickets_total} tickets
        </span>
      </div>
      <RoleProgress role={role} />
      <p className="m-0 text-sm leading-normal text-fg2">{ticketsToGo(role)}</p>
      <div className="flex flex-col gap-2 border-t border-line pt-3">
        <h3 className="m-0 text-sm font-extrabold">Your belt</h3>
        <div aria-hidden="true" className="box-border h-[18px] w-40 rounded border border-dashed border-ctl bg-surface" />
        <p className="m-0 text-[13px] leading-normal text-muted">Marks come from how you work, not from tickets.</p>
      </div>
    </section>
  );
}

/** Phone role strip on top of the page. */
function RoleStrip({ role }: { role: NonNullable<TodayData['role']> }) {
  return (
    <div className="flex flex-col gap-2.5 rounded-2xl border border-line bg-surface px-4 py-3.5 md:hidden">
      <div className="flex justify-between gap-3 text-sm">
        <span className="font-extrabold">{role.title}</span>
        <span className="font-mono text-muted">
          {role.tickets_passed} of {role.tickets_total} tickets
        </span>
      </div>
      <RoleProgress role={role} />
    </div>
  );
}

const panel = 'hidden flex-col gap-3.5 rounded-[20px] border border-line bg-surface p-[22px] md:flex';
const HEAT = ['bg-heat0', 'bg-heat1', 'bg-heat2', 'bg-heat3', 'bg-heat4'] as const;

/** First run only: the board's dashed note. "One so far" needs exactly one pass in total. */
function ActivityNote({ activity }: { activity: Activity }) {
  const one = activity.total === 1 ? (activity.recent[0] ?? null) : null;
  return (
    <section aria-label="Activity" className={panel}>
      <h2 className="m-0 text-base font-extrabold">Activity</h2>
      <p className="m-0 rounded-xl border border-dashed border-ctl p-4 text-[13px] leading-normal text-muted">
        Each ticket you pass shows up here.
        {one && (
          <>
            {' '}
            One so far: {one.commit && <span className="font-mono text-accentink">{shortCommit(one.commit)} </span>}
            {one.ticket_id}
          </>
        )}
      </p>
    </section>
  );
}

/** The returning learner's heat grid and last passes. Draws nothing for a learner with no passes. */
function ActivityPanel({ activity }: { activity: Activity }) {
  if (activity.total === 0) return null;
  const grid = buildHeatGrid(activity.to, activity.days);
  return (
    <section aria-label="Activity" className={panel}>
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="m-0 text-base font-extrabold">Activity</h2>
        <span className="font-mono text-xs text-muted">last 20 weeks</span>
      </div>
      <div role="img" aria-label={heatSummary(grid)} className="grid grid-flow-col grid-rows-[repeat(7,10px)] gap-[3px]">
        {grid.weeks.flatMap((week) =>
          week.map((c) => (
            <div key={c.day} className={`size-2.5 rounded-xs ${c.future ? '' : HEAT[c.level]}`} />
          )),
        )}
      </div>
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0 text-[13px]">
        {activity.recent.map((r) => (
          <li key={r.ticket_id} className="flex min-w-0 items-baseline gap-2 text-fg2">
            {r.commit && <span className="flex-none font-mono text-xs text-accentink">{shortCommit(r.commit)}</span>}
            <span className="min-w-0 flex-1 truncate">{r.title}</span>
            <span className="flex-none font-mono text-xs text-muted">{ago(r.at)}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Holds the panel's space while the read is out, so nothing below it moves. */
function ActivityPlaceholder() {
  return (
    <section aria-hidden="true" className={`${panel} min-h-[216px]`}>
      <div className="h-5 w-24 rounded-md bg-surface2" />
      <div className="h-[88px] rounded-md bg-surface2" />
      <div className="h-[66px] rounded-md bg-surface2" />
    </section>
  );
}

/** The aside's activity slot on the returning view: placeholder, panel, or nothing when the read failed. */
function ActivitySlot({ query }: { query: ReturnType<typeof useActivity> }) {
  if (query.isPending) return <ActivityPlaceholder />;
  return query.data ? <ActivityPanel activity={query.data} /> : null;
}

function Message({ brief }: { brief: BriefMessage }) {
  return (
    <section aria-label={`Message from ${brief.sender}`} className={`${card} gap-2.5 p-[18px]`}>
      <div className="flex items-center gap-2.5">
        <span
          className="inline-flex size-[34px] flex-none items-center justify-center rounded-full bg-storybg font-bold text-storyink"
          aria-hidden="true"
        >
          {initial(brief.sender)}
        </span>
        <div className="flex min-w-0 flex-col">
          <h2 className="m-0 text-sm font-bold">{brief.team ? `${brief.sender} · ${capitalize(brief.team)}` : brief.sender}</h2>
          <span className="font-mono text-xs text-muted">{brief.ref}</span>
        </div>
      </div>
      <p className="m-0 text-[15px] leading-[1.55]">{brief.text}</p>
    </section>
  );
}

function LabCard({ current, ticket }: { current: TodayTicket; ticket: Ticket | null }) {
  const p = ticket?.progress ?? null;
  const checked = p && p.check_count > 0 && p.last_check_at;
  return (
    <section aria-label="Your lab" className={`${card} hidden h-full gap-3 p-[18px] md:flex`}>
      <div className="flex items-center justify-between gap-3">
        <h2 className="m-0 text-[15px] font-extrabold">Your lab</h2>
        {current.lab_state && <span className={`${badge} bg-surface2 font-mono text-fg2`}>{current.lab_state}</span>}
      </div>
      <p className="m-0 text-sm leading-normal text-fg2">
        {checked ? (
          <>
            Last <code className="font-mono text-[13px]">dojo check</code> {ago(p.last_check_at)}
            {p.last_check_passed !== null && p.last_check_total !== null ? (
              <>
                : <b>{p.last_check_passed} of {p.last_check_total} checks pass</b>.
              </>
            ) : (
              '.'
            )}
          </>
        ) : (
          <>
            Fix it on your computer, then run <code className="rounded-md bg-surface2 px-1.5 py-px font-mono text-[13px]">dojo check</code>.
          </>
        )}
      </p>
      <div className="mt-auto flex flex-col gap-2.5">
        <Link to={ticketHref(current.id)} className={primaryBtn}>
          Continue the ticket
        </Link>
        <CopyCommand command="dojo check" />
      </div>
    </section>
  );
}

/** Phone: the last check as a short card, then the one next step. */
function PhoneCheck({ current, ticket }: { current: TodayTicket; ticket: Ticket | null }) {
  const p = ticket?.progress ?? null;
  const checked = p && p.check_count > 0 && p.last_check_ok !== null;
  return (
    <div className="flex flex-col gap-4 md:hidden">
      {checked && (
        <section aria-label="Last check" className="overflow-hidden rounded-2xl border border-line bg-surface">
          <div className="flex items-center justify-between gap-3 border-b border-line px-[18px] py-3.5">
            <h2 className="m-0 text-[15px] font-extrabold">Last check</h2>
            {p.last_check_passed !== null && p.last_check_total !== null && (
              <span className={`${badge} font-mono ${p.last_check_ok ? 'bg-oksoft text-ok' : 'bg-badsoft text-bad'}`}>
                {p.last_check_passed}/{p.last_check_total}
              </span>
            )}
          </div>
          <div className="flex flex-col gap-2.5 px-[18px] py-3.5 font-mono text-[13px] text-fg2">
            <div className="text-xs text-muted">{ago(p.last_check_at)}</div>
            {p.last_check_output && (
              <pre tabIndex={0} aria-label="Check output" className="m-0 max-h-60 overflow-auto font-mono text-[13px] whitespace-pre-wrap">
                {p.last_check_output}
              </pre>
            )}
          </div>
        </section>
      )}
      <p className="m-0 text-sm leading-normal text-fg2">
        Fix it on your computer, then run <code className="rounded-md bg-surface2 px-1.5 py-px font-mono text-[13px]">dojo check</code>.
      </p>
      <div className="flex flex-col gap-2.5">
        <Link to={ticketHref(current.id)} className={primaryBtn}>
          Continue the ticket
        </Link>
        {ticket && ticket.hint_count > 0 && (
          <Link to={ticketHref(current.id)} className={outlineBtn}>
            Open the ticket for hints
          </Link>
        )}
      </div>
    </div>
  );
}

function Row({ tag, tone, id, title, end }: { tag: string; tone: 'passed' | 'now' | 'next'; id: string; title: string; end?: string | null }) {
  const tagCls = { passed: 'bg-oksoft text-ok', now: 'bg-accent text-accentfg', next: 'bg-surface2 text-muted' }[tone];
  return (
    <li className={`border-b border-line last:border-b-0 ${tone === 'now' ? 'bg-accentsoft' : ''}`}>
      <Link to={ticketHref(id)} className="flex min-h-11 items-center gap-3.5 px-5 py-3 text-sm text-fg2 no-underline">
        <span className={`${badge} ${tagCls}`}>{tag}</span>
        <span className="hidden w-[190px] flex-none font-mono sm:inline">{id}</span>
        <span className={tone === 'now' ? 'font-bold text-fg' : ''}>{title}</span>
        {end && <span className="ml-auto font-mono text-xs text-muted">{end}</span>}
      </Link>
    </li>
  );
}

function Recent({ last, current, next, role }: { last: Activity['recent'][number] | null; current: TodayTicket | null; next: TodayTicket | null; role: TodayData['role'] }) {
  return (
    <section aria-label="Recent and next" className="hidden overflow-hidden rounded-2xl border border-line bg-surface md:block">
      <div className="flex items-baseline justify-between gap-3 border-b border-line px-5 py-4">
        <h2 className="m-0 text-base font-extrabold">Recent and next</h2>
        {role && (
          <span className={`${mono} text-[13px]`}>
            {role.tickets_passed} of {role.tickets_total} tickets
          </span>
        )}
      </div>
      <ul className="m-0 list-none p-0">
        {last && <Row tone="passed" tag="Passed" id={last.ticket_id} title={last.title} end={last.commit ? shortCommit(last.commit) : null} />}
        {current && <Row tone="now" tag="Now" id={current.id} title={current.title} />}
        {next && <Row tone="next" tag="Next" id={next.id} title={next.title} />}
      </ul>
    </section>
  );
}

const OPEN_LABEL: Record<TodayBetween['kind'], string> = {
  story: 'Open the story',
  quiz: 'Open the quiz',
  writing: 'Open the writing task',
  feedback: 'Open the feedback round',
};

/** The one item between tickets (design 3.10): the dark card of the L1 board. Never blocks, so no skip. */
function BetweenCard({ item }: { item: TodayBetween }) {
  const eyebrow = item.kind === 'story' ? 'Story · your team' : `${BETWEEN_LABEL[item.kind]} · ${item.est_minutes} min`;
  return (
    <section aria-label="Between tickets" className="relative flex flex-col gap-3 rounded-[20px] bg-fg p-5 text-bg md:p-[22px]">
      <p className="m-0 text-xs font-bold tracking-[0.8px] uppercase opacity-70">{eyebrow}</p>
      <p className="m-0 text-[15px] leading-normal font-semibold md:text-base">{item.title}</p>
      <Link
        to={itemHref(item.kind, item.id)}
        className="inline-flex h-11 items-center self-start rounded-[10px] bg-bg px-4 text-sm font-bold text-fg no-underline after:absolute after:inset-0 after:content-[''] md:after:hidden"
      >
        {OPEN_LABEL[item.kind]}
      </Link>
    </section>
  );
}

// ---------------------------------------------------------------- views

function CurrentView({ current, next, role, between }: { current: TodayTicket; next: TodayTicket | null; role: TodayData['role']; between: TodayBetween | null }) {
  const activity = useActivity();
  const ticket = useTicketIf(current.id);
  const last = lastPass(activity.data);
  const brief = ticket ? parseBrief(ticket.brief) : null;
  return (
    <div className={grid2}>
      <div className="flex min-w-0 flex-col gap-4 md:gap-[22px]">
        {role && <RoleStrip role={role} />}
        <div className="flex flex-col gap-2 md:gap-2.5">
          <div className="flex flex-wrap items-center gap-2 md:gap-2.5">
            <span className={`${badge} bg-accentsoft text-accentink`}>In progress</span>
            <span className={`${mono} text-xs md:text-[13px]`}>
              <span className="md:hidden">{shortId(current.id)}</span>
              <span className="hidden md:inline">
                {current.id}
                {current.lab_state ? ` · lab ${current.lab_state}` : ''}
              </span>
            </span>
          </div>
          <Headline text={current.title} />
        </div>
        <div className={`grid items-stretch gap-4 ${brief ? 'md:grid-cols-2' : ''}`}>
          {brief && <Message brief={brief} />}
          <LabCard current={current} ticket={ticket} />
        </div>
        <PhoneCheck current={current} ticket={ticket} />
        <Recent last={last} current={current} next={next} role={role} />
      </div>
      <aside aria-label="Your progress" className="flex flex-col gap-[18px]">
        {role && (
          <div className="hidden md:block">
            <RoleCard role={role} />
          </div>
        )}
        <ActivitySlot query={activity} />
        {between && <BetweenCard item={between} />}
      </aside>
    </div>
  );
}

/** Leaving "day one done" through its action marks it seen (design 3.9); a failure only means it shows once more. */
function useMarkFirstRunSeen() {
  const queryClient = useQueryClient();
  return () => {
    void patchMe({ first_run_seen: true }).then((r) => {
      if (r.ok) queryClient.setQueryData<TodayData>(TODAY_KEY, (t) => (t ? { ...t, first_run: null } : t));
    });
  };
}

/** `next` is the ticket the action opens: the next one, or with `continuing` the one already in progress. */
function NextView({
  next,
  role,
  name,
  firstRun,
  between,
  continuing = false,
}: {
  next: TodayTicket;
  role: TodayData['role'];
  name: string;
  firstRun: FirstRun | null;
  between: TodayBetween | null;
  continuing?: boolean;
}) {
  const activityQuery = useActivity();
  const markSeen = useMarkFirstRunSeen();
  const nextTicket = useTicketIf(next.id);
  const activity = activityQuery.data;
  const done = activity?.recent.find((r) => r.ticket_id === firstRun?.id) ?? null;
  const brief = nextTicket ? parseBrief(nextTicket.brief) : null;
  const first = firstRun !== null;
  const left = role ? role.tickets_total - role.tickets_passed : 0;

  return (
    <div className="flex flex-col gap-6">
      {first ? (
        <div className="flex flex-col gap-2.5">
          <span className={`${mono} text-[13px]`}>day one · done</span>
          <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[44px] md:tracking-[-1.5px]">
            Welcome to the team,{' '}
            <span style={{ background: 'var(--hlbg)', WebkitBackgroundClip: 'text', backgroundClip: 'text', color: 'transparent' }}>@{name}</span>
            <span className="-ml-[0.12em]">.</span>
          </h1>
        </div>
      ) : (
        <Headline text="Ready for the next ticket." />
      )}
      <div className="grid items-start gap-[18px] md:grid-cols-[1fr_380px] md:gap-6">
        <div className="flex min-w-0 flex-col gap-[18px]">
          {firstRun && (
            <section aria-label="Your first ticket is done" className={`${card} border-ok`}>
              <div className="flex items-center justify-between gap-3">
                <h2 className="m-0 text-base font-extrabold">Your first ticket is done</h2>
                <span className={`${badge} bg-oksoft font-mono text-ok`}>{shortId(firstRun.id)}</span>
              </div>
              <p className="m-0 text-[17px] font-extrabold">{firstRun.title}</p>
              <p className="m-0 text-sm leading-[1.55] text-fg2">
                {done?.commit && (
                  <>
                    Your first signed commit is <b className="font-mono">{shortCommit(done.commit)}</b>.{' '}
                  </>
                )}
                {role && `${left} ${left === 1 ? 'ticket' : 'tickets'} to go in ${role.title}.`}
              </p>
            </section>
          )}
          <section aria-label="Next up" className={card}>
            <div className="flex items-center justify-between gap-3">
              <h2 className="m-0 text-base font-extrabold">{continuing ? 'In progress' : 'Next up'}</h2>
              {nextTicket && <span className="font-mono text-xs text-muted">about {nextTicket.est_minutes} min</span>}
            </div>
            <p className="m-0 text-[17px] font-extrabold">
              <span className="font-mono text-muted">{shortId(next.id)}</span> · {next.title}
            </p>
            {brief && <p className="m-0 text-sm leading-[1.55] text-fg2">{brief.text}</p>}
            <div className="flex flex-wrap gap-2.5">
              <Link to={ticketHref(next.id)} onClick={first ? markSeen : undefined} className={primaryBtn}>
                {continuing ? 'Continue' : first ? 'Start the next one' : `Start ${shortId(next.id)}`}
              </Link>
              <Link to="/path" className={ghostBtn}>
                See the whole path
              </Link>
            </div>
          </section>
        </div>
        <aside aria-label="Your progress" className="flex flex-col gap-[18px]">
          {role && <RoleCard role={role} />}
          {activity && (first ? <ActivityNote activity={activity} /> : <ActivityPanel activity={activity} />)}
          {between && <BetweenCard item={between} />}
        </aside>
      </div>
    </div>
  );
}

function Notice({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex max-w-xl flex-col gap-3">
      <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px]">{title}</h1>
      {children}
    </div>
  );
}

function PlainTitle() {
  return <h1 className="m-0 text-[32px] leading-[1.08] font-extrabold tracking-[-1px] md:text-[44px]">Today</h1>;
}

function Failed({ error, retry }: { error: Error; retry: () => void }) {
  const reason = error instanceof ReadError ? error.reason : 'failed';
  return (
    <div role="alert" className="flex flex-col items-start gap-3">
      <p className="m-0 text-bad">{READ_MESSAGES[reason]}</p>
      {reason !== 'suspended' && (
        <button type="button" onClick={retry} className="h-11 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold text-fg">
          Retry
        </button>
      )}
    </div>
  );
}

/** L1 Today: the ticket in progress, or the next one; role, belt and activity on the side. */
export function Today({ me }: { me: Me }) {
  useLiveData();
  const today = useToday();
  const queryClient = useQueryClient();
  const signedOut = today.isError && today.error instanceof ReadError && today.error.reason === 'signed_out';

  // The cached learner is stale: clear it so /signin does not bounce back here.
  useEffect(() => {
    if (signedOut) queryClient.setQueryData(ME_KEY, null);
  }, [signedOut, queryClient]);
  if (signedOut) return null;

  let body: ReactNode;
  if (today.isPending) {
    body = (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <p role="status" className="m-0 text-muted">
          Loading today...
        </p>
      </div>
    );
  } else if (today.isError) {
    body = (
      <div className="flex flex-col gap-3">
        <PlainTitle />
        <Failed error={today.error} retry={() => void today.refetch()} />
      </div>
    );
  } else {
    const { current, next, role, all_passed: allPassed, first_run: firstRun = null, between = null } = today.data;
    if (firstRun && (current ?? next)) {
      body = <NextView next={(current ?? next)!} role={role} name={me.username} firstRun={firstRun} between={between} continuing={current !== null} />;
    } else if (current) body = <CurrentView current={current} next={next} role={role} between={between} />;
    else if (next) body = <NextView next={next} role={role} name={me.username} firstRun={null} between={between} />;
    else if (allPassed && role) {
      body = (
        <div className={grid2}>
          <Notice title="Every ticket is passed.">
            <p className="m-0 text-fg2">Nothing is waiting for you right now.</p>
            <Link to="/path" className={`${primaryBtn} self-start`}>
              See your path
            </Link>
          </Notice>
          <div className="flex flex-col gap-[18px]">
            <RoleCard role={role} />
            {between && <BetweenCard item={between} />}
          </div>
        </div>
      );
    } else {
      body = (
        <Notice title="Nothing to work on yet.">
          <p className="m-0 text-fg2">Pick up a ticket from your path.</p>
          <Link to="/path" className={`${primaryBtn} self-start`}>
            See your path
          </Link>
          {between && <BetweenCard item={between} />}
        </Notice>
      );
    }
  }

  return (
    <AppFrame me={me}>
      {me.email === null && (
        <div className="mb-5">
          <EmailPrompt me={me} />
        </div>
      )}
      {body}
    </AppFrame>
  );
}
