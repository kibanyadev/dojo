import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { SettingsLayout } from '../components/SettingsLayout';
import type { Me } from '../lib/me';
import { AGENTS_EXCERPT, CLIENTS, type ClientConfig, type ClientId } from '../lib/mcpConfig';
import { useOnboarding, type DoctorCheck } from '../lib/onboarding';
import { useOnboardingLive } from '../lib/onboardingLive';

export const COPIED_CLEAR_MS = 3000;
export const STILL_WAITING_MS = 60_000;

const card = 'flex flex-col gap-3 rounded-2xl border border-line bg-surface p-5';
const title = 'm-0 text-base font-extrabold';
const body = 'm-0 text-sm leading-[1.55] text-fg2';
const code = 'm-0 overflow-x-auto rounded-xl bg-term px-4 py-3.5 text-left font-mono text-xs leading-[1.6] whitespace-pre-wrap text-termfg';
const inline = 'rounded bg-surface2 px-1.5 py-0.5 font-mono text-[13px]';
const action = 'inline-flex h-11 items-center justify-center rounded-[10px] px-3.5 text-sm font-bold md:h-9';

function Mark({ ok }: { ok: boolean }) {
  return (
    <span className={`inline-flex size-5 flex-none items-center justify-center rounded-full text-xs font-extrabold text-white ${ok ? 'bg-ok' : 'bg-bad'}`}>
      <span aria-hidden="true">{ok ? '✓' : '✗'}</span>
      <span className="sr-only">{ok ? 'Your AI can see:' : 'Your AI cannot see:'}</span>
    </span>
  );
}

function AgentsCard({ login }: { login: string }) {
  return (
    <section className={card} aria-labelledby="ai-agents">
      <div className="flex items-center justify-between gap-2.5">
        <h2 id="ai-agents" className={title}>
          AGENTS.md in your repo
        </h2>
        <span className="inline-flex h-[26px] flex-none items-center rounded-md bg-oksoft px-2.5 font-mono text-xs font-bold whitespace-nowrap text-ok">in {login}/dojo</span>
      </div>
      <p className={body}>
        Any assistant that opens your repo or the lab reads it first. It turns your AI into a mentor: it asks what you tried, explains, points to the docs, and never writes the fix.
      </p>
      <pre aria-label="AGENTS.md excerpt" tabIndex={0} className={code}>
        {AGENTS_EXCERPT}
      </pre>
      <p className={body}>Claude Code, Copilot and Cursor read it through CLAUDE.md, .github/copilot-instructions.md and a Cursor rule, all pointing to the same file.</p>
    </section>
  );
}

