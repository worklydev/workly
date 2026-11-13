import { ArrowRight } from 'lucide-react';
import { Link } from 'react-router';
import { cn } from 'cn';
import { RelativeTime } from '../../components/RelativeTime';
import { formatCount } from '../../lib/format';
import type { Issue, Status as StatusData } from '../../lib/home';

/** How many issues the panel lists; the rest are a link away. */
const SHOWN = 5;

const PANEL = {
  ok: 'border-border bg-card/50 dark:bg-card',
  idle: 'border-border bg-card/50 dark:bg-card',
  warning: 'border-warning/25 bg-warning/6',
  danger: 'border-destructive/25 bg-destructive/6',
} as const;

const DOT = {
  ok: 'bg-success',
  idle: 'bg-faint',
  warning: 'bg-warning-fill',
  danger: 'bg-destructive',
} as const;

const WHAT = {
  danger: 'text-destructive',
  warning: 'text-warning',
  neutral: 'text-muted-foreground',
} as const;

/**
 * The top of Home: how the workspace is doing in one sentence, then one row
 * per queue or schedule that needs a look. It's the only place on Home that
 * talks about failures, so they're never counted twice.
 */
export function Status({ status, lastTaskAt }: { status: StatusData; lastTaskAt?: string }) {
  const { tone, title, issues, running } = status;
  const more = issues.length - SHOWN;
  return (
    <section role="status" className={cn('overflow-hidden rounded-lg border', PANEL[tone])}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
        <div className="flex min-w-0 grow items-center gap-3">
          <span className="relative flex size-2 shrink-0">
            {(tone === 'danger' || tone === 'warning') && (
              <span className={cn('absolute inset-0 animate-ping rounded-full opacity-60', DOT[tone])} />
            )}
            <span className={cn('relative size-2 rounded-full', DOT[tone])} />
          </span>
          <p className="min-w-0">
            {title}
            {tone === 'idle' && lastTaskAt && (
              <span className="text-muted-foreground">
                {' '}
                · last task <RelativeTime iso={lastTaskAt} />
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-4 text-xs">
          {running > 0 && (
            <Link
              to="/tasks?state=running"
              title="Tasks with a delivery in flight"
              className="flex items-center gap-1.5 text-muted-foreground hover:text-foreground"
            >
              <span className="size-1.5 animate-pulse rounded-full bg-primary" />
              <span className="font-mono tabular-nums">{formatCount(running)}</span> running now
            </Link>
          )}
          {tone === 'danger' && (
            <Link to="/failures" className="group/link flex items-center gap-1 text-destructive hover:underline">
              Failures
              <ArrowRight className="size-3 transition-transform group-hover/link:translate-x-0.5" />
            </Link>
          )}
        </div>
      </div>
      {issues.length > 0 && (
        <ul className="border-t border-border/70">
          {issues.slice(0, SHOWN).map((issue) => (
            <IssueRow key={issue.key} issue={issue} />
          ))}
          {more > 0 && (
            <li className="border-t border-border/70 px-4 py-2 text-xs text-faint">
              <Link to="/failures" className="hover:text-foreground">
                and {more} more
              </Link>
            </li>
          )}
        </ul>
      )}
    </section>
  );
}

function IssueRow({ issue: i }: { issue: Issue }) {
  return (
    <li className="border-t border-border/70 first:border-t-0">
      <Link
        to={i.to}
        title={i.detail}
        className="group/row flex h-10 items-center gap-3 px-4 text-sm transition-colors hover:bg-muted/50"
      >
        {/* Where the summary's dot is, so names line up under the sentence. */}
        <span className="w-2 shrink-0" />
        <span className="w-36 shrink-0 truncate font-mono text-xs" title={i.name}>
          {i.name}
        </span>
        <span className="flex min-w-0 grow items-baseline gap-2">
          <span className={cn('shrink-0', WHAT[i.tone])}>{i.what}</span>
          {i.detail && <span className="min-w-0 truncate text-muted-foreground">{i.detail}</span>}
          {i.more && <span className="hidden shrink-0 text-xs text-faint sm:inline">{i.more}</span>}
        </span>
        {i.at && (
          <span className="hidden shrink-0 text-xs text-faint sm:inline">
            <RelativeTime iso={i.at} />
          </span>
        )}
        <ArrowRight className="size-3.5 shrink-0 text-faint opacity-0 transition-opacity group-hover/row:opacity-100" />
      </Link>
    </li>
  );
}
