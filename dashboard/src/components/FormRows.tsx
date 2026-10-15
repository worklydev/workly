import type { ReactNode } from 'react';

/** Settings forms in dialogs: titled groups of label, control and hint rows. */
export function FormGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h3 className="text-xs text-muted-foreground">{title}</h3>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  );
}

export function FormRow({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="grid grid-cols-[9rem_minmax(0,1fr)] items-start gap-x-4">
      <span className="pt-1.5">{label}</span>
      <span className="flex flex-col gap-1">
        {children}
        {hint && <span className="text-xs text-faint">{hint}</span>}
      </span>
    </label>
  );
}

export function Unit({ unit, children }: { unit: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      {children}
      <span className="text-xs text-faint">{unit}</span>
    </span>
  );
}
