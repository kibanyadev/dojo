import { useEffect, useId, useRef, useState } from 'react';
import { Link, useLocation } from 'react-router';
import { useUnread } from '../lib/inbox';
import { InboxList, MarkAllRead } from './InboxList';

const FOCUSABLE = 'a[href], button:not([disabled])';

/** The header bell (N1, desktop): a button that opens the notifications dialog under it. */
export function NotificationsBell() {
  const unread = useUnread().data ?? 0;
  // Open is tied to the page it opened on, so any navigation closes it without an effect.
  const { pathname } = useLocation();
  const [openAt, setOpenAt] = useState<string | null>(null);
  const open = openAt === pathname;
  const setOpen = (next: boolean) => setOpenAt(next ? pathname : null);
  const wrap = useRef<HTMLDivElement>(null);
  const bell = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const id = useId();

  useEffect(() => {
    if (!open) return;
    const first = dialog.current?.querySelector<HTMLElement>(FOCUSABLE) ?? dialog.current;
    first?.focus();
    const onDown = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpenAt(null);
    };
    // On the document, not the dialog: focus can fall to <body> when a control unmounts, and Escape and Tab must still work.
    const onKey = (e: KeyboardEvent) => {
      const box = dialog.current;
      if (!box) return;
      if (e.key === 'Escape') {
        setOpenAt(null);
        bell.current?.focus();
        return;
      }
      if (e.key !== 'Tab') return;
      const active = document.activeElement;
      const inside = active !== null && box.contains(active);
      // Focus on the bell itself tabs naturally into the dialog; anywhere else outside it is pulled back in.
      if (!inside && active !== document.body && active !== null) return;
      const nodes = [...box.querySelectorAll<HTMLElement>(FOCUSABLE)];
      if (nodes.length === 0) {
        e.preventDefault();
        box.focus();
        return;
      }
      const firstNode = nodes[0]!;
      const lastNode = nodes[nodes.length - 1]!;
      if (!inside) {
        e.preventDefault();
        (e.shiftKey ? lastNode : firstNode).focus();
      } else if (e.shiftKey && (active === firstNode || active === box)) {
        e.preventDefault();
        lastNode.focus();
      } else if (!e.shiftKey && active === lastNode) {
        e.preventDefault();
        firstNode.focus();
      }
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={wrap} className="relative hidden md:block">
      <button
        ref={bell}
        type="button"
        aria-label={unread > 0 ? `Notifications, ${unread} unread` : 'Notifications'}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen(!open)}
        className="relative inline-flex size-10 flex-none items-center justify-center rounded-[10px] border border-line bg-surface text-fg2 hover:text-fg"
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
          <path d="M10.3 21a1.9 1.9 0 0 0 3.4 0" />
        </svg>
        {unread > 0 && <span aria-hidden="true" className="absolute top-[7px] right-2 box-border size-[9px] rounded-full border-2 border-surface bg-accent" />}
      </button>
      {open && (
        <div
          ref={dialog}
          id={id}
          role="dialog"
          aria-label="Notifications"
          tabIndex={-1}
          className="absolute top-full right-0 z-20 mt-2 w-[400px] max-w-[calc(100vw-32px)] overflow-hidden rounded-2xl border border-line bg-surface shadow-card"
        >
          <div className="flex items-center justify-between px-4 pt-3.5 pb-2">
            <span className="text-base font-extrabold">Notifications</span>
            <MarkAllRead unread={unread} />
          </div>
          <div className="max-h-[min(60vh,480px)] overflow-y-auto">
            <InboxList onNavigate={() => setOpen(false)} />
          </div>
          <div className="flex items-center justify-between border-t border-line px-4 py-1.5 text-[13px]">
            <Link to="/settings" onClick={() => setOpen(false)} className="inline-flex min-h-9 items-center text-fg2 no-underline">
              Notification settings
            </Link>
            <Link to="/inbox" onClick={() => setOpen(false)} className="inline-flex min-h-9 items-center font-bold text-accentink no-underline">
              See all
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
