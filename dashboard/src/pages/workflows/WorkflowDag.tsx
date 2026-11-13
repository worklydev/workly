import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { cn } from 'cn';
import type { StepStatus } from '../../api';
import { StepStatusBadge } from '../../components/states';
import { layers, topoOrder } from '../../lib/workflows';

export type DagNode = {
  name: string;
  depends_on?: string[];
  /** A run's step has a status; a definition's doesn't. */
  status?: StepStatus;
  /** Why it is where it is, or what it calls. */
  detail?: ReactNode;
  /** Right of the name: a duration. */
  meta?: ReactNode;
  to?: string;
  /** Router state for `to`. */
  state?: unknown;
};

type Edge = { from: string; to: string; d: string };

const BORDER: Partial<Record<StepStatus, string>> = {
  running: 'border-primary/50',
  waiting: 'border-primary/50',
  failed: 'border-destructive/50',
};

/**
 * Steps as a DAG, left to right: a column per depth, edges drawn over them
 * in SVG, measured from the cards so they follow however the text wraps. An
 * edge is solid once its step has succeeded, so what's done reads at a
 * glance. On phones the same steps are a list, each after its dependencies.
 */
export function WorkflowDag({ nodes }: { nodes: DagNode[] }) {
  return (
    <>
      <DagColumns nodes={nodes} />
      <ol className="flex flex-col gap-2 sm:hidden">
        {topoOrder(nodes).map((n) => (
          <li key={n.name}>
            <StepCard node={n} after={n.depends_on} />
          </li>
        ))}
      </ol>
    </>
  );
}

/** An edge that skips columns passes each one through a gap of its own, rather than behind a card. */
type Via = { via: string };
const viaKey = (from: string, to: string, column: number) => `${from}>${to}@${column}`;

function DagColumns({ nodes }: { nodes: DagNode[] }) {
  const inner = useRef<HTMLDivElement>(null);
  const cards = useRef(new Map<string, HTMLElement>());
  const [edges, setEdges] = useState<Edge[]>([]);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const status = new Map(nodes.map((n) => [n.name, n.status]));
  // The edges' shape depends only on names and dependencies; statuses only recolor them.
  const shape = nodes.map((n) => `${n.name}<${(n.depends_on ?? []).join(',')}`).join(';');

  const columns: (DagNode | Via)[][] = layers(nodes);
  const rank = new Map(columns.flatMap((col, i) => col.map((n) => [(n as DagNode).name, i] as const)));
  for (const n of nodes) {
    for (const dep of n.depends_on ?? []) {
      const from = rank.get(dep);
      const to = rank.get(n.name);
      if (from === undefined || to === undefined) continue;
      for (let c = from + 1; c < to; c++) columns[c].push({ via: viaKey(dep, n.name, c) });
    }
  }

  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    const measure = () => {
      const box = el.getBoundingClientRect();
      const left = (r: DOMRect) => [r.left - box.left, r.top + r.height / 2 - box.top] as const;
      const right = (r: DOMRect) => [r.right - box.left, r.top + r.height / 2 - box.top] as const;
      const curve = ([x1, y1]: readonly [number, number], [x2, y2]: readonly [number, number]) => {
        const dx = Math.max(16, (x2 - x1) / 2);
        return `C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`;
      };
      const next: Edge[] = [];
      for (const n of nodes) {
        const to = cards.current.get(n.name)?.getBoundingClientRect();
        for (const dep of n.depends_on ?? []) {
          const from = cards.current.get(dep)?.getBoundingClientRect();
          if (!from || !to) continue;
          let at = right(from);
          let d = `M${at[0]},${at[1]}`;
          for (let c = (rank.get(dep) ?? 0) + 1; c < (rank.get(n.name) ?? 0); c++) {
            const via = cards.current.get(viaKey(dep, n.name, c))?.getBoundingClientRect();
            if (!via) continue;
            d += ` ${curve(at, left(via))}`;
            at = right(via);
            d += ` L${at[0]},${at[1]}`;
          }
          d += ` ${curve(at, left(to))}`;
          next.push({ from: dep, to: n.name, d });
        }
      }
      setEdges(next);
      setSize({ w: el.scrollWidth, h: el.scrollHeight });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `shape` stands for the nodes' layout
  }, [shape]);

  const register = (key: string) => (el: HTMLElement | null) => {
    if (el) cards.current.set(key, el);
    else cards.current.delete(key);
  };

  return (
    // Wide DAGs scroll sideways inside their frame, never the page.
    <div className="hidden overflow-x-auto pb-1 sm:block">
      <div ref={inner} className="relative flex w-max items-stretch gap-12">
        <svg aria-hidden className="pointer-events-none absolute inset-0" width={size.w} height={size.h}>
          {edges.map((e) => {
            const done = status.get(e.from) === 'succeeded';
            return (
              <path
                key={`${e.from}>${e.to}`}
                d={e.d}
                fill="none"
                strokeWidth={1.25}
                strokeDasharray={done || status.get(e.from) === undefined ? undefined : '3 3'}
                className={done ? 'stroke-success/60' : 'stroke-muted-foreground/40'}
              />
            );
          })}
        </svg>
        {columns.map((column, i) => (
          <div key={i} className="relative flex flex-col justify-center gap-3">
            {column.map((n) =>
              'via' in n ? (
                <div key={n.via} ref={register(n.via)} aria-hidden className="h-3 w-56" />
              ) : (
                <StepCard key={n.name} node={n} cardRef={register(n.name)} />
              ),
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function StepCard({
  node: n,
  after,
  cardRef,
}: {
  node: DagNode;
  /** In the phone list, which steps it follows. */
  after?: string[];
  cardRef?: (el: HTMLElement | null) => void;
}) {
  const body = (
    <>
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium">{n.name}</span>
        {n.meta && <span className="shrink-0 font-mono text-xs text-faint">{n.meta}</span>}
      </div>
      {n.status && (
        <div>
          <StepStatusBadge status={n.status} />
        </div>
      )}
      {n.detail && <div className="line-clamp-2 text-xs text-muted-foreground">{n.detail}</div>}
      {after && after.length > 0 && <div className="text-xs text-faint">after {after.join(', ')}</div>}
    </>
  );
  const className = cn(
    'flex w-full flex-col gap-1.5 rounded-md border bg-card px-3 py-2 sm:w-56',
    (n.status && BORDER[n.status]) ?? 'border-border',
    n.status === 'skipped' && 'opacity-60',
  );
  if (n.to) {
    return (
      <Link
        ref={cardRef}
        to={n.to}
        state={n.state}
        className={cn(
          className,
          'transition-colors outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40',
        )}
      >
        {body}
      </Link>
    );
  }
  return (
    <div ref={cardRef} className={className}>
      {body}
    </div>
  );
}
