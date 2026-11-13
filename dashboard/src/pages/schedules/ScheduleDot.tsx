import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { cn } from 'cn';
import type { Schedule } from '../../api';
import { RelativeTime } from '../../components/RelativeTime';

/**
 * A schedule's health as a dot: orange while paused, otherwise the color of
 * how its last run went (green succeeded, red failed, yellow skipped), hollow
 * before the first. What it means opens on hover, on a tap and from the
 * keyboard, as DisabledReason does.
 */
export function ScheduleDot({ schedule: s }: { schedule: Schedule }) {
  const run = s.last_run;
  const { color, text } = s.paused
    ? { color: 'bg-primary', text: 'Paused' }
    : !run
      ? { color: 'border border-faint bg-transparent', text: 'No runs yet' }
      : run.state === 'succeeded'
        ? { color: 'bg-success', text: 'Last run succeeded' }
        : run.state === 'failed'
          ? { color: 'bg-destructive', text: 'Last run failed' }
          : run.state === 'skipped'
            ? { color: 'bg-warning-fill', text: 'Last run was skipped' }
            : { color: 'bg-muted-foreground/50', text: `Last run ${run.state}` };
  const when = s.paused ? s.paused_at : (run?.finished_at ?? run?.scheduled_for);

  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        openOnHover
        delay={100}
        nativeButton={false}
        aria-label={text}
        render={<span tabIndex={0} onClick={(e) => e.stopPropagation()} />}
        className="-m-1.5 inline-flex shrink-0 cursor-default rounded-full p-1.5 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <span className={cn('size-2 rounded-full', color)} />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Positioner side="bottom" align="start" sideOffset={6} className="isolate z-50">
          <PopoverPrimitive.Popup className="flex max-w-64 origin-(--transform-origin) flex-col gap-0.5 rounded-md border border-input bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-lg shadow-black/20 outline-none duration-100 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0">
            <span>
              {text}
              {when && (
                <span className="text-muted-foreground">
                  {' '}
                  · <RelativeTime iso={when} />
                </span>
              )}
            </span>
            {!s.paused && run?.state === 'failed' && run.last_error && (
              <span className="font-mono text-muted-foreground">{run.last_error}</span>
            )}
          </PopoverPrimitive.Popup>
        </PopoverPrimitive.Positioner>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}
