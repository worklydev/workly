import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router';
import { listGroupsOptions } from '../../api/@tanstack/react-query.gen';
import { RowHint } from '../../components/RowHint';
import { Section } from '../../components/Section';
import { GroupStatusBadge } from '../../components/states';
import { LIVE } from '../../lib/api';
import { shortId } from '../../lib/format';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../primitives/table';
import { GroupProgress } from '../groups/GroupProgress';

const SHOWN = 5;

/**
 * Groups still in progress: closed ones finishing their tasks, then open
 * ones with tasks still to finish. Hidden otherwise, since many workspaces
 * never use groups and a group with nothing left to do has nothing to show.
 */
export function GroupsOverview() {
  const navigate = useNavigate();
  const running = useQuery({ ...listGroupsOptions({ query: { status: 'running', limit: SHOWN } }), ...LIVE });
  const open = useQuery({ ...listGroupsOptions({ query: { status: 'open', limit: SHOWN } }), ...LIVE });
  const all = [...(running.data?.groups ?? []), ...(open.data?.groups ?? []).filter((g) => g.remaining > 0)];
  const groups = all.slice(0, SHOWN);
  if (groups.length === 0) return null;
  const more = Boolean(running.data?.next_cursor || open.data?.next_cursor) || all.length > groups.length;

  return (
    <Section title="Groups in progress" link={{ to: '/groups', label: more ? 'All groups' : 'Groups' }}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Group</TableHead>
            <TableHead>Progress</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {groups.map((g) => (
            <TableRow key={g.id} className="cursor-pointer" onClick={() => navigate(`/groups/${g.id}`)}>
              <TableCell className="w-full max-w-0 min-w-36">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/groups/${g.id}`}
                    title={g.id}
                    className={g.name ? 'truncate font-medium' : 'truncate font-mono text-xs'}
                    onClick={(e) => e.stopPropagation()}
                  >
                    {g.name ?? shortId(g.id)}
                  </Link>
                  <RowHint />
                </div>
              </TableCell>
              <TableCell>
                <GroupProgress group={g} />
              </TableCell>
              <TableCell>
                <GroupStatusBadge status={g.status} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Section>
  );
}
