import { useState, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router';
import { client } from './api/client.gen';
import { apiKeyAuth, type Auth } from './auth';
import { SlotsContext, type GettingStarted, type NavItem } from './context';
import { errorCode } from './lib/api';
import { useLiveUpdates } from './lib/live';
import { AppShell } from './components/AppShell';
import { DesignPage } from './pages/DesignPage';
import { AlertsPage } from './pages/AlertsPage';
import { FailuresPage } from './pages/FailuresPage';
import { GroupPage } from './pages/groups/GroupPage';
import { GroupsPage } from './pages/groups/GroupsPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { QueuePage } from './pages/queues/QueuePage';
import { QueuesPage } from './pages/queues/QueuesPage';
import { RunPage } from './pages/schedules/RunPage';
import { SchedulePage } from './pages/schedules/SchedulePage';
import { SchedulesPage } from './pages/schedules/SchedulesPage';
import { TaskPage } from './pages/tasks/TaskPage';
import { TasksPage } from './pages/tasks/TasksPage';

export type DashboardProps = {
  /** How API requests authenticate. Defaults to an API key prompt. */
  auth?: Auth;
  /** Base URL of the Workly API. Defaults to the page's origin. */
  apiUrl?: string;
  /** URL path the dashboard is served under, e.g. "/app". */
  basename?: string;
  /** Extra <Route> elements, rendered inside the dashboard's shell. */
  routes?: ReactNode;
  /** Extra sidebar items, below the built-in ones. */
  nav?: NavItem[];
  /** Extra header content, e.g. an organization switcher. */
  header?: ReactNode;
  /**
   * Shown on the Queues and Schedules pages while there are none, instead
   * of the `workly dev` examples.
   */
  gettingStarted?: GettingStarted;
  /** Share a QueryClient with the wrapping app. */
  queryClient?: QueryClient;
};

/**
 * The Workly dashboard. The open-source build renders it as is; a hosted
 * build wraps it with its own auth, pages and navigation.
 */
export function Dashboard({
  auth = apiKeyAuth,
  apiUrl = '',
  basename,
  routes,
  nav = [],
  header,
  gettingStarted,
  queryClient,
}: DashboardProps) {
  const [unauthorized, setUnauthorized] = useState(false);
  const [queries] = useState(() => {
    client.setConfig({ baseUrl: apiUrl, auth: auth.token, credentials: auth.credentials });
    const onError = (error: unknown) => {
      if (errorCode(error) === 'unauthorized') setUnauthorized(true);
    };
    // Retry network failures once; an API error (4xx, 5xx) won't change on retry.
    const retry = (count: number, error: unknown) => errorCode(error) === undefined && count < 1;
    const qc = queryClient ?? new QueryClient({ defaultOptions: { queries: { retry } } });
    // Subscribing keeps a shared client's own handlers intact.
    qc.getQueryCache().subscribe((event) => {
      if (event.type === 'updated' && event.action.type === 'error') onError(event.action.error);
    });
    qc.getMutationCache().subscribe((event) => {
      if (event.type === 'updated' && event.action.type === 'error') onError(event.action.error);
    });
    return qc;
  });

  if (unauthorized) {
    const retry = () => {
      setUnauthorized(false);
      queries.resetQueries();
    };
    return <auth.Unauthorized retry={retry} />;
  }

  return (
    <QueryClientProvider client={queries}>
      <LiveUpdates />
      <SlotsContext value={{ nav, header, gettingStarted }}>
        <BrowserRouter basename={basename}>
          <Routes>
            <Route path="design" element={<DesignPage />} />
            <Route element={<AppShell />}>
              <Route index element={<Navigate to="queues" replace />} />
              <Route path="queues" element={<QueuesPage />} />
              <Route path="queues/:queue" element={<QueuePage />} />
              <Route path="tasks" element={<TasksPage />} />
              <Route path="tasks/:id" element={<TaskPage />} />
              <Route path="groups" element={<GroupsPage />} />
              <Route path="groups/:id" element={<GroupPage />} />
              <Route path="failures" element={<FailuresPage />} />
              <Route path="schedules" element={<SchedulesPage />} />
              <Route path="schedules/:schedule" element={<SchedulePage />} />
              <Route path="runs/:id" element={<RunPage />} />
              <Route path="alerts" element={<AlertsPage />} />
              {routes}
              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </SlotsContext>
    </QueryClientProvider>
  );
}

function LiveUpdates() {
  useLiveUpdates();
  return null;
}
