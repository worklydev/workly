import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Lock } from 'lucide-react';
import { useParams, useSearchParams } from 'react-router';
import {
  closeGroupMutation,
  getGroupOptions,
  listQueuesOptions,
  searchTasksInfiniteOptions,
} from '../../api/@tanstack/react-query.gen';
import type { Group, SearchTasksData, TaskState } from '../../api';
import { CallbackSection } from '../../components/CallbackSection';
import { CopyButton } from '../../components/CopyButton';
import { DisabledReason } from '../../components/DisabledReason';
import { PageHeader } from '../../components/PageHeader';
import { InlineFields } from '../../components/InlineFields';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { Section } from '../../components/Section';
import { StatCard, StatStrip } from '../../components/StatCard';
import { GroupStatusBadge } from '../../components/states';
import { TaskTable } from '../../components/TaskTable';
import { PAGE_SIZE } from '../../hooks/useListPage';
import { errorMessage, LIVE } from '../../lib/api';
import { groupFinished } from '../../lib/groups';
import { taskPaging } from '../../lib/tasks';
import { useTaskSort } from '../../lib/taskSort';
import { Button } from '../../primitives/button';
import { Tabs, TabsList, TabsTab } from '../../primitives/tabs';
import { GroupProgress } from './GroupProgress';

const STATES: (TaskState | 'all')[] = ['all', 'pending', 'running', 'succeeded', 'failed', 'cancelled'];

export function GroupPage() {
  const id = useParams().id!;
  const { data: group, error, isPending } = useQuery({ ...getGroupOptions({ path: { id } }), ...LIVE });

  if (isPending) return <Muted>Loading…</Muted>;
  if (error) return <ErrorText error={error} />;

  return (
    <>
      <div className="flex flex-col gap-3">
        <PageHeader
          title={
            <span className="flex min-w-0 items-center gap-1">
              {group.name ? (
                <span className="truncate">{group.name}</span>
              ) : (
                <span className="truncate font-mono text-base">{group.id}</span>
              )}
              <CopyButton text={group.id} title="Copy group ID" />
            </span>
          }
          description={group.name && <span className="font-mono text-xs">{group.id}</span>}
          badge={<GroupStatusBadge status={group.status} />}
        >
          <CloseGroup group={group} />
        </PageHeader>
        <InlineFields
          items={[
            { label: 'Created', value: <RelativeTime iso={group.created_at} /> },
            { label: 'Closed', value: group.closed_at ? <RelativeTime iso={group.closed_at} /> : <None /> },
            { label: 'Completed', value: group.finished_at ? <RelativeTime iso={group.finished_at} /> : <None /> },
            { label: 'Idempotency key', value: group.idempotency_key ?? <None /> },
          ]}
        />
      </div>
      <Notice group={group} />
      <StatStrip className="grid-cols-2 sm:grid-cols-6">
        <StatCard
          className="col-span-2"
          label="Progress"
          value={
            <div className="flex h-7 items-center">
              <GroupProgress group={group} />
            </div>
          }
        />
        <StatCard label="Succeeded" value={group.succeeded} />
        <StatCard label="Failed" value={group.failed} tone={group.failed > 0 ? 'danger' : undefined} />
        <StatCard label="Cancelled" value={group.cancelled} tone={group.cancelled > 0 ? 'danger' : undefined} />
        <StatCard label="Remaining" value={group.remaining} />
      </StatStrip>
      <Callback group={group} />
      <Tasks group={group} />
    </>
  );
}

function None() {
  return <span className="text-faint">—</span>;
}

/** What the group is waiting for, so an open group isn't mistaken for a stuck one. */
function Notice({ group: g }: { group: Group }) {
  let text: string | undefined;
  if (g.status === 'open') {
    text = "Open: it accepts tasks, and completes only once it's closed. Close it when you've enqueued all its tasks.";
  } else if (g.status === 'running') {
    text = `Closed. It completes when its ${g.remaining} remaining ${g.remaining === 1 ? 'task finishes' : 'tasks finish'}.`;
  } else if (g.status === 'failed') {
    text = `${g.failed + g.cancelled} of its tasks failed or were cancelled. Retrying them now won't change the group: it has completed for good.`;
  }
  if (!text) return null;
  return <p className="text-sm text-muted-foreground">{text}</p>;
}

