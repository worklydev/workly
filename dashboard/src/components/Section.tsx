import type { ReactNode } from 'react';

export function Section({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <h3 className="text-xs text-muted-foreground">{title}</h3>
        {actions}
      </div>
      {children}
    </section>
  );
}
