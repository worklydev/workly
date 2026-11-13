import { useState } from 'react';
import { CalendarClock, CirclePause, Plus } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import type { Schedule } from '../../api';
import { EmptyState } from '../../components/EmptyState';
import { Section } from '../../components/Section';
import { useNow } from '../../hooks/useNow';
import { useRelativeTime } from '../../hooks/useRelativeTime';
import { describeCron } from '../../lib/cron';
import { formatDate } from '../../lib/format';
import { Button } from '../../primitives/button';
import { EditSchedule } from '../schedules/EditSchedule';

const SHOWN = 6;

type Slot = { schedule: Schedule; at: string };

/** Every schedule's next run, soonest first: a timeline of what's about to happen. */
export function ComingUp({ schedules }: { schedules: Schedule[] }) {
  const now = useNow(15_000);
  const [creating, setCreating] = useState(false);
  const slots: Slot[] = schedules
    .filter((s) => !s.paused)
    // Each schedule's next run, so a schedule firing every minute doesn't fill the list.
    .flatMap((s) => {
      const at = s.next_runs.find((t) => Date.parse(t) > now - 1000);
      return at ? [{ schedule: s, at }] : [];
    })
    .sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || a.schedule.name.localeCompare(b.schedule.name))
    .slice(0, SHOWN);

  return (
    <Section title="Coming up" link={{ to: '/schedules', label: 'Schedules' }}>
      <EditSchedule open={creating} onClose={() => setCreating(false)} />
      {schedules.length === 0 ? (
        <EmptyState
          icon={CalendarClock}
          title="No schedules yet"
          hint="A schedule calls a URL on a cron timetable. Its next runs line up here."
          action={
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus />
              New schedule
            </Button>
          }
        />
      ) : slots.length === 0 ? (
        <EmptyState
          icon={CirclePause}
          title={schedules.length === 1 ? 'The schedule is paused' : 'Every schedule is paused'}
          hint="Resume one and its next runs line up here."
        />
      ) : (
        <ol className="flex flex-col">
          {slots.map((slot, i) => (
            <SlotRow
              key={`${slot.schedule.name}\n${slot.at}`}
              slot={slot}
              first={i === 0}
              last={i === slots.length - 1}
            />
          ))}
        </ol>
      )}
    </Section>
  );
}

function SlotRow({ slot: { schedule: s, at }, first, last }: { slot: Slot; first: boolean; last: boolean }) {
  const relative = useRelativeTime(at);
  return (
    <li>
      <Link
        to={`/schedules/${encodeURIComponent(s.name)}`}
        className="group/row flex h-10 items-center gap-3 rounded-md px-1 transition-colors hover:bg-muted/60"
      >
        <span
          className={cn(
            'w-14 shrink-0 text-right font-mono text-xs tabular-nums',
            first ? 'text-primary' : 'text-muted-foreground',
          )}
        >
          {relative}
        </span>
        {/* The timeline: a rail through every slot, the next one marked in the accent. */}
        <span className="relative flex h-full w-3 shrink-0 items-center justify-center">
          <span
            className={cn('absolute w-px bg-border', first ? 'top-1/2' : 'top-0', last ? 'bottom-1/2' : 'bottom-0')}
          />
          <span
            className={cn(
              'relative size-2 rounded-full',
              first ? 'bg-primary ring-4 ring-primary/15' : 'border border-input bg-surface',
            )}
          />
        </span>
        <span className="min-w-0 grow truncate">
          <span className="font-mono text-xs">{s.name}</span>
          <span className="text-xs text-faint"> · {describeCron(s.cron) ?? s.cron}</span>
        </span>
        <span className="shrink-0 font-mono text-xs text-faint" title={s.timezone}>
          {slotTime(at)}
        </span>
      </Link>
    </li>
  );
}

const time = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });

/** Today's slots by time alone; later ones with their date. */
function slotTime(iso: string): string {
  const d = new Date(iso);
  return d.toDateString() === new Date().toDateString() ? time.format(d) : formatDate(iso, { seconds: false });
}