function CloseGroup({ group }: { group: Group }) {
  const qc = useQueryClient();
  const close = useMutation({
    ...closeGroupMutation(),
    onSuccess: () => qc.invalidateQueries(),
  });
  const reason = group.status === 'open' ? undefined : 'Already closed: it accepts no more tasks.';
  return (
    <>
      {close.error && <span className="text-xs text-destructive">{errorMessage(close.error)}</span>}
      <DisabledReason reason={reason}>
        <Button
          title={reason ? undefined : 'Stop accepting tasks, so the group completes once its tasks have finished.'}
          onClick={() => close.mutate({ path: { id: group.id } })}
          disabled={reason !== undefined || close.isPending}
        >
          <Lock />
          Close
        </Button>
      </DisabledReason>
    </>
  );
}

function Callback({ group: g }: { group: Group }) {
  if (!g.on_complete) {
    return (
      <Section title="Callback">
        <Muted>No callback. Set on_complete when creating a group to have Workly call you when it completes.</Muted>
      </Section>
    );
  }
  return (
    <CallbackSection
      callback={g.on_complete}
      taskId={g.callback_task_id}
      body={summary(g)}
      waiting="Waiting: it is enqueued when the group completes."
      note={
        <>
          Enqueued into <span className="font-mono">_callbacks</span> once, when the group completes, and delivered with
          that queue's retries. The body has the counts at completion.
        </>
      }
    />
  );
}

/** The callback's body as it is (or will be) sent. */
function summary(g: Group) {
  return {
    group_id: g.id,
    ...(g.name && { name: g.name }),
    status: groupFinished(g) ? g.status : '…',
    total: g.total,
    succeeded: g.succeeded,
    failed: g.failed,
    cancelled: g.cancelled,
  };
}

function Tasks({ group }: { group: Group }) {
  const [params, setParams] = useSearchParams();
  const state = (params.get('state') ?? 'all') as TaskState | 'all';
  const sorting = useTaskSort(state);
  const query: SearchTasksData['query'] = {
    group_id: group.id,
    ...sorting.query,
    limit: PAGE_SIZE,
  };
  if (state !== 'all') query.state = state;
  const pages = useInfiniteQuery({
    ...searchTasksInfiniteOptions({ query }),
    ...taskPaging,
    ...LIVE,
    placeholderData: keepPreviousData,
  });
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const paused = new Set(queues.data?.queues.filter((q) => q.paused).map((q) => q.name));
  const counts: Partial<Record<TaskState, number>> = {
    succeeded: group.succeeded,
    failed: group.failed,
    cancelled: group.cancelled,
  };

  return (
    <Section title="Tasks">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <Tabs
          value={state}
          onValueChange={(value) => {
            const next = new URLSearchParams(params);
            if (value === 'all') next.delete('state');
            else next.set('state', String(value));
            setParams(next, { replace: true });
          }}
        >
          <TabsList>
            {STATES.map((s) => (
              <TabsTab key={s} value={s} className="flex items-center gap-1.5">
                {s === 'all' ? 'All' : s.charAt(0).toUpperCase() + s.slice(1)}
                {s !== 'all' && (counts[s] ?? 0) > 0 && (
                  <span
                    className={s === 'failed' ? 'font-mono text-xs text-destructive' : 'font-mono text-xs text-faint'}
                  >
                    {counts[s]}
                  </span>
                )}
              </TabsTab>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <TaskTable
        pages={pages}
        showQueue
        sorting={sorting}
        paused={(q) => paused.has(q)}
        empty={state === 'all' ? 'No tasks in this group yet.' : `No ${state} tasks.`}
      />
      {groupFinished(group) && (
        <p className="text-xs text-faint">
          Tasks are listed until the server's retention deletes them; the counts above stay.
        </p>
      )}
    </Section>
  );
}
