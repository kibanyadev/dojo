import { describe, expect, it, vi } from 'vitest';
import { clearDataCache, registerServiceWorker } from './pwa';

describe('registerServiceWorker', () => {
  it('registers /app/sw.js with scope /app/ in production', async () => {
    const register = vi.fn(async () => ({}));
    await registerServiceWorker(true, { register } as unknown as ServiceWorkerContainer);
    expect(register).toHaveBeenCalledWith('/app/sw.js', { scope: '/app/' });
  });

  it('does nothing outside production', async () => {
    const register = vi.fn();
    await registerServiceWorker(false, { register } as unknown as ServiceWorkerContainer);
    expect(register).not.toHaveBeenCalled();
  });

  it('does nothing without service worker support', async () => {
    await expect(registerServiceWorker(true, undefined)).resolves.toBeUndefined();
  });

  it('swallows a failed registration', async () => {
    const register = vi.fn(async () => {
      throw new Error('denied');
    });
    await expect(registerServiceWorker(true, { register } as unknown as ServiceWorkerContainer)).resolves.toBeUndefined();
  });
});

describe('clearDataCache', () => {
  it('posts the clear message with a port and resolves on the ack', async () => {
    const postMessage = vi.fn((_msg: unknown, ports: MessagePort[]) => void ports[0]?.postMessage('cleared'));
    await clearDataCache({ controller: { postMessage } } as unknown as ServiceWorkerContainer, 2000);
    expect(postMessage).toHaveBeenCalledWith({ type: 'clear' }, expect.any(Array));
  });

  it('resolves after the timeout when the worker never answers', async () => {
    const postMessage = vi.fn();
    const start = Date.now();
    await clearDataCache({ controller: { postMessage } } as unknown as ServiceWorkerContainer, 30);
    expect(Date.now() - start).toBeGreaterThanOrEqual(25);
  });

  it('resolves at once with no worker', async () => {
    await expect(clearDataCache({ controller: null } as unknown as ServiceWorkerContainer)).resolves.toBeUndefined();
    await expect(clearDataCache(undefined)).resolves.toBeUndefined();
  });

  it('falls back to the registered worker when the page is uncontrolled', async () => {
    const postMessage = vi.fn((_msg: unknown, ports: MessagePort[]) => void ports[0]?.postMessage('cleared'));
    const getRegistration = vi.fn(async () => ({ active: { postMessage } }));
    await clearDataCache({ controller: null, getRegistration } as unknown as ServiceWorkerContainer, 2000);
    expect(getRegistration).toHaveBeenCalledWith('/app/');
    expect(postMessage).toHaveBeenCalledWith({ type: 'clear' }, expect.any(Array));
  });

  it('resolves when uncontrolled with no registration, or when the lookup hangs or fails', async () => {
    await expect(clearDataCache({ controller: null, getRegistration: async () => undefined } as unknown as ServiceWorkerContainer)).resolves.toBeUndefined();
    await expect(clearDataCache({ controller: null, getRegistration: async () => { throw new Error('x'); } } as unknown as ServiceWorkerContainer)).resolves.toBeUndefined();
    await expect(clearDataCache({ controller: null, getRegistration: () => new Promise(() => {}) } as unknown as ServiceWorkerContainer, 30)).resolves.toBeUndefined();
  });

  it('resolves when posting throws', async () => {
    const postMessage = vi.fn(() => {
      throw new Error('gone');
    });
    await expect(clearDataCache({ controller: { postMessage } } as unknown as ServiceWorkerContainer, 2000)).resolves.toBeUndefined();
  });
});
