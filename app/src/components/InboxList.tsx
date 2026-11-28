import { useEffect, useRef } from 'react';
import { Link } from 'react-router';
import { formatInboxTime, groupInbox, safeInboxUrl, useInboxActions, useInboxList, type InboxItem } from '../lib/inbox';

const AVATAR = 'inline-flex size-8 flex-none items-center justify-center rounded-full text-sm font-extrabold';

/** Round glyph per kind: text, never color alone. A failed check reads "Check failed" in its title. */
function Glyph({ item }: { item: InboxItem }) {
  switch (item.kind) {
    case 'check':
      return /^check failed/i.test(item.title) ? (
        <span aria-hidden="true" className={`${AVATAR} bg-badsoft text-bad`}>
          ✗
        </span>
      ) : (
        <span aria-hidden="true" className={`${AVATAR} bg-oksoft text-ok`}>
          ✓
        </span>
      );
    case 'story':
      return (
        <span aria-hidden="true" className={`${AVATAR} bg-storybg text-storyink`}>
          {item.title.slice(0, 1).toUpperCase()}
        </span>
      );
    case 'quiz':
      return (
        <span aria-hidden="true" className={`${AVATAR} bg-accentsoft text-accentink`}>
          ?
        </span>
      );
    case 'mark':
    case 'belt':
      return (
        <span aria-hidden="true" className={`${AVATAR} bg-accentsoft text-accentink`}>
          ★
        </span>
      );
    default:
      return (
        <span aria-hidden="true" className={`${AVATAR} bg-surface2 text-fg2`}>
          !
        </span>
      );
  }
}

const ROW = 'flex min-h-11 w-full items-start gap-3 rounded-xl px-3.5 py-3 text-left text-fg no-underline';

function Row({ item, now, onDone }: { item: InboxItem; now: Date; onDone?: () => void }) {
  const { markRead } = useInboxActions();
  const href = safeInboxUrl(item.url);
  const cls = `${ROW} ${item.read ? '' : 'bg-accentsoft'}`;
  const activate = () => {
    if (!item.read) void markRead(item.id);
    onDone?.();
  };
  const inner = (
    <>
      <Glyph item={item} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className={`text-sm ${item.read ? 'font-semibold' : 'font-extrabold'}`}>{item.title}</span>
        <span className="text-[13px] leading-[1.45] text-fg2">{item.body}</span>
      </span>
      <span className="flex flex-none flex-col items-end gap-1.5">
        <span className="font-mono text-[11px] text-muted">{formatInboxTime(item.at, now)}</span>
        {!item.read && (
          <>
            <span aria-hidden="true" className="size-2 rounded-full bg-accent" />
            <span className="sr-only">Unread</span>
          </>
        )}
      </span>
    </>
  );
  return (
    <li>
      {href ? (
        <Link to={href} onClick={activate} className={cls} data-inbox-row="">
          {inner}
        </Link>
      ) : (
        // A url that is not a path on this app is never followed; the row only marks itself read.
        <button type="button" onClick={activate} className={cls} data-inbox-row="">
          {inner}
        </button>
      )}
    </li>
  );
}

/** "Mark all read" (N1). Always rendered so focus never lands on a vanished control. */
export function MarkAllRead({ unread }: { unread: number }) {
  const { markAllRead } = useInboxActions();
  return (
    <button
      type="button"
      onClick={() => {
        if (unread > 0) void markAllRead();
      }}
      className="inline-flex min-h-11 items-center px-1 text-[13px] font-bold text-accentink md:min-h-9"
    >
      Mark all read
    </button>
  );
}

/** The inbox list shared by the bell dropdown and the phone page: groups, states and paging. */
export function InboxList({ onNavigate, now = new Date() }: { onNavigate?: () => void; now?: Date }) {
  const list = useInboxList();
  const box = useRef<HTMLDivElement>(null);
  // Rows loaded before a "Show older" click; once the next page lands, focus moves to the first new row.
  const focusFrom = useRef<number | null>(null);
  const loading = useRef(false);
  useEffect(() => {
    const from = focusFrom.current;
    if (from === null) return;
    focusFrom.current = null;
    const rows = box.current?.querySelectorAll<HTMLElement>('[data-inbox-row]');
    (rows?.[from] ?? box.current)?.focus();
  }, [list.data]);
  const showOlder = async () => {
    // A ref, not isFetchingNextPage: two clicks in one tick both see the old render.
    if (loading.current) return;
    loading.current = true;
    focusFrom.current = list.data?.pages.reduce((n, p) => n + p.items.length, 0) ?? 0;
    const res = await list.fetchNextPage().finally(() => {
      loading.current = false;
    });
    if (res.isError) {
      focusFrom.current = null;
      box.current?.focus();
    }
  };
  if (list.isPending) {
    return (
      <p role="status" className="m-0 px-3.5 py-6 text-sm text-muted">
        Loading...
      </p>
    );
  }
  if (list.isError) {
    return (
      <div role="alert" className="px-3.5 py-4 text-sm">
        <p className="m-0 text-bad">Could not load this. Please try again.</p>
        <button
          type="button"
          onClick={() => void list.refetch()}
          className="mt-2 h-11 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold md:h-9"
        >
          Retry
        </button>
      </div>
    );
  }
  const items = list.data.pages.flatMap((p) => p.items);
  if (items.length === 0) return <p className="m-0 px-3.5 py-6 text-sm text-muted">Nothing new</p>;
  return (
    <div ref={box} tabIndex={-1} className="px-1.5 pb-1.5 outline-none">
      {groupInbox(items, now).map((g) => (
        <div key={g.label} role="group" aria-label={g.label}>
          <div aria-hidden="true" className="px-3.5 pt-2.5 pb-1 font-mono text-xs tracking-[0.6px] text-muted uppercase">
            {g.label}
          </div>
          <ul className="m-0 list-none p-0">
            {g.items.map((item) => (
              <Row key={item.id} item={item} now={now} onDone={onNavigate} />
            ))}
          </ul>
        </div>
      ))}
      {list.hasNextPage && (
        <button
          type="button"
          // Stays focusable while it loads (aria-disabled, clicks ignored), so focus is never on a disabled control.
          aria-disabled={list.isFetchingNextPage}
          onClick={() => void showOlder()}
          className="mt-1 flex min-h-11 w-full items-center justify-center rounded-xl text-[13px] font-bold text-accentink aria-disabled:opacity-45"
        >
          {list.isFetchingNextPage ? 'Loading...' : 'Show older'}
        </button>
      )}
    </div>
  );
}
