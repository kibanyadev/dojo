import type { ReactNode } from 'react';
import { Link, NavLink } from 'react-router';
import { AppFrame } from './AppFrame';
import type { Me } from '../lib/me';

const navItem = 'flex h-11 flex-none items-center rounded-[10px] px-3 text-sm font-semibold no-underline md:h-10';

function SectionLink({ to, end, children }: { to: string; end?: boolean; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) => `${navItem} ${isActive ? 'bg-accentsoft text-accentink' : 'text-fg2 hover:text-fg'}`}
    >
      {children}
    </NavLink>
  );
}

/** The Settings frame: title, section nav (Account, Machines, Your AI), and the cards of the current section. */
export function SettingsLayout({ me, children }: { me: Me; children: ReactNode }) {
  return (
    <AppFrame me={me}>
      <div className="flex flex-col gap-6">
        <div className="flex flex-col gap-2">
          <Link to="/today" className="inline-flex min-h-11 items-center text-[13px] font-bold text-accentink no-underline md:min-h-6">
            &larr; Back to Today
          </Link>
          <h1 className="m-0 text-[32px] leading-[1.06] font-extrabold tracking-[-1px] md:text-[44px] md:tracking-[-1.5px]">Settings</h1>
        </div>
        <div className="grid items-start gap-4 md:grid-cols-[220px_1fr] md:gap-6">
          <nav aria-label="Settings sections" className="flex gap-1 overflow-x-auto md:flex-col">
            <SectionLink to="/settings" end>
              Account
            </SectionLink>
            <SectionLink to="/settings/machines">Machines</SectionLink>
            <SectionLink to="/settings/ai">Your AI</SectionLink>
          </nav>
          <div className="flex min-w-0 flex-col gap-4">{children}</div>
        </div>
      </div>
    </AppFrame>
  );
}
