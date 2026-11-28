import { Link, Navigate } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import type { Me } from '../lib/me';
import { BETWEEN_LABEL, itemHref } from '../lib/today';
import { READ_MESSAGES, ReadError, useCatalog, usePath, type BetweenItem, type Catalog, type LearnerPath, type PathItem, type PathRole, type TicketState } from '../lib/read';
import { useLiveData } from '../lib/stream';

type RoleStatus = 'passed' | 'here' | 'next' | 'soon';

const PILL = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';
const META = 'font-mono text-xs text-muted';

// here/next are derived client-side because /v1/path has no current-role field; tickets are not gated.
/** First unpassed, unlocked role is "here"; later ones follow it; a locked role has no tickets yet. */
export function roleStatuses(roles: PathRole[]): RoleStatus[] {
  const here = roles.findIndex((r) => !r.passed && !r.locked);
  return roles.map((r, i) => {
    if (r.passed) return 'passed';
    if (r.locked) return 'soon';
    return i === here ? 'here' : 'next';
  });
}

const ITEM_LABEL: Record<TicketState | 'none', string> = {
  passed: 'Passed',
  started: 'In progress',
  revisit: 'Revisit',
  none: 'Not started',
};

function Marker({ status, locked }: { status: RoleStatus; locked: boolean }) {
  const base = 'inline-flex size-8 flex-none items-center justify-center rounded-full text-sm font-extrabold';
  if (status === 'passed') {
    return (
      <span className={`${base} bg-ok text-white`}>
        <span aria-hidden="true">✓</span>
        <span className="sr-only">done</span>
      </span>
    );
  }
  if (status === 'here') {
    return (
      <span className={`${base} bg-accent text-white`}>
        <span aria-hidden="true">●</span>
        <span className="sr-only">you are here</span>
      </span>
    );
  }
  return (
    <span className={`${base} box-border border-2 border-ctl bg-surface text-muted`}>
      <span className="sr-only">{locked ? 'coming later' : 'next'}</span>
    </span>
  );
}

type Row = { kind: 'ticket'; order: number; item: PathItem } | { kind: 'between'; order: number; item: BetweenItem };

/** Tickets and between items of one role in one list, by `order` (the shared position); tickets first on a tie. */
export function interleave(items: PathItem[], between: BetweenItem[]): Row[] {
  const rows: Row[] = [
    ...items.map((item): Row => ({ kind: 'ticket', order: item.order, item })),
    ...between.map((item): Row => ({ kind: 'between', order: item.order, item })),
  ];
  return rows.sort((a, b) => a.order - b.order || (a.kind === b.kind ? 0 : a.kind === 'ticket' ? -1 : 1));
}

const ROW = 'flex min-h-11 items-center justify-between gap-3 rounded-[10px] px-2 text-sm text-fg no-underline hover:bg-surface2';

function Items({ items, between }: { items: PathItem[]; between: BetweenItem[] }) {
  return (
    <ol className="m-0 mt-1 flex list-none flex-col p-0">
      {interleave(items, between).map((row) => {
        if (row.kind === 'between') {
          const it = row.item;
          return (
            <li key={it.id}>
              <Link to={itemHref(it.kind, it.id)} className={ROW}>
                <span>
                  <span className="font-mono text-xs text-muted">{it.order}. </span>
                  <span className="font-mono text-xs font-bold tracking-[0.6px] text-accentink uppercase">{BETWEEN_LABEL[it.kind]}</span> · {it.title}
                </span>
                <span className={`font-mono text-xs ${it.done ? 'text-ok' : 'text-muted'}`}>{it.done ? 'Done' : 'Not done'}</span>
              </Link>
            </li>
          );
        }
        const it = row.item;
        const label = ITEM_LABEL[it.state ?? 'none'];
        return (
          <li key={it.id}>
            <Link to={`/t/${it.id}`} className={ROW}>
              <span>
                <span className="font-mono text-xs text-muted">{it.order}. </span>
                {it.title}
              </span>
              <span className={`font-mono text-xs ${it.state === 'passed' ? 'text-ok' : 'text-muted'}`}>{label}</span>
            </Link>
          </li>
        );
      })}
    </ol>
  );
}

