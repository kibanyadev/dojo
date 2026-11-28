/** Pure helpers for the Today page: no React, no fetching. */

export interface BriefMessage {
  /** Ticket reference as the brief writes it, e.g. `FND-001`. */
  ref: string;
  sender: string;
  /** The parenthesized role after the sender, e.g. `team lead`. */
  team: string | null;
  /** First paragraph of the message, line breaks folded. */
  text: string;
}

/** Reads the client message off a brief that opens "Ticket REF, from NAME (TEAM):". Anything else gives null. */
export function parseBrief(brief: string): BriefMessage | null {
  const m = /^Ticket (\S+?), from ([^(:\n]+?)(?: \(([^)\n]*)\))?:\r?\n\r?\n([\s\S]+)$/.exec(brief.trim());
  if (!m) return null;
  const text = (m[4] ?? '').split(/\r?\n\s*\r?\n/)[0]?.replace(/\s*\r?\n\s*/g, ' ').trim() ?? '';
  if (!text) return null;
  return { ref: m[1] ?? '', sender: (m[2] ?? '').trim(), team: m[3]?.trim() || null, text };
}

export function ago(iso: string | null, now = Date.now()): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return '';
  const min = Math.max(0, Math.round((now - t) / 60_000));
  if (min < 1) return 'just now';
  if (min < 60) return `${min} min ago`;
  const h = Math.round(min / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} days ago`;
}

export const shortCommit = (sha: string) => sha.slice(0, 7);

export const initial = (name: string) => (name.trim()[0] ?? '?').toUpperCase();

export const capitalize = (s: string) => (s ? s[0]!.toUpperCase() + s.slice(1) : s);

// ---------------------------------------------------------------- activity grid

export const GRID_WEEKS = 20;

export interface HeatCell {
  day: string;
  count: number;
  /** 0 (none) to 4 (four or more). */
  level: 0 | 1 | 2 | 3 | 4;
  /** After today, in the last week: drawn empty. */
  future: boolean;
}

export interface HeatGrid {
  /** Monday-first weeks, oldest first, today's week last; seven cells each. */
  weeks: HeatCell[][];
  /** Passes inside the drawn weeks. */
  total: number;
  /** The day with the most passes (the latest on a tie), or null when none. */
  busiest: { day: string; count: number } | null;
}

const MS_DAY = 86_400_000;
const parseDay = (d: string) => {
  const [y, m, dd] = d.split('-').map(Number);
  return Date.UTC(y ?? 1970, (m ?? 1) - 1, dd ?? 1);
};
const fmtDay = (ms: number) => new Date(ms).toISOString().slice(0, 10);

/**
 * Lays the served days out as 20 Monday-first weeks ending with the week of `to`. Days are calendar
 * dates stepped in UTC, so a DST change never shifts a cell. Days outside the drawn weeks are dropped.
 */
export function buildHeatGrid(to: string, days: { day: string; count: number }[]): HeatGrid {
  const end = parseDay(to);
  const monday = end - ((new Date(end).getUTCDay() + 6) % 7) * MS_DAY;
  const start = monday - (GRID_WEEKS - 1) * 7 * MS_DAY;
  const counts = new Map(days.map((d) => [d.day, d.count]));
  const weeks: HeatCell[][] = [];
  let total = 0;
  let busiest: HeatGrid['busiest'] = null;
  for (let w = 0; w < GRID_WEEKS; w++) {
    const week: HeatCell[] = [];
    for (let i = 0; i < 7; i++) {
      const ms = start + (w * 7 + i) * MS_DAY;
      const day = fmtDay(ms);
      const future = ms > end;
      const count = future ? 0 : (counts.get(day) ?? 0);
      total += count;
      if (count > 0 && (!busiest || count >= busiest.count)) busiest = { day, count };
      week.push({ day, count, level: Math.min(count, 4) as HeatCell['level'], future });
    }
    weeks.push(week);
  }
  return { weeks, total, busiest };
}

/** Labels of the non-ticket kinds, as the canvas names them (W5, L1 Today). */
export const BETWEEN_LABEL: Record<'writing' | 'quiz' | 'story' | 'feedback', string> = {
  writing: 'Writing',
  quiz: 'Quiz',
  story: 'Story',
  feedback: 'Feedback',
};

/** Item pages of design 3.10: L9 `/w/*id`, L11 `/quiz/*id`, L4 `/story/*id` (also the feedback round). */
export function itemHref(kind: keyof typeof BETWEEN_LABEL, id: string): string {
  const prefix = kind === 'writing' ? 'w' : kind === 'quiz' ? 'quiz' : 'story';
  return `/${prefix}/${id.split('/').map(encodeURIComponent).join('/')}`;
}

const DAY_LABEL = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });

/** The text alternative for the grid. */
export function heatSummary(grid: HeatGrid): string {
  const { total, busiest } = grid;
  if (total === 0 || !busiest) return `No tickets passed in the last ${GRID_WEEKS} weeks`;
  const noun = total === 1 ? 'ticket' : 'tickets';
  return `${total} ${noun} passed in the last ${GRID_WEEKS} weeks, most on ${DAY_LABEL.format(parseDay(busiest.day))}`;
}
