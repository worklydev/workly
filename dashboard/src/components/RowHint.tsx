import { ArrowRight } from 'lucide-react';

/** Next to a row's name: an arrow that shows while the row is hovered, so a clickable row says it opens. */
export function RowHint() {
  return (
    <ArrowRight
      aria-hidden
      className="size-3.5 shrink-0 text-faint opacity-0 transition-opacity group-hover/row:opacity-100"
    />
  );
}