function RoleRow({ role, status, hereTitle, hasExam, last }: { role: PathRole; status: RoleStatus; hereTitle: string | null; hasExam: boolean; last: boolean }) {
  const pill =
    status === 'passed' ? (
      <span className={`${PILL} bg-oksoft text-ok`}>Passed</span>
    ) : status === 'here' ? (
      <span className={`${PILL} bg-accentsoft text-accentink`}>You are here</span>
    ) : null;
  let note: string | null = null;
  if (last && status !== 'passed' && status !== 'here') note = 'the destination';
  else if (status === 'soon') note = 'coming later';
  else if (status === 'next' && hereTitle) note = `after ${hereTitle}`;
  const between = role.between_items ?? [];
  const hasTickets = (role.tickets_total > 0 || between.length > 0) && status !== 'soon';
  return (
    <li className="relative flex gap-3.5">
      {!last && <span aria-hidden="true" className="absolute top-8 -bottom-0 left-[15px] w-0.5 bg-line" />}
      <Marker status={status} locked={role.locked} />
      <div className="flex min-w-0 flex-1 flex-col gap-1 pb-[18px] md:pb-[22px]">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2 className="m-0 text-lg font-extrabold">{role.title}</h2>
          {pill}
        </div>
        {role.summary && <p className="m-0 text-sm leading-normal text-fg2">{role.summary}</p>}
        {hasExam && <div className={META}>ends in a role exam: one work day</div>}
        {status === 'passed' ? (
          <div className="font-mono text-xs text-accentink">passed</div>
        ) : (
          role.tickets_total > 0 && (
            <div className="font-mono text-xs text-accentink">
              {role.tickets_passed} of {role.tickets_total} tickets
            </div>
          )
        )}
        {note && <div className="font-mono text-xs text-accentink">{note}</div>}
        {hasTickets && (
          <details className="group mt-1">
            <summary className="flex min-h-11 w-fit cursor-pointer items-center text-[13px] font-bold text-accentink">
              Tickets in {role.title}
            </summary>
            <Items items={role.items} between={between} />
          </details>
        )}
      </div>
    </li>
  );
}

const CARD = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5';
const EYEBROW = 'm-0 text-xs font-bold tracking-[0.8px] text-muted uppercase';

/** Belt state is not served yet (see API gaps): an empty belt, with no mark claimed. */
function BeltPanel() {
  return (
    <section aria-labelledby="belt-h" className={CARD}>
      <h2 id="belt-h" className={EYEBROW}>
        Your belt · how you work, on every path
      </h2>
      <div aria-hidden="true" className="box-border h-[18px] w-[200px] rounded border border-dashed border-ctl bg-surface" />
      <p className="m-0 text-sm leading-[1.55] text-fg2">
        Belt marks come from what you write and how you work with others, in English. They never unlock or block a role.
      </p>
      <a href="/paths" className="inline-flex min-h-11 w-fit items-center text-[13px] font-bold text-accentink no-underline md:min-h-0">
        What the belt measures
      </a>
    </section>
  );
}

export interface Crossing {
  path: string;
  pathTitle: string;
  shared: string[];
  opens: string | null;
  open: boolean;
  planned: boolean;
}

/** Other catalog paths that share a role with this one; "opens" is the first role of theirs that follows a shared one. */
export function crossings(catalog: Catalog, current: LearnerPath): Crossing[] {
  if (!current.path) return [];
  const title = (slug: string) => catalog.roles.find((r) => r.slug === slug)?.title ?? slug;
  const mine = new Set(current.roles.map((r) => r.slug));
  const passed = new Set(current.roles.filter((r) => r.passed).map((r) => r.slug));
  const out: Crossing[] = [];
  for (const p of catalog.paths) {
    if (p.slug === current.path) continue;
    const roles = catalog.path_roles.filter((pr) => pr.path === p.slug).sort((a, b) => a.position - b.position);
    const shared = roles.filter((r) => mine.has(r.role));
    if (shared.length === 0) continue;
    const lastShared = Math.max(...shared.map((r) => roles.indexOf(r)));
    const next = roles.slice(lastShared + 1).find((r) => !mine.has(r.role));
    const nextHasTickets = next ? catalog.items.some((i) => i.kind === 'ticket' && i.role === next.role) : false;
    const planned = p.status !== 'available';
    out.push({
      path: p.slug,
      pathTitle: p.title,
      shared: shared.map((r) => title(r.role)),
      opens: next ? title(next.role) : null,
      open: !planned && next !== undefined && nextHasTickets && shared.every((r) => passed.has(r.role)),
      planned,
    });
  }
  return out;
}

/** Holds the panel's space while the catalog loads, so it does not push the page when it arrives. */
function CrossingPlaceholder() {
  return <div aria-hidden="true" className={`${CARD} min-h-[170px]`} />;
}

