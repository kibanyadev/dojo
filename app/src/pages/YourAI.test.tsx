// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { AGENTS_EXCERPT, CLIENTS } from '../lib/mcpConfig';
import { COPIED_CLEAR_MS, STILL_WAITING_MS } from './YourAI';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene-gh', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const warn = { id: 'mcp', group: 'advisory', status: 'warn', text: 'No AI has connected to dojo mcp yet', fix: 'Add the config from Settings > Your AI, restart your AI, then run dojo doctor again', source: 'cli', checked_at: '2020-01-01T00:00:00Z' };
const ok = { id: 'mcp', group: 'advisory', status: 'ok', text: 'Your AI connected: claude-code, 2026-11-28', fix: '', source: 'cli', checked_at: '2020-01-01T00:00:00Z' };

let container: HTMLDivElement;
let root: Root;
let client: QueryClient;
let checks: object[] | 'fail';
const at = (offsetMs: number) => new Date(Date.now() + offsetMs).toISOString();
const reload = async () => {
  await act(async () => void client.invalidateQueries({ queryKey: ['onboarding'] }));
  await settle();
};

function onboarding() {
  return { next_step: 'done', path: { slug: 'systems-devops', chosen: true }, cli_connected: true, first_ticket: null, checks };
}

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount() {
  client = new QueryClient();
  const router = createMemoryRouter(routes, { initialEntries: ['/settings/ai'] });
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  checks = [warn];
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/onboarding') return checks === 'fail' ? Response.json({ error: 'x' }, { status: 500 }) : Response.json(onboarding());
      return Response.json({}, { status: 404 });
    }),
  );
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const text = () => container.textContent ?? '';
const tabs = () => [...container.querySelectorAll<HTMLElement>('[role=tab]')];
const button = (label: string) => [...container.querySelectorAll('button')].find((b) => b.textContent === label || b.getAttribute('aria-label') === label);
const click = async (el: HTMLElement | undefined) => {
  await act(async () => el?.click());
  await settle();
};

