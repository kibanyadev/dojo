import { AppFrame } from '../components/AppFrame';
import { InboxList, MarkAllRead } from '../components/InboxList';
import type { Me } from '../lib/me';
import { useUnread } from '../lib/inbox';

/** N1 on the phone (and by "See all" on desktop): the full inbox page at /inbox. */
export function InboxPage({ me }: { me: Me }) {
  const unread = useUnread().data ?? 0;
  return (
    <AppFrame me={me}>
      <div className="mx-auto max-w-[640px]">
        <div className="flex items-center justify-between px-2 pt-1.5 pb-1">
          <h1 className="m-0 text-[28px] font-extrabold tracking-[-0.8px]">Inbox</h1>
          <MarkAllRead unread={unread} />
        </div>
        <InboxList />
      </div>
    </AppFrame>
  );
}
