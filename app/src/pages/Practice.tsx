import { useState } from 'react';
import { Link, Navigate } from 'react-router';
import { AppFrame } from '../components/AppFrame';
import type { Me } from '../lib/me';
import { READ_MESSAGES, ReadError, useCatalog, usePractice, type Difficulty, type PracticeItem } from '../lib/read';
import { useLiveData } from '../lib/stream';
import { CopyCommand, ticketHref } from './Today';

export type Kind = PracticeItem['kind'];

export interface Filters {
  kind: Kind | null;
  role: string | null;
  tag: string | null;
  difficulty: Difficulty | null;
}

export const NO_FILTERS: Filters = { kind: null, role: null, tag: null, difficulty: null };

const DIFFICULTIES: Difficulty[] = ['easy', 'medium', 'hard'];
const DIFFICULTY_LABEL: Record<Difficulty, string> = { easy: 'Easy', medium: 'Medium', hard: 'Hard' };
const KIND_CHIP: Record<Kind, string> = { practice: 'Practice', ticket: 'Revisit' };

export const isFiltered = (f: Filters) => f.kind !== null || f.role !== null || f.tag !== null || f.difficulty !== null;

/** Every chip narrows the list; the groups combine, so a card must match each one that is on. */
export function applyFilters(items: PracticeItem[], f: Filters): PracticeItem[] {
  return items.filter(
    (it) =>
      (f.kind === null || it.kind === f.kind) &&
      (f.role === null || it.role === f.role) &&
      (f.tag === null || it.tags.includes(f.tag)) &&
      (f.difficulty === null || it.difficulty === f.difficulty),
  );
}

const unique = (values: string[]) => [...new Set(values)];

const CHIP = 'inline-flex h-11 items-center rounded-[10px] px-3.5 text-[13px] font-bold md:h-10';
const CHIP_ON = 'border-2 border-accent bg-accentsoft text-accentink';
const CHIP_OFF = 'border border-line bg-surface text-fg2 hover:text-fg';

function Chip({ label, pressed, onClick }: { label: string; pressed: boolean; onClick: () => void }) {
  return (
    <button type="button" aria-pressed={pressed} onClick={onClick} className={`${CHIP} ${pressed ? CHIP_ON : CHIP_OFF}`}>
      {pressed && <span aria-hidden="true">✓&nbsp;</span>}
      {label}
    </button>
  );
}

function Group({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap items-center gap-2">
      {children}
    </div>
  );
}

function Chips({
  items,
  filters,
  roleTitle,
  onChange,
}: {
  items: PracticeItem[];
  filters: Filters;
  roleTitle: (slug: string) => string;
  onChange: (f: Filters) => void;
}) {
  const kinds = unique(items.map((i) => i.kind)) as Kind[];
  const roles = unique(items.flatMap((i) => (i.role ? [i.role] : [])));
  const tags = unique(items.flatMap((i) => i.tags)).sort();
  const levels = DIFFICULTIES.filter((d) => items.some((i) => i.difficulty === d));
  const toggle = <K extends keyof Filters>(key: K, value: NonNullable<Filters[K]>) =>
    onChange({ ...filters, [key]: filters[key] === value ? null : value });
  return (
    <div role="group" aria-label="Filter practice" className="flex flex-wrap items-center gap-2">
      <Chip label="All" pressed={!isFiltered(filters)} onClick={() => onChange(NO_FILTERS)} />
      {kinds.length > 1 && (
        <Group label="Kind">
          {kinds.map((k) => (
            <Chip key={k} label={KIND_CHIP[k]} pressed={filters.kind === k} onClick={() => toggle('kind', k)} />
          ))}
        </Group>
      )}
      {roles.length > 0 && (
        <Group label="Role">
          {roles.map((r) => (
            <Chip key={r} label={roleTitle(r)} pressed={filters.role === r} onClick={() => toggle('role', r)} />
          ))}
        </Group>
      )}
      {tags.length > 0 && (
        <Group label="Topic">
          {tags.map((t) => (
            <Chip key={t} label={t.charAt(0).toUpperCase() + t.slice(1)} pressed={filters.tag === t} onClick={() => toggle('tag', t)} />
          ))}
        </Group>
      )}
      {levels.length > 0 && (
        <Group label="Difficulty">
          {levels.map((d) => (
            <Chip key={d} label={DIFFICULTY_LABEL[d]} pressed={filters.difficulty === d} onClick={() => toggle('difficulty', d)} />
          ))}
        </Group>
      )}
    </div>
  );
}

const BADGE = 'inline-flex h-[26px] flex-none items-center rounded-md px-2.5 text-xs font-bold whitespace-nowrap';