describe('Your AI page', () => {
  it('is a Settings section with its own link', async () => {
    await mount();
    expect(container.querySelector('nav a[aria-current=page]')?.textContent).toBe('Your AI');
  });

  it('shows the repo pill from the GitHub login, the AGENTS.md excerpt and the board copy', async () => {
    await mount();
    expect(text()).toContain('in irene-gh/dojo');
    expect(container.querySelector('pre[aria-label="AGENTS.md excerpt"]')?.textContent).toBe(AGENTS_EXCERPT);
    expect(text()).toContain('never writes the fix');
    expect(text()).toContain('all pointing to the same file.');
    expect(text()).toContain('Your AI sees the ticket you are on, without copy and paste.');
  });

  it('shows one tab per tool, Claude first, with its snippet', async () => {
    await mount();
    expect(tabs().map((t) => t.textContent)).toEqual(['Claude', 'VS Code', 'Cursor']);
    const pre = container.querySelector('pre[aria-label="Claude config"]');
    expect(JSON.parse(pre?.textContent ?? '')).toEqual({ mcpServers: { dojo: { command: 'dojo', args: ['mcp'] } } });
    expect(text()).not.toContain('claude mcp add');
    expect(container.querySelector('a[href^="https://"]')).toBeNull();
  });

  it('follows the tabs pattern: linked panel, roving tabindex, arrow, Home and End keys', async () => {
    await mount();
    const key = (k: string) => act(async () => void document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })));
    const selected = () => tabs().find((t) => t.getAttribute('aria-selected') === 'true')?.textContent;
    const panel = container.querySelector('[role=tabpanel]');
    expect(tabs().every((t) => t.getAttribute('aria-controls') === panel?.id)).toBe(true);
    expect(tabs().map((t) => t.tabIndex)).toEqual([0, -1, -1]);
    act(() => tabs()[0]!.focus());
    await key('ArrowRight');
    expect(selected()).toBe('VS Code');
    expect(document.activeElement).toBe(tabs()[1]);
    expect(text()).toContain('.vscode/mcp.json');
    await key('ArrowLeft');
    await key('ArrowLeft');
    expect(selected()).toBe('Cursor');
    await key('Home');
    expect(selected()).toBe('Claude');
    await key('End');
    expect(selected()).toBe('Cursor');
    expect(tabs().map((t) => t.tabIndex)).toEqual([-1, -1, 0]);
    expect(panel?.getAttribute('aria-labelledby')).toBe(tabs()[2]!.id);
  });

  it('gives every button a 44 px phone target', async () => {
    await mount();
    for (const b of [...tabs(), button('Copy config')!, button('Test the connection')!]) expect(b.className).toContain('h-11');
  });

  it('lists what the AI can and cannot see, with the mark spoken', async () => {
    await mount();
    const rows = [...container.querySelectorAll('section[aria-labelledby=ai-sees] li')].map((li) => li.textContent);
    expect(rows).toEqual([
      '✓Your AI can see:The ticket and what fixed means',
      '✓Your AI can see:Your last dojo check, line by line',
      '✓Your AI can see:The hints you have opened, and the docs links',
      '✗Your AI cannot see:Solutions: they are not on your machine',
      '✗Your AI cannot see:Role exams: no AI, they run on our servers',
    ]);
  });

  it('copies the snippet and says Copied, then clears it after a few seconds, on a tab change too', async () => {
    await mount();
    const writeText = vi.fn(async () => undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const copied = () => [...container.querySelectorAll('[role=status]')].some((s) => s.textContent === 'Copied');
    await click(button('Copy config'));
    expect(writeText).toHaveBeenCalledWith(CLIENTS[0]!.snippet);
    expect(copied()).toBe(true);
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await act(async () => button('Copy config')?.click());
    expect(copied()).toBe(true);
    await act(async () => void vi.advanceTimersByTime(COPIED_CLEAR_MS + 10));
    expect(copied()).toBe(false);
    await act(async () => button('Copy config')?.click());
    expect(copied()).toBe(true);
    await act(async () => tabs()[1]!.click());
    expect(copied()).toBe(false);
    vi.useRealTimers();
    writeText.mockRejectedValueOnce(new Error('no'));
    await click(button('Copy config'));
    expect(text()).toContain('Copy failed, select the text instead');
  });

  it('says it is not connected, with the fix, when the mcp check warns or is missing', async () => {
    await mount();
    expect(text()).toContain('Not connected yet');
    expect(text()).toContain('restart your AI');
    expect(text()).toContain('Run dojo doctor in your terminal after your AI has started');
    checks = [];
    await mount();
    expect(text()).toContain('Not connected yet');
  });

  it('says Connected with the check text when the mcp check is ok', async () => {
    checks = [ok];
    await mount();
    expect(text()).toContain('Your AI connected: claude-code, 2026-11-28');
    expect(text()).not.toContain('Not connected yet');
  });

  it.each([
    ['a browser clock far ahead of the server', 3_600_000],
    ['a browser clock far behind the server', -3_600_000],
  ])('Test the connection ignores the browser clock: %s', async (_name, skew) => {
    checks = [{ ...warn, checked_at: at(skew) }];
    await mount();
    await click(button('Test the connection'));
    expect(text()).toContain('Waiting for dojo doctor to report');
    checks = [{ ...ok, checked_at: at(skew + 1000) }];
    await reload();
    expect(text()).not.toContain('Waiting for dojo doctor');
    expect(text()).toContain('Your AI connected: claude-code');
  });

  it('resolves on the first-ever row when there was none at the click', async () => {
    checks = [];
    await mount();
    await click(button('Test the connection'));
    expect(text()).toContain('Waiting for dojo doctor to report');
    checks = [{ ...warn, checked_at: at(0) }];
    await reload();
    expect(text()).not.toContain('Waiting for dojo doctor');
    expect(text()).toContain('Not connected yet');
  });

  it('keeps waiting while the same row comes back, and says so after a minute', async () => {
    await mount();
    await click(button('Test the connection'));
    await reload();
    expect(text()).toContain('Waiting for dojo doctor to report');
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await act(async () => button('Test the connection')?.click());
    await act(async () => void vi.advanceTimersByTime(STILL_WAITING_MS + 10));
    vi.useRealTimers();
    expect(text()).toContain('Still waiting. Run dojo doctor in your terminal.');
  });

  it('offers a retry when the state cannot be loaded', async () => {
    checks = 'fail';
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('Could not load the connection state');
    expect(button('Retry')?.className).toContain('h-11');
  });
});
