import { Navigate } from 'react-router';
import { SuspendedError, useMe } from './lib/me';
import { onboardingRedirect, useOnboarding } from './lib/onboarding';
import { Start } from './pages/Start';
import { Doctor } from './pages/Doctor';
import { Key } from './pages/Key';
import { Install } from './pages/Install';
import { First } from './pages/First';
import { Today } from './pages/Today';
import { Settings } from './pages/Settings';
import { Machines } from './pages/Machines';
import { YourAI } from './pages/YourAI';
import { PathPage } from './pages/Path';
import { PracticePage } from './pages/Practice';
import { SignInPage } from './pages/SignIn';
import { TicketPage } from './pages/Ticket';
import { StoryPage } from './pages/Story';
import { QuizPage } from './pages/Quiz';
import { WritingPage } from './pages/Writing';
import { InboxPage } from './pages/Inbox';

function Loading() {
  return (
    <p role="status" className="p-6 font-sans text-muted">
      Loading...
    </p>
  );
}

function LoadFailed({ retry }: { retry: () => void }) {
  return (
    <div role="alert" className="p-6 font-sans">
      <p className="text-bad">Could not reach Dojo. Check your connection and try again.</p>
      <button type="button" onClick={retry} className="h-9 rounded-[10px] border border-line bg-surface px-3.5 text-sm font-bold">
        Retry
      </button>
    </div>
  );
}

// A suspended account goes to O1 with the suspended notice instead of a retry that cannot help.
function MeFailed({ error, retry }: { error: Error; retry: () => void }) {
  if (error instanceof SuspendedError) return <Navigate to="/signin?error=suspended" replace />;
  return <LoadFailed retry={retry} />;
}

// A signed-in learner who has not finished onboarding belongs on the step it names, never on an empty Today.
function useOnboardingRedirect(signedIn: boolean) {
  const onboarding = useOnboarding(signedIn);
  return { pending: signedIn && onboarding.isPending, to: onboardingRedirect(onboarding.data ?? null) };
}

// /app: signed out (401) goes to O1 at /app/signin; signed in goes to Today (L1 is home) or the first unfinished step.
export function App() {
  const me = useMe();
  const onboarding = useOnboardingRedirect(!!me.data);
  if (me.isPending || onboarding.pending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <Navigate to={onboarding.to ?? '/today'} replace />;
}

// /app/today: same guard as /app.
export function TodayRoute() {
  const me = useMe();
  const onboarding = useOnboardingRedirect(!!me.data);
  // Today draws its own loading state, so a slow onboarding read never holds it back; the redirect follows when it lands.
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  if (onboarding.to) return <Navigate to={onboarding.to} replace />;
  return <Today me={me.data} />;
}

// /app/start (O0): reachable signed out, so a failed or missing session is just "not signed in".
export function StartRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError && me.error instanceof SuspendedError) return <Navigate to="/signin?error=suspended" replace />;
  return <Start me={me.data ?? null} />;
}

// /app/start/install, doctor, key, first (O2 to O5): signed in only.
export function StartStepRoute({ id }: { id: 'o2' | 'o3' | 'o4' | 'o5' }) {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  if (id === 'o2') return <Install me={me.data} />;
  if (id === 'o3') return <Doctor />;
  if (id === 'o4') return <Key me={me.data} />;
  return <First />;
}

// /app/settings: same guard as /app.
export function SettingsRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <Settings me={me.data} />;
}

// /app/settings/machines: same guard as /app.
export function MachinesRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <Machines me={me.data} />;
}

// /app/settings/ai: same guard as /app.
export function YourAIRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <YourAI me={me.data} />;
}

// /app/path: same guard as /app.
export function PathRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <PathPage me={me.data} />;
}

// /app/practice: same guard as /app.
export function PracticeRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <PracticePage me={me.data} />;
}

// /app/inbox (N1, phone tab): same guard as /app.
export function InboxRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <InboxPage me={me.data} />;
}

// /app/t/<role>/<ticket>: same guard as /app.
export function TicketRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <TicketPage me={me.data} />;
}

// /app/story/<role>/<item>: same guard as /app.
export function StoryRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <StoryPage me={me.data} />;
}

// /app/quiz/<role>/<quiz>: same guard as /app.
export function QuizRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <QuizPage me={me.data} />;
}

// /app/w/<role>/<item>: same guard as /app.
export function WritingRoute() {
  const me = useMe();
  if (me.isPending) return <Loading />;
  if (me.isError) return <MeFailed error={me.error} retry={() => void me.refetch()} />;
  if (me.data === null) return <Navigate to="/signin" replace />;
  return <WritingPage me={me.data} />;
}

// /app/signin: a signed-in browser has nothing to do here.
export function SignInRoute() {
  const me = useMe();
  // A refetch that fails keeps the old data, so a learner suspended mid-session still has me.data.
  if (me.data && !me.isError) return <Navigate to="/today" replace />;
  return <SignInPage />;
}
