import { Navigate, type RouteObject } from 'react-router';
import { App, InboxRoute, MachinesRoute, PathRoute, PracticeRoute, QuizRoute, SettingsRoute, SignInRoute, StartRoute, StartStepRoute, TicketRoute, TodayRoute, YourAIRoute } from './App';
import { AnalyticsLayout, type PageHandle } from './components/AnalyticsLayout';

const page = (pattern: string): PageHandle => ({ page: pattern });

// Every route that should get a page view declares its pattern in `handle`; the catch-all declares none.
export const routes: RouteObject[] = [
  {
    element: <AnalyticsLayout />,
    children: [
      { path: '/', element: <App /> },
      { path: '/today', element: <TodayRoute />, handle: page('/today') },
      { path: '/settings', element: <SettingsRoute />, handle: page('/settings') },
      { path: '/settings/machines', element: <MachinesRoute />, handle: page('/settings/machines') },
      { path: '/settings/ai', element: <YourAIRoute />, handle: page('/settings/ai') },
      { path: '/path', element: <PathRoute />, handle: page('/path') },
      { path: '/practice', element: <PracticeRoute />, handle: page('/practice') },
      { path: '/inbox', element: <InboxRoute />, handle: page('/inbox') },
      { path: '/t/*', element: <TicketRoute />, handle: page('/t/*') },
      { path: '/quiz/*', element: <QuizRoute />, handle: page('/quiz/*') },
      { path: '/start', element: <StartRoute />, handle: page('/start') },
      { path: '/start/install', element: <StartStepRoute id="o2" />, handle: page('/start/install') },
      { path: '/start/doctor', element: <StartStepRoute id="o3" />, handle: page('/start/doctor') },
      { path: '/start/key', element: <StartStepRoute id="o4" />, handle: page('/start/key') },
      { path: '/start/first', element: <StartStepRoute id="o5" />, handle: page('/start/first') },
      { path: '/signin', element: <SignInRoute />, handle: page('/signin') },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
];
