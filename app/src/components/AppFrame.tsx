import { useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { Logo } from './Logo';
import { NotificationsBell } from './NotificationsBell';
import { TabBar, TopNav } from './PrimaryNav';
import { useLiveData } from '../lib/stream';
import { apiFetch } from '../lib/api';
import type { Me } from '../lib/me';

/** POST /v1/auth/logout. 204, and 401/404 (already signed out, endpoint not live yet) all mean "go to O1". */
export async function signOut(): Promise<boolean> {
  const res = await apiFetch('/v1/auth/logout', { method: 'POST', body: '{}' });
  return res.status === 204 || res.status === 401 || res.status === 404;
}

/** The signed-in shell: header (home, nav, sign out, profile avatar to Settings) around a page. */
export function AppFrame({ me, children }: { me: Me; children: ReactNode }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  // The bell and the Inbox tab badge stay live on every signed-in page.
  useLiveData();

  async function onSignOut() {
    setBusy(true);
    setFailed(false);
    const ok = await signOut().catch(() => false);
    if (ok) {
      queryClient.clear();
      await navigate('/signin', { replace: true });
      return;
    }
    setBusy(false);
    setFailed(true);
  }

  return (
    <div className="min-h-screen bg-bg font-sans text-fg">
      <header className="border-b border-line bg-bg">
        <div className="mx-auto flex max-w-[1200px] items-center justify-between gap-4 py-2 pr-2 pl-4 md:px-6 md:py-3">
          <div className="flex items-center gap-5 md:gap-[22px]">
            <Link to="/today" aria-label="Dojo, home" className="flex min-h-11 items-center gap-2 text-fg no-underline md:gap-2.5">
              <Logo size={28} stroke={2.66} />
              <span className="font-mono text-base font-semibold md:text-lg">dojo</span>
            </Link>
            <TopNav />
          </div>
          <div className="flex items-center gap-2.5">
            <NotificationsBell />
            <button
              type="button"
              onClick={onSignOut}
              disabled={busy}
              className="h-11 rounded-[10px] px-3 text-sm font-bold text-fg2 hover:text-fg disabled:opacity-45 md:h-10"
            >
              Sign out
            </button>
            <Link
              to="/settings"
              aria-label={`Settings, @${me.username}`}
              className="inline-flex size-11 items-center justify-center rounded-full md:size-10"
            >
              <span
                className="inline-flex size-[30px] items-center justify-center rounded-full text-[13px] font-extrabold text-white uppercase md:size-8"
                style={{ background: 'var(--hlbg)' }}
                aria-hidden="true"
              >
                {me.username.slice(0, 1)}
              </span>
            </Link>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-[1200px] px-4 pt-6 pb-28 md:px-6 md:py-9">
        <p role="status" aria-live="polite" className="m-0 text-[13px] font-semibold text-bad empty:hidden">
          {failed ? 'Could not sign out. Please try again.' : ''}
        </p>
        {children}
      </main>
      <TabBar />
    </div>
  );
}
