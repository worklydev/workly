import type { ReactNode } from 'react';

/** A titled group of label–value rows, for detail pages. Place groups side by side with PropertyGroups. */
export function Properties({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <h3 className="text-xs text-muted-foreground">{title}</h3>
      <dl className="grid grid-cols-[9rem_minmax(0,1fr)] gap-x-4 gap-y-2.5">{children}</dl>
    </section>
  );
}

export function Property({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate">{children}</dd>
    </>
  );
}

export function PropertyGroups({ children }: { children: ReactNode }) {
  return <div className="grid gap-x-12 gap-y-6 md:grid-cols-2">{children}</div>;
}
