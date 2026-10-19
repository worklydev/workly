import { keepPreviousData, useInfiniteQuery } from '@tanstack/react-query';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { listGroupsInfiniteOptions } from '../../api/@tanstack/react-query.gen';
import type { Group, GroupStatus, ListGroupsData } from '../../api';
import { CodePanel } from '../../components/CodePanel';
import { CursorPager } from '../../components/ListControls';
import { PageHeader } from '../../components/PageHeader';
import { ErrorText, Muted } from '../../components/QueryState';
import { RelativeTime } from '../../components/RelativeTime';
import { GroupStatusBadge } from '../../components/states';
import { useCursorPages } from '../../hooks/useCursorPages';
import { PAGE_SIZE } from '../../hooks/useListPage';
import { LIVE } from '../../lib/api';
import { groupPaging, type GroupPages } from '../../lib/groups';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { Tabs, TabsList, TabsTab } from '../../primitives/tabs';
import { GroupProgress } from './GroupProgress';

const STATUSES: (GroupStatus | 'all')[] = ['all', 'open', 'running', 'succeeded', 'failed'];

export function GroupsPage() {
  const [params, setParams] = useSearchParams();
  const status = (params.get('status') ?? 'all') as GroupStatus | 'all';
  const query: ListGroupsData['query'] = status === 'all' ? { limit: PAGE_SIZE } : { status, limit: PAGE_SIZE };
  const pages = useInfiniteQuery({
    ...listGroupsInfiniteOptions({ query }),
    ...groupPaging,
    ...LIVE,
    placeholderData: keepPreviousData,
  });

  return (
    <>
      <PageHeader
        title="Groups"
        description="Sets of tasks tracked together. Once a group is closed and all its tasks have finished, it enqueues its callback."
      />
      <Tabs
        value={status}
        onValueChange={(value) => {
          const next = new URLSearchParams(params);
          if (value === 'all') next.delete('status');
          else next.set('status', String(value));
          setParams(next, { replace: true });
        }}
      >
        <TabsList>
          {STATUSES.map((s) => (
            <TabsTab key={s} value={s}>
              {s === 'all' ? 'All' : s.charAt(0).toUpperCase() + s.slice(1)}
            </TabsTab>
          ))}
        </TabsList>
      </Tabs>
      <GroupTable pages={pages} status={status} />
    </>
  );
}

function GroupTable({ pages, status }: { pages: GroupPages; status: GroupStatus | 'all' }) {
  const navigate = useNavigate();
  const page = useCursorPages(pages, (p) => p.groups);
  if (pages.isPending) return <Muted>Loading…</Muted>;
  if (pages.error) return <ErrorText error={pages.error} />;
  if (page.items.length === 0) return status === 'all' ? <GettingStarted /> : <Muted>No {status} groups.</Muted>;

  return (
    <div className="flex flex-col gap-3">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Group</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Progress</TableHead>
            <TableHead className="text-right">Failed</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Completed</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {page.items.map((g) => (
            <TableRow key={g.id} className="cursor-pointer" onClick={() => navigate(`/groups/${g.id}`)}>
              <TableCell>
                <GroupName group={g} />
              </TableCell>
              <TableCell>
                <GroupStatusBadge status={g.status} />
              </TableCell>
              <TableCell>
                <GroupProgress group={g} />
              </TableCell>
              <TableCell className="text-right font-mono text-xs">
                {g.failed + g.cancelled > 0 ? (
                  <span className="text-destructive" title={`${g.failed} failed, ${g.cancelled} cancelled`}>
                    {g.failed + g.cancelled}
                  </span>
                ) : (
                  <span className="text-faint">0</span>
                )}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                <RelativeTime iso={g.created_at} />
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {g.finished_at ? <RelativeTime iso={g.finished_at} /> : <span className="text-faint">—</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <CursorPager page={page} noun="groups" />
    </div>
  );
}

function GroupName({ group: g }: { group: Group }) {
  return (
    <div className="flex flex-col">
      <Link
        to={`/groups/${g.id}`}
        className={g.name ? 'font-medium' : 'font-mono text-xs'}
        onClick={(e) => e.stopPropagation()}
      >
        {g.name ?? g.id}
      </Link>
      {g.name && <span className="font-mono text-xs text-faint">{g.id}</span>}
    </div>
  );
}

function GettingStarted() {
  const origin = window.location.origin;
  return (
    <div className="flex max-w-2xl flex-col gap-3">
      <p className="text-muted-foreground">
        No groups yet. Create one, enqueue tasks into it with <span className="font-mono text-xs">group_id</span>, then
        close it. When its tasks have all finished, Workly enqueues its callback with how they went:
      </p>
      <CodePanel title="Terminal">
        {`group=$(curl -s -X POST ${origin}/v1/groups -H 'Content-Type: application/json' \\
  -d '{"name": "hello", "on_complete": {"queue": "callbacks", "url": "${origin}/_dev/echo"}}' | jq -r .id)
curl -X POST ${origin}/v1/queues/hello/tasks -H 'Content-Type: application/json' \\
  -d "{\\"url\\": \\"${origin}/_dev/echo\\", \\"group_id\\": \\"$group\\"}"
curl -X POST ${origin}/v1/groups/$group/close`}
      </CodePanel>
    </div>
  );
}
