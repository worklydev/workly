import { useMemo, useState } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { useNavigate, useSearchParams } from 'react-router';
import { listQueuesOptions, searchTasksInfiniteOptions } from '../../api/@tanstack/react-query.gen';
import type { SearchTasksData, TaskState } from '../../api';
import { PageHeader } from '../../components/PageHeader';
import { SortSummary } from '../../components/SortSummary';
import { TaskTable } from '../../components/TaskTable';
import { taskPaging } from '../../lib/tasks';
import { useTaskSort } from '../../lib/taskSort';
import { LIVE } from '../../lib/api';
import { useNow } from '../../hooks/useNow';
import { Input } from '../../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../primitives/select';
import { PAGE_SIZE } from '../../hooks/useListPage';

const STATE_ITEMS = [
  { value: 'all', label: 'All states' },
  ...(['pending', 'running', 'succeeded', 'failed', 'cancelled'] satisfies TaskState[]).map((s) => ({
    value: s,
    label: s.charAt(0).toUpperCase() + s.slice(1),
  })),
];

const HOUR = 3_600_000;
const SINCE_ITEMS = [
  { value: 'all', label: 'Any time', ms: 0 },
  { value: '1h', label: 'Last hour', ms: HOUR },
  { value: '24h', label: 'Last 24 hours', ms: 24 * HOUR },
  { value: '7d', label: 'Last 7 days', ms: 7 * 24 * HOUR },
];

export function TasksPage() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const key = params.get('idempotency_key') ?? '';
  const queue = params.get('queue') ?? 'all';
  const state = params.get('state') ?? 'all';
  const since = params.get('since') ?? 'all';
  const [search, setSearch] = useState(key);
  // Follow the URL when the header search changes it.
  const [shownKey, setShownKey] = useState(key);
  if (key !== shownKey) {
    setShownKey(key);
    setSearch(key);
  }

  const set = (name: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value && value !== 'all') next.set(name, value);
    else next.delete(name);
    setParams(next, { replace: true });
  };

  // The cutoff moves once a minute, not on every render, so the query key
  // stays stable between polls.
  const minute = Math.floor(useNow(60_000) / 60_000) * 60_000;
  const sorting = useTaskSort(state as TaskState | 'all');
  const { sort, order } = sorting.query;
  const query = useMemo(() => {
    const q: SearchTasksData['query'] = { sort, order, limit: PAGE_SIZE };
    if (key) q.idempotency_key = key;
    if (queue !== 'all') q.queue = queue;
    if (state !== 'all') q.state = state as TaskState;
    const ms = SINCE_ITEMS.find((i) => i.value === since)?.ms;
    if (ms) q.created_after = new Date(minute - ms).toISOString();
    return q;
  }, [key, queue, state, since, minute, sort, order]);

  const pages = useInfiniteQuery({ ...searchTasksInfiniteOptions({ query }), ...taskPaging, ...LIVE });
  const queues = useQuery({ ...listQueuesOptions(), ...LIVE });
  const paused = new Set(queues.data?.queues.filter((q) => q.paused).map((q) => q.name));
  const queueItems = [
    { value: 'all', label: 'All queues' },
    ...(queues.data?.queues ?? []).map((q) => ({ value: q.name, label: q.name })),
  ];

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const q = search.trim();
    if (q.startsWith('task_')) navigate(`/tasks/${q}`);
    else set('idempotency_key', q);
  };

  return (
    <>
      <PageHeader title="Tasks" description="Search across all queues." />
      <div className="flex flex-wrap items-center gap-2">
        <form onSubmit={submit}>
          <Input
            icon={<Search />}
            placeholder="Task ID or idempotency key"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onBlur={() => !search.trim().startsWith('task_') && set('idempotency_key', search.trim())}
            className="w-72"
          />
        </form>
        <Filter items={queueItems} value={queue} onChange={(v) => set('queue', v)} className="w-40" />
        <Filter items={STATE_ITEMS} value={state} onChange={(v) => set('state', v)} className="w-36" />
        <Filter items={SINCE_ITEMS} value={since} onChange={(v) => set('since', v)} className="w-40" />
        <div className="ml-auto">
          <SortSummary sorting={sorting} />
        </div>
      </div>
      <TaskTable pages={pages} showQueue sorting={sorting} paused={(q) => paused.has(q)} empty="No tasks match." />
    </>
  );
}

function Filter({
  items,
  value,
  onChange,
  className,
}: {
  items: { value: string; label: string }[];
  value: string;
  onChange: (value: string) => void;
  className?: string;
}) {
  return (
    <Select items={items} value={value} onValueChange={(v) => onChange(String(v ?? 'all'))}>
      <SelectTrigger className={className}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((item) => (
          <SelectItem key={item.value} value={item.value}>
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
