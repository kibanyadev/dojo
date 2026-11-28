export async function registerServiceWorker(prod: boolean, container: ServiceWorkerContainer | undefined): Promise<void> {
  if (!prod || !container) return;
  try {
    await container.register('/app/sw.js', { scope: '/app/' });
  } catch {
    // The app works without a worker; only offline support is lost.
  }
}

/** Posts {"type":"clear"} to the worker and waits for its ack, so sign-out and account deletion never leave learner data cached. Never hangs: no worker, or no ack within the timeout, resolves anyway. */
export async function clearDataCache(container: ServiceWorkerContainer | undefined = typeof navigator === 'undefined' ? undefined : navigator.serviceWorker, timeoutMs = 1500): Promise<void> {
  if (!container || typeof MessageChannel === 'undefined') return;
  const deadline = Date.now() + timeoutMs;
  // A hard reload leaves the page uncontrolled; the registered worker still holds the cache.
  const worker =
    container.controller ?? (await Promise.race([container.getRegistration?.('/app/').then((r) => r?.active ?? null), sleep(timeoutMs).then(() => null)]).catch(() => null));
  if (!worker) return;
  const left = Math.max(0, deadline - Date.now());
  await new Promise<void>((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(resolve, left);
    channel.port1.onmessage = () => {
      clearTimeout(timer);
      resolve();
    };
    try {
      worker.postMessage({ type: 'clear' }, [channel.port2]);
    } catch {
      clearTimeout(timer);
      resolve();
    }
  });
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