function CrossingPanel({ items }: { items: Crossing[] }) {
  if (items.length === 0) return null;
  return (
    <section aria-labelledby="cross-h" className={CARD}>
      <h2 id="cross-h" className={EYEBROW}>
        Paths that cross yours
      </h2>
      <ul className="m-0 flex list-none flex-col gap-3 p-0">
        {items.map((c) => (
          <li key={c.path} className="flex items-start gap-2.5 text-sm leading-[1.45]">
            <span
              className={`box-border inline-flex size-5 flex-none items-center justify-center rounded-full text-xs font-extrabold ${c.open ? 'bg-ok text-white' : 'border-2 border-muted'}`}
            >
              {c.open && <span aria-hidden="true">✓</span>}
              <span className="sr-only">{c.open ? 'Open to you' : c.planned ? 'Coming later' : 'Not yet'}</span>
            </span>
            <span>
              {c.opens ? (
                <>
                  {c.shared.join(' and ')} also opens <b>{c.opens}</b> on the {c.pathTitle} path{c.planned ? ', coming later' : ''}
                </>
              ) : (
                <>
                  {c.shared.join(' and ')} is shared with the <b>{c.pathTitle}</b> path{c.planned ? ', coming later' : ''}
                </>
              )}
            </span>
          </li>
        ))}
      </ul>
      <p className="m-0 text-[13px] leading-[1.55] text-muted">Switching path keeps every role you passed.</p>
    </section>
  );
}

function Timeline({ path }: { path: LearnerPath }) {
  const catalog = useCatalog();
  const exam = new Set(catalog.data?.roles.filter((r) => r.has_exam).map((r) => r.slug));
  const pathTitle = catalog.data?.paths.find((p) => p.slug === path.path)?.title ?? null;
  const statuses = roleStatuses(path.roles);
  const hereIdx = statuses.indexOf('here');
  const hereTitle = path.roles[hereIdx]?.title ?? null;
  const target = path.roles.at(-1)?.title ?? '';
  const cross = catalog.data ? crossings(catalog.data, path) : [];
  return (
    <>
      <div className="min-h-5 font-mono text-[13px] text-muted">{pathTitle ? `path · ${pathTitle}` : ''}</div>
      <h1 className="m-0 mt-3 text-[30px] leading-[1.1] font-extrabold tracking-tight md:text-[44px]">
        Your way to <span className="bg-(image:--hlbg) bg-clip-text text-transparent">{target}</span>
      </h1>
      <p className="mt-4 mb-6 max-w-[60ch] text-[15px] leading-[1.55] text-fg2">
        {path.roles.length === 1 ? 'One role' : `${numberWord(path.roles.length)} roles`}, each a real job. Pass a role's work-day exam
        and you are ready for that job, even if you stop there.
      </p>
      <div className="grid items-start gap-[18px] lg:grid-cols-[1fr_400px] lg:gap-8">
        <ol aria-label="Roles on your path" className="m-0 flex list-none flex-col p-0">
          {path.roles.map((role, i) => (
            <RoleRow key={role.slug} role={role} status={statuses[i] ?? 'next'} hereTitle={hereTitle} hasExam={exam.has(role.slug)} last={i === path.roles.length - 1} />
          ))}
        </ol>
        <div className="flex flex-col gap-[18px]">
          <BeltPanel />
          {catalog.isPending ? <CrossingPlaceholder /> : <CrossingPanel items={cross} />}
        </div>
      </div>
    </>
  );
}

const WORDS = ['Zero', 'One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven', 'Eight', 'Nine'];
const numberWord = (n: number) => WORDS[n] ?? String(n);

function Body() {
  useLiveData();
  const path = usePath();
  if (path.isPending) return <p role="status" className="text-muted">Loading your path...</p>;
  if (path.isError) {
    const err = path.error;
    if (err instanceof ReadError && err.reason === 'signed_out') return <Navigate to="/signin" replace />;
    const reason = err instanceof ReadError ? err.reason : 'failed';
    return (
      <div role="alert">
        <p className="text-bad">{READ_MESSAGES[reason]}</p>
        <button type="button" onClick={() => void path.refetch()} className="h-11 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold md:h-9">
          Retry
        </button>
      </div>
    );
  }
  if (path.data.path === null || path.data.roles.length === 0) {
    return (
      <div>
        <h1 className="m-0 text-[30px] font-extrabold tracking-tight">Your path</h1>
        <p className="mt-3 text-fg2">No path is open yet. Check back soon.</p>
      </div>
    );
  }
  return <Timeline path={path.data} />;
}

/** R1: the learner's path as a vertical role timeline. */
export function PathPage({ me }: { me: Me }) {
  return (
    <AppFrame me={me}>
      <Body />
    </AppFrame>
  );
}