function StateBadge({ state }: { state: PracticeItem['state'] }) {
  if (state === 'passed' || state === 'revisit') {
    return (
      <span className={`${BADGE} bg-oksoft text-ok`}>
        <span aria-hidden="true">✓&nbsp;</span>Passed
      </span>
    );
  }
  if (state === 'started') return <span className={`${BADGE} bg-surface2 text-fg2`}>In progress</span>;
  return null;
}

function Card({ item, roleTitle }: { item: PracticeItem; roleTitle: (slug: string) => string }) {
  const label = item.kind === 'ticket' ? 'Revisit' : item.difficulty ? DIFFICULTY_LABEL[item.difficulty] : 'Practice';
  const meta = [`${item.est_minutes} min`, ...(item.kind === 'ticket' && item.role ? [roleTitle(item.role)] : [])].join(' · ');
  return (
    <li className="flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5">
      <div className="flex flex-wrap items-center gap-3">
        <span className={`${BADGE} bg-surface2 text-fg2`}>{label}</span>
        <span className="font-mono text-xs text-muted">{meta}</span>
        <StateBadge state={item.state} />
      </div>
      <h2 className="m-0 text-[17px] font-extrabold">
        <Link to={ticketHref(item.id)} className="text-fg no-underline hover:underline">
          {item.title}
        </Link>
      </h2>
      {item.tags.length > 0 && <div className="font-mono text-xs text-accentink">{item.tags.join(' · ')}</div>}
      <div className="self-start">
        <CopyCommand command={`dojo start ${item.id}`} />
      </div>
    </li>
  );
}

function DuckNote() {
  return (
    <div className="flex items-center gap-3">
      <span aria-hidden="true" className="box-border size-16 flex-none rounded-lg border border-dashed border-ctl bg-surface" />
      <p className="m-0 text-[13px] leading-[1.55] text-muted">New every month. Stuck? Explain it to the duck first.</p>
    </div>
  );
}

function Loaded({ items }: { items: PracticeItem[] }) {
  const catalog = useCatalog();
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const roleTitle = (slug: string) => catalog.data?.roles.find((r) => r.slug === slug)?.title ?? slug;
  const shown = applyFilters(items, filters);
  return (
    <>
      {items.length === 0 ? (
        <p className="m-0 text-[15px] text-fg2">No scenarios yet. New ones arrive every month.</p>
      ) : (
        <>
          <Chips items={items} filters={filters} roleTitle={roleTitle} onChange={setFilters} />
          {shown.length === 0 ? (
            <div role="status" className="flex flex-col items-start gap-3">
              <p className="m-0 text-[15px] text-fg2">Nothing matches these filters.</p>
              <button
                type="button"
                onClick={() => setFilters(NO_FILTERS)}
                className="h-11 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold md:h-10"
              >
                Show all
              </button>
            </div>
          ) : (
            <ul aria-label="Practice scenarios" className="m-0 grid list-none grid-cols-1 gap-4 p-0 md:grid-cols-2 lg:grid-cols-3">
              {shown.map((item) => (
                <Card key={item.id} item={item} roleTitle={roleTitle} />
              ))}
            </ul>
          )}
        </>
      )}
      <DuckNote />
    </>
  );
}

function Body() {
  useLiveData();
  const practice = usePractice();
  let content;
  if (practice.isPending) {
    content = (
      <p role="status" className="text-muted">
        Loading practice...
      </p>
    );
  } else if (practice.isError) {
    const err = practice.error;
    if (err instanceof ReadError && err.reason === 'signed_out') return <Navigate to="/signin" replace />;
    const reason = err instanceof ReadError ? err.reason : 'failed';
    content = (
      <div role="alert">
        <p className="text-bad">{READ_MESSAGES[reason]}</p>
        <button type="button" onClick={() => void practice.refetch()} className="h-11 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold md:h-9">
          Retry
        </button>
      </div>
    );
  } else {
    content = <Loaded items={practice.data.items} />;
  }
  return (
    <div className="flex flex-col gap-[18px] md:gap-6">
      <div className="font-mono text-[13px] text-muted">practice · outside your path</div>
      <h1 className="m-0 text-[30px] leading-[1.06] font-extrabold tracking-tight md:text-[44px]">
        Broken servers, <span className="bg-(image:--hlbg) bg-clip-text text-transparent">on demand</span>
      </h1>
      <p className="m-0 max-w-[60ch] text-[15px] leading-[1.55] text-fg2">
        Short scenarios to keep your hands warm between roles. Same commands as your tickets: start it, ssh in, check. They run on your laptop
        and never block your path.
      </p>
      {content}
    </div>
  );
}

/** R6: practice scenarios and revisits, filtered on the client by chips. */
export function PracticePage({ me }: { me: Me }) {
  return (
    <AppFrame me={me}>
      <Body />
    </AppFrame>
  );
}