function Tabs({ client, onChange }: { client: ClientConfig; onChange: (id: ClientId) => void }) {
  const id = useId();
  // Arrow keys, Home and End move between tabs and select them (automatic activation, roving tabindex).
  function onKey(e: KeyboardEvent<HTMLDivElement>) {
    const at = CLIENTS.findIndex((c) => c.id === client.id);
    const to = { ArrowRight: (at + 1) % CLIENTS.length, ArrowLeft: (at + CLIENTS.length - 1) % CLIENTS.length, Home: 0, End: CLIENTS.length - 1 }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    const next = CLIENTS[to]!.id;
    onChange(next);
    document.getElementById(`${id}-${next}`)?.focus();
  }
  return (
    <>
      <div role="tablist" aria-label="Your AI tool" onKeyDown={onKey} className="flex flex-wrap gap-1.5">
        {CLIENTS.map((c) => (
          <button
            key={c.id}
            id={`${id}-${c.id}`}
            type="button"
            role="tab"
            aria-selected={client.id === c.id}
            aria-controls={`${id}-panel`}
            tabIndex={client.id === c.id ? 0 : -1}
            onClick={() => onChange(c.id)}
            className={`h-11 rounded-[10px] border px-3 text-[13px] font-bold md:h-[34px] ${client.id === c.id ? 'border-accent bg-accentsoft text-accentink' : 'border-line bg-surface text-fg2'}`}
          >
            {c.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${id}-panel`} aria-labelledby={`${id}-${client.id}`} className="flex flex-col gap-2">
        <pre aria-label={`${client.label} config`} tabIndex={0} className={code}>
          {client.snippet}
        </pre>
        <p className="m-0 text-[13px] leading-normal text-muted">
          Save it as <span className={inline}>{client.file}</span> in your project. If the file exists, add the <span className={inline}>dojo</span> entry.
        </p>
      </div>
    </>
  );
}

type Shown = { tone: 'ok' | 'warn' | 'wait'; mark: string; name: string; text: string; fix: string };

function describe(check: DoctorCheck | undefined, waiting: boolean, slow: boolean): Shown {
  if (waiting) {
    return {
      tone: 'wait',
      mark: '…',
      name: 'Waiting',
      text: slow ? 'Still waiting. Run dojo doctor in your terminal.' : 'Waiting for dojo doctor to report from your terminal...',
      fix: '',
    };
  }
  if (check?.status === 'ok') return { tone: 'ok', mark: '✓', name: 'Connected', text: check.text, fix: '' };
  return {
    tone: 'warn',
    mark: '!',
    name: 'Not connected yet',
    text: check?.text || 'No AI has connected to dojo mcp yet',
    fix: check?.fix || 'Add the config above, restart your AI, then run dojo doctor again',
  };
}

const TONE = { ok: 'bg-oksoft text-ok', warn: 'bg-accentsoft text-accentink', wait: 'bg-surface2 text-fg2' };

function McpCard() {
  useOnboardingLive();
  const onboarding = useOnboarding();
  const [clientId, setClientId] = useState<ClientId>('claude');
  const [notice, setNotice] = useState('');
  const noticeTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  // The row's checked_at when "Test the connection" was pressed; a different value later means a new report.
  const [snapshot, setSnapshot] = useState<{ at: string | null } | null>(null);
  const [slowFor, setSlowFor] = useState<object | null>(null);
  const client = CLIENTS.find((c) => c.id === clientId) ?? CLIENTS[0]!;
  const check = onboarding.data?.checks?.find((c) => c.id === 'mcp');
  const waiting = snapshot !== null && (check?.checked_at ?? null) === snapshot.at;

  useEffect(() => () => clearTimeout(noticeTimer.current), []);
  useEffect(() => {
    if (!waiting) return;
    const t = setTimeout(() => setSlowFor(snapshot), STILL_WAITING_MS);
    return () => clearTimeout(t);
  }, [waiting, snapshot]);
  const slow = waiting && slowFor === snapshot;

  function say(text: string) {
    clearTimeout(noticeTimer.current);
    setNotice(text);
    noticeTimer.current = setTimeout(() => setNotice(''), COPIED_CLEAR_MS);
  }

  async function copy() {
    // Clear first so a second copy changes the live region again and is announced again.
    setNotice('');
    try {
      await navigator.clipboard.writeText(client.snippet);
      say('Copied');
    } catch {
      say('Copy failed, select the text instead');
    }
  }

  function pick(id: ClientId) {
    clearTimeout(noticeTimer.current);
    setNotice('');
    setClientId(id);
  }

  let state;
  if (onboarding.isPending) {
    state = (
      <p role="status" className="m-0 text-sm text-muted">
        Loading...
      </p>
    );
  } else if (!onboarding.data) {
    state = (
      <div role="alert" className="flex flex-wrap items-center justify-between gap-2.5">
        <p className={body}>Could not load the connection state. Please try again.</p>
        <button type="button" onClick={() => void onboarding.refetch()} className={`${action} border border-line bg-surface text-fg`}>
          Retry
        </button>
      </div>
    );
  } else {
    const shown = describe(check, waiting, slow);
    state = (
      <div role="status" className={`flex items-start gap-2.5 rounded-xl px-3.5 py-3 text-sm ${TONE[shown.tone]}`}>
        <span aria-hidden="true" className="font-bold">
          {shown.mark}
        </span>
        <div className="flex min-w-0 flex-col gap-1">
          <span className="font-bold">{shown.name}</span>
          <span className="break-words">{shown.text}</span>
          {shown.fix && <span className="break-words">{shown.fix}</span>}
        </div>
      </div>
    );
  }

  return (
    <section className={card} aria-labelledby="ai-mcp">
      <h2 id="ai-mcp" className={title}>
        Connect the dojo MCP
      </h2>
      <p className={body}>Your AI sees the ticket you are on, without copy and paste.</p>
      <Tabs client={client} onChange={pick} />
      <div className="flex flex-wrap items-center gap-2.5">
        <button type="button" onClick={() => void copy()} className={`${action} border border-line bg-surface text-fg`}>
          Copy config
        </button>
        <button type="button" disabled={!onboarding.data} onClick={() => setSnapshot({ at: check?.checked_at ?? null })} className={`${action} border-0 bg-transparent text-fg2 hover:text-fg`}>
          Test the connection
        </button>
        <span role="status" className="text-[13px] font-bold text-fg2">
          {notice}
        </span>
      </div>
      {state}
      <p className="m-0 text-[13px] leading-normal text-muted">
        Run <span className={inline}>dojo doctor</span> in your terminal after your AI has started. This page updates when it reports.
      </p>
    </section>
  );
}

const SEES: [boolean, string][] = [
  [true, 'The ticket and what fixed means'],
  [true, 'Your last dojo check, line by line'],
  [true, 'The hints you have opened, and the docs links'],
  [false, 'Solutions: they are not on your machine'],
  [false, 'Role exams: no AI, they run on our servers'],
];

function SeesCard() {
  return (
    <section className={card} aria-labelledby="ai-sees">
      <h2 id="ai-sees" className={title}>
        What your AI can see
      </h2>
      <ul className="m-0 flex list-none flex-col gap-3 p-0">
        {SEES.map(([ok, text]) => (
          <li key={text} className="flex items-start gap-2.5 text-sm leading-[1.45]">
            <Mark ok={ok} />
            <span>{text}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function YourAI({ me }: { me: Me }) {
  return (
    <SettingsLayout me={me}>
      <AgentsCard login={me.github_login} />
      <McpCard />
      <SeesCard />
    </SettingsLayout>
  );
}
