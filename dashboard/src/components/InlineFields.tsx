import type { ReactNode } from 'react';

/**
 * One line of label–value pairs, compact enough to sit under a page's title:
 * what the entity is set to (a queue's policy, a schedule's delivery), as
 * against how it's doing. Children follow the last pair, e.g. an edit button.
 */
export function InlineFields({
  items,
  children,
}: {
  /** Values are mono, as machine values; `prose` ones ("only 408 and 429") are words, in the body font. */
  items: { label: string; value: ReactNode; prose?: boolean }[];
  children?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
      <dl className="contents">
        {items.map(({ label, value, prose }) => (
          <div key={label} className="flex items-baseline gap-1.5 text-xs">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className={prose ? undefined : 'font-mono'}>{value}</dd>
          </div>
        ))}
      </dl>
      {children}
    </div>
  );
}
