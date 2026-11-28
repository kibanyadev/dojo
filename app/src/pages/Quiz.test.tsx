// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { routes } from '../routes';
import { resetReadBlock, type AnswerResult, type QuizItem } from '../lib/read';
import { applyAnswer } from './Quiz';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = { id: '1', username: 'irene', github_login: 'irene', email: null, language: 'en', usage_stats: true, analytics_consent: null };
const ID = 'support-l1/quiz-02';

const SPEC = [
  { id: 'who-last', text: 'Who logged in to web-02 last night?', choices: ['`who`', '`last`', '`w`'], answer: 1, explain: '`who` only shows who is logged in now.' },
  { id: 'sudo', text: 'Which group grants sudo?', choices: ['users', 'staff', 'sudo'], answer: 2, explain: null },
  { id: 'logs', text: 'Where do ssh logins land?', choices: ['`/tmp`', '`/var/log/auth.log`', '`/etc`'], answer: 1, explain: 'The auth log.' },
];
const NOTES = (i: number) => SPEC[i]!.choices.map((c) => `note for ${c}`);

interface Row {
  first: number;
  last: number;
}
let rows: Record<string, Row>;
let kind: string;
let answerStatus: number;
let posts: { question: string; choice: number }[];
let headers: Headers[];

function view(): QuizItem {
  const questions = SPEC.map((s, i) => {
    const r = rows[s.id];
    return {
      id: s.id,
      text: s.text,
      choices: s.choices,
      answer_state: r
        ? { first_choice: r.first, first_correct: r.first === s.answer, last_choice: r.last, last_correct: r.last === s.answer, answer: s.answer, notes: NOTES(i), explain: s.explain }
        : null,
    };
  });
  const answered = questions.filter((q) => q.answer_state).length;
  return {
    id: ID,
    kind: 'quiz',
    role: 'support-l1',
    title: 'Users and logs',
    est_minutes: 5,
    tags: [],
    done: answered === SPEC.length,
    answered,
    total: SPEC.length,
    first_try_correct: questions.filter((q) => q.answer_state?.first_correct).length,
    questions,
  };
}

function stubApi() {
  posts = [];
  headers = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/v1/me') return Response.json(me);
      if (url === '/v1/path') return Response.json({ path: 'systems', chosen: true, roles: [{ slug: 'support-l1', title: 'Support L1', summary: '', locked: false, passed: false, tickets_passed: 1, tickets_total: 3, items: [] }] });
      if (url === `/v1/items/${ID}`) {
        if (kind === 'missing') return new Response(null, { status: 404 });
        if (kind === 'writing') return Response.json({ id: ID, kind: 'writing' });
        return Response.json(view());
      }
      if (url === `/v1/items/${ID}/answers` && init?.method === 'POST') {
        headers.push(new Headers(init.headers));
        const body = JSON.parse(init.body as string) as { question: string; choice: number };
        posts.push(body);
        if (answerStatus === 429) return Response.json({ error: 'rate_limited' }, { status: 429, headers: { 'Retry-After': '7' } });
        if (answerStatus !== 200) return Response.json({ error: 'server_error' }, { status: answerStatus });
        const i = SPEC.findIndex((s) => s.id === body.question);
        const spec = SPEC[i]!;
        const prev = rows[spec.id];
        rows[spec.id] = { first: prev?.first ?? body.choice, last: body.choice };
        const v = view();
        const result: AnswerResult = {
          correct: body.choice === spec.answer,
          answer: spec.answer,
          notes: NOTES(i),
          explain: spec.explain,
          done: v.done,
          answered: v.answered,
          total: v.total,
          first_try_correct: v.first_try_correct,
        };
        return Response.json(result);
      }
      return new Response(null, { status: 404 });
    }),
  );
}

let container: HTMLDivElement;
let root: Root;

async function settle() {
  for (let i = 0; i < 4; i++) await act(async () => new Promise((r) => setTimeout(r, 5)));
}

