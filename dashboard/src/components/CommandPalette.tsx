import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  CalendarClock,
  CalendarPlus,
  CornerDownLeft,
  Hash,
  Layers,
  ListChecks,
  Plus,
  Search,
  type LucideIcon,
} from 'lucide-react';
import { useNavigate } from 'react-router';
import { cn } from 'cn';
import { listQueuesOptions, listSchedulesOptions } from '../api/@tanstack/react-query.gen';
import { useSlots } from '../context';
import { EditPolicy } from '../pages/queues/EditPolicy';
import { EditSchedule } from '../pages/schedules/EditSchedule';
import { Dialog, DialogContent, DialogTitle } from '../primitives/dialog';
import { ITEMS } from '../lib/nav';

type Item = {
  key: string;
  group: string;
  label: string;
  hint?: string;
  icon: LucideIcon;
  mono?: boolean;
  run: () => void;
};

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
const SHORTCUT = isMac ? '⌘K' : 'Ctrl K';

/**
 * Jump anywhere from the keyboard (⌘K / Ctrl K): pages, queues, schedules,
 * a task or run by ID, tasks by idempotency key, and creating a queue or schedule.
 */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [create, setCreate] = useState<'queue' | 'schedule'>();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === 'k' && (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="hidden h-8 w-64 cursor-pointer items-center gap-2 rounded-md border border-input bg-surface px-2.5 text-sm text-faint hover:text-muted-foreground md:flex"
      >
        <Search className="size-4" />
        <span className="flex-1 text-left">Search or jump to…</span>
        <kbd className="font-mono text-xs">{SHORTCUT}</kbd>
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        {open && <Palette onClose={() => setOpen(false)} onCreate={setCreate} />}
      </Dialog>
      <EditPolicy open={create === 'queue'} onClose={() => setCreate(undefined)} />
      <EditSchedule open={create === 'schedule'} onClose={() => setCreate(undefined)} />
    </>
  );
}

function Palette({ onClose, onCreate }: { onClose: () => void; onCreate: (what: 'queue' | 'schedule') => void }) {
  const navigate = useNavigate();
  const { nav } = useSlots();
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const queues = useQuery(listQueuesOptions());
  const schedules = useQuery(listSchedulesOptions());

  const go = (to: string) => () => {
    onClose();
    navigate(to);
  };
  const q = query.trim();
  const items = filter(
    [
      ...[...ITEMS, ...nav].map((p) => ({
        key: `page:${p.to}`,
        group: 'Go to',
        label: p.label,
        icon: p.icon,
        run: go(p.to),
      })),
      ...(queues.data?.queues ?? []).map((x) => ({
        key: `queue:${x.name}`,
        group: 'Queues',
        label: x.name,
        hint: x.paused ? 'paused' : undefined,
        icon: Layers,
        mono: true,
        run: go(`/queues/${encodeURIComponent(x.name)}`),
      })),
      ...(schedules.data?.schedules ?? []).map((x) => ({
        key: `schedule:${x.name}`,
        group: 'Schedules',
        label: x.name,
        hint: x.cron,
        icon: CalendarClock,
        mono: true,
        run: go(`/schedules/${encodeURIComponent(x.name)}`),
      })),
      ...(
        [
          ['queue', 'New queue', Plus],
          ['schedule', 'New schedule', CalendarPlus],
        ] as const
      ).map(([what, label, icon]) => ({
        key: `new:${what}`,
        group: 'Create',
        label,
        icon,
        run: () => {
          onClose();
          onCreate(what);
        },
      })),
    ],
    q,
  );
  // What was typed may be an ID or an idempotency key rather than a name.
  if (/^(task|run)_\S+$/.test(q)) {
    const run = q.startsWith('run_');
    items.unshift({
      key: 'id',
      group: 'Open',
      label: q,
      hint: run ? 'run' : 'task',
      icon: Hash,
      mono: true,
      run: go(`/${run ? 'runs' : 'tasks'}/${encodeURIComponent(q)}`),
    });
  } else if (q) {
    items.push({
      key: 'idempotency',
      group: 'Find tasks',
      label: `Idempotency key “${q}”`,
      icon: ListChecks,
      run: go(`/tasks?${new URLSearchParams({ idempotency_key: q })}`),
    });
  }
  const current = Math.min(active, items.length - 1);

  useEffect(() => {
    listRef.current?.querySelector('[data-active]')?.scrollIntoView({ block: 'nearest' });
  }, [current]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive((current + step + items.length) % Math.max(1, items.length));
    } else if (e.key === 'Enter' && items[current]) {
      e.preventDefault();
      items[current].run();
    }
  };

  return (
    <DialogContent className="top-[15vh] w-[min(36rem,calc(100vw-2rem))] translate-y-0 gap-0 overflow-hidden p-0 [&>button:last-child]:hidden">
      <DialogTitle className="sr-only">Search or jump to</DialogTitle>
      <div className="flex items-center gap-2 border-b border-border px-4">
        <Search className="size-4 shrink-0 text-faint" />
        <input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          placeholder="Pages, queues, schedules, a task or run ID…"
          aria-label="Search"
          className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-faint"
        />
      </div>
      <div ref={listRef} role="listbox" className="max-h-[50vh] overflow-y-auto p-2">
        {items.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-faint">Nothing matches “{q}”.</p>
        ) : (
          items.map((item, i) => (
            <div key={item.key}>
              {item.group !== items[i - 1]?.group && (
                <div className="px-2 pt-2 pb-1 text-xs text-faint">{item.group}</div>
              )}
              <div
                role="option"
                aria-selected={i === current}
                data-active={i === current ? '' : undefined}
                onMouseMove={() => i !== current && setActive(i)}
                onClick={item.run}
                className={cn(
                  'flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-sm text-muted-foreground',
                  i === current && 'bg-muted text-foreground [&_svg]:text-primary',
                )}
              >
                <item.icon className="size-4 shrink-0" />
                <span className={cn('truncate', item.mono && 'font-mono text-xs')}>{item.label}</span>
                {item.hint && <span className="truncate font-mono text-xs text-faint">{item.hint}</span>}
                {i === current && <CornerDownLeft className="ml-auto size-3.5 shrink-0 text-faint" />}
              </div>
            </div>
          ))
        )}
      </div>
    </DialogContent>
  );
}

/** Items whose label contains every word typed, those starting with it first, keeping groups together. */
function filter(items: Item[], q: string): Item[] {
  if (!q) return items;
  const words = q.toLowerCase().split(/\s+/);
  const matches = items.filter((i) => words.every((w) => i.label.toLowerCase().includes(w)));
  const groups = [...new Set(matches.map((i) => i.group))];
  const starts = (i: Item) => (i.label.toLowerCase().startsWith(words[0]) ? 0 : 1);
  return groups.flatMap((g) => matches.filter((i) => i.group === g).sort((a, b) => starts(a) - starts(b)));
}
