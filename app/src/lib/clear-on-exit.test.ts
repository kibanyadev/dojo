// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const calls: string[] = [];
vi.mock('./pwa', () => ({ clearDataCache: vi.fn(async () => void calls.push('clear')) }));

import { signOut } from '../components/AppFrame';
import { deleteMe } from './settings';

afterEach(() => {
  calls.length = 0;
  vi.unstubAllGlobals();
});

const stubFetch = (res: () => Response | never) =>
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      calls.push('request');
      return res();
    }),
  );

describe('clearing the data cache around sign-out and account deletion', () => {
  it('clears before and after the logout request', async () => {
    stubFetch(() => new Response(null, { status: 204 }));
    expect(await signOut()).toBe(true);
    expect(calls).toEqual(['clear', 'request', 'clear']);
  });

  it('clears after a failed logout request too', async () => {
    stubFetch(() => {
      throw new TypeError('offline');
    });
    await expect(signOut()).rejects.toThrow();
    expect(calls).toEqual(['clear', 'request', 'clear']);
  });

  it('clears before and after the account DELETE, whatever its outcome', async () => {
    stubFetch(() => new Response(null, { status: 204 }));
    await deleteMe('irene');
    expect(calls).toEqual(['clear', 'request', 'clear']);
    calls.length = 0;
    stubFetch(() => {
      throw new TypeError('offline');
    });
    await deleteMe('irene');
    expect(calls).toEqual(['clear', 'request', 'clear']);
  });
});