async function mount(path = `/quiz/${ID}`) {
  stubApi();
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  await act(async () => {
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  await settle();
}

const text = () => container.textContent ?? '';
const h1 = () => container.querySelector('h1')?.textContent ?? '';
const button = (name: string) => [...container.querySelectorAll('button, a')].find((b) => b.textContent === name) as HTMLElement | undefined;
const radios = () => [...container.querySelectorAll<HTMLInputElement>('input[type=radio]')];

async function pick(i: number) {
  await act(async () => radios()[i]!.click());
}
async function click(name: string) {
  const b = button(name);
  if (!b) throw new Error(`no button ${name}`);
  await act(async () => b.click());
  await settle();
}
/** Pick a choice and press Check answer. */
async function answer(i: number) {
  await pick(i);
  await click('Check answer');
}

beforeEach(() => {
  resetReadBlock();
  rows = {};
  kind = 'quiz';
  answerStatus = 200;
  container = document.createElement('div');
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe('applyAnswer', () => {
  const base = view_(undefined);
  function view_(row: Row | undefined): QuizItem {
    rows = row ? { sudo: row } : {};
    return view();
  }
  const r: AnswerResult = { correct: true, answer: 2, notes: ['a', 'b', 'c'], explain: null, done: false, answered: 1, total: 3, first_try_correct: 1 };

  it('writes the first and the last on a first answer', () => {
    const out = applyAnswer(base, 'sudo', 2, r);
    expect(out.questions[1]!.answer_state).toMatchObject({ first_choice: 2, first_correct: true, last_choice: 2, last_correct: true, answer: 2 });
    expect(out).toMatchObject({ answered: 1, first_try_correct: 1, done: false });
  });

  it('keeps the first answer on a retake and moves only the last', () => {
    const wrong = applyAnswer(base, 'sudo', 0, { ...r, correct: false, first_try_correct: 0 });
    const again = applyAnswer(wrong, 'sudo', 2, { ...r, first_try_correct: 0 });
    expect(again.questions[1]!.answer_state).toMatchObject({ first_choice: 0, first_correct: false, last_choice: 2, last_correct: true });
  });
});

describe('unanswered quiz', () => {
  beforeEach(() => mount());

  it('shows the first question, a radio group and the progress count', () => {
    expect(h1()).toBe('Question 1 of 3');
    expect(text()).toContain('Support L1 · quiz-02 · Users and logs');
    expect(text()).toContain('Who logged in to web-02 last night?');
    const group = container.querySelector('[role=radiogroup]')!;
    expect(group.getAttribute('aria-labelledby')).toBe('q-who-last');
    expect(container.querySelector('#q-who-last')?.textContent).toContain('Who logged in');
    expect(radios()).toHaveLength(3);
    expect(radios().every((r) => !r.checked && !r.disabled)).toBe(true);
    expect(container.querySelector('[aria-label="Progress: question 1 of 3"]')).not.toBeNull();
    expect(text()).toContain('A wrong answer never blocks you.');
  });

  it('shows no notes or explanation before the answer', () => {
    expect(text()).not.toContain('note for');
    expect(container.querySelector('#quiz-feedback')?.textContent).toBe('');
  });

  it('asks for a pick without calling the server', async () => {
    await click('Check answer');
    expect(container.querySelector('[role=alert]')?.textContent).toBe('Pick an answer first.');
    expect(posts).toEqual([]);
  });
});

describe('answering', () => {
  beforeEach(() => mount());

  it('marks a correct answer, shows every note and the explanation in the polite live region', async () => {
    await answer(1);
    expect(posts).toEqual([{ question: 'who-last', choice: 1 }]);
    expect(headers[0]!.get('X-Dojo-Csrf')).toBe('1');
    const live = container.querySelector('#quiz-feedback[role=status][aria-live=polite]')!;
    expect(live.textContent).toContain('Correct');
    expect(live.textContent).toContain('only shows who is logged in now');
    expect(live.textContent).not.toContain('comes back later');
    expect(text()).toContain('note for who');
    expect(text()).toContain('note for w');
    expect(text()).toContain('✓ correct');
    expect(radios().every((r) => r.disabled)).toBe(true);
    expect(container.querySelector('[aria-label="Progress: question 1 of 3"]')).not.toBeNull();
  });

  it('moves focus to Next and the progress fills after the answer', async () => {
    await answer(1);
    expect(document.activeElement).toBe(button('Next question'));
    expect(container.querySelectorAll('[role=img] span.bg-accent')).toHaveLength(1);
  });

  it('marks a wrong pick with text, shows the right one and says the question returns', async () => {
    await answer(0);
    const live = container.querySelector('#quiz-feedback')!;
    expect(live.textContent).toContain('Not quite');
    expect(live.textContent).toContain('This question comes back later in the quiz.');
    expect(text()).toContain('✗ yours');
    expect(text()).toContain('(not correct)');
    expect(text()).toContain('✓ correct');
  });

  it('goes to the next question with focus on its heading', async () => {
    await answer(1);
    await click('Next question');
    expect(h1()).toBe('Question 2 of 3');
    expect(document.activeElement).toBe(container.querySelector('h1'));
    expect(radios().every((r) => !r.checked)).toBe(true);
    expect(container.querySelector('#quiz-feedback')?.textContent).toBe('');
  });

  it('lets the keyboard do the whole question: Check and Next are real buttons in tab order', async () => {
    await pick(2);
    expect(button('Check answer')?.tagName).toBe('BUTTON');
    expect(radios().every((r) => r.name === 'q-who-last')).toBe(true);
  });
});

describe('a full run', () => {
  beforeEach(() => mount());

  it('brings the missed question back at the end, then shows the result with the first-try count', async () => {
    await answer(0); // wrong, returns later
    await click('Next question');
    await answer(2);
    await click('Next question');
    await answer(1);
    expect(button('Next question')).toBeDefined();
    await click('Next question');
    expect(h1()).toBe('Question 1 of 3');
    await answer(1);
    expect(container.querySelector('#quiz-feedback')?.textContent).not.toContain('comes back later');
    expect(button('See your result')).toBeDefined();
    await click('See your result');
    expect(h1()).toBe('Quiz done. 2 of 3.');
    expect(document.activeElement).toBe(container.querySelector('h1'));
    expect(container.querySelector('[aria-label="2 of 3 correct on the first try"]')).not.toBeNull();
    expect(button('Retry the one you missed')).toBeUndefined();
    expect(button('Back to the path')?.getAttribute('href')).toBe('/path');
    expect(text()).toContain('2 of 3 questions right on the first try.');
  });

  it('keeps focus on the heading when the last question was missed and comes straight back', async () => {
    await answer(1);
    await click('Next question');
    await answer(2);
    await click('Next question');
    await answer(0); // last question, wrong: it is queued again right away
    await click('Next question');
    expect(h1()).toBe('Question 3 of 3');
    expect(document.activeElement).toBe(container.querySelector('h1'));
  });

  it('offers the question again when the retake is still wrong, and keeps the first-try score', async () => {
    await answer(0);
    await click('Next question');
    await answer(2);
    await click('Next question');
    await answer(1);
    await click('Next question');
    await answer(2); // still wrong
    expect(container.querySelector('#quiz-feedback')?.textContent).not.toContain('comes back later');
    await click('See your result');
    expect(h1()).toBe('Quiz done. 2 of 3.');
    expect(text()).toContain('The one to look at again');
    expect(text()).toContain('question 1');
    expect(text()).toContain('only shows who is logged in now');
    await click('Retry the one you missed');
    expect(h1()).toBe('Question 1 of 3');
    await answer(1);
    await click('See your result');
    expect(h1()).toBe('Quiz done. 2 of 3.');
    expect(text()).not.toContain('The one to look at again');
    expect(button('Retry the one you missed')).toBeUndefined();
  });
});

describe('a quiz already started or finished', () => {
  it('resumes at the first unanswered question', async () => {
    rows = { 'who-last': { first: 1, last: 1 } };
    await mount();
    expect(h1()).toBe('Question 2 of 3');
    expect(container.querySelectorAll('[role=img] span.bg-accent')).toHaveLength(1);
  });

  it('opens on the result when every question was answered, and offers the missed one', async () => {
    rows = { 'who-last': { first: 0, last: 0 }, sudo: { first: 2, last: 2 }, logs: { first: 1, last: 1 } };
    await mount();
    expect(h1()).toBe('Quiz done. 2 of 3.');
    expect(button('Retry the one you missed')).toBeDefined();
  });
});

describe('errors', () => {
  beforeEach(() => mount());

  it('says how long to wait on a 429 and keeps the pick', async () => {
    answerStatus = 429;
    await answer(1);
    expect(container.querySelector('[role=alert]')?.textContent).toBe('Too many requests. Try again in 7 s.');
    expect(radios()[1]!.checked).toBe(true);
    expect(container.querySelector('#quiz-feedback')?.textContent).toBe('');
    answerStatus = 200;
    await act(async () => new Promise((r) => setTimeout(r, 1)));
  });

  it('says it could not check on a server error, and a retry works', async () => {
    answerStatus = 500;
    await answer(1);
    expect(container.querySelector('[role=alert]')?.textContent).toBe('Could not check that answer. Please try again.');
    answerStatus = 200;
    await click('Check answer');
    expect(container.querySelector('#quiz-feedback')?.textContent).toContain('Correct');
    expect(container.querySelector('[role=alert]')).toBeNull();
  });
});

describe('load failures', () => {
  it('says the quiz is missing on a 404', async () => {
    kind = 'missing';
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('We could not find that quiz.');
    expect(button('Back to the path')).toBeDefined();
  });

  it('does not draw another kind as a quiz', async () => {
    kind = 'writing';
    await mount();
    expect(container.querySelector('[role=alert]')?.textContent).toContain('We could not find that quiz.');
  });

  it('rejects an id with the wrong shape without a request', async () => {
    await mount('/quiz/one-part');
    expect(container.querySelector('[role=alert]')?.textContent).toContain('We could not find that quiz.');
    const urls = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls.map((c) => c[0] as string);
    expect(urls.some((u) => u.startsWith('/v1/items'))).toBe(false);
  });
});
