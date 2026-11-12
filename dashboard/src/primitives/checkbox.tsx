import * as React from 'react';
import { Check, Minus } from 'lucide-react';
import { cn } from 'cn';

/**
 * A native checkbox drawn with the tokens: the browser's own is pure white in
 * dark mode, the one thing on the page that is.
 */
function Checkbox({
  className,
  indeterminate = false,
  ...props
}: Omit<React.ComponentProps<'input'>, 'type'> & { indeterminate?: boolean }) {
  return (
    <span className={cn('relative inline-grid size-3.5 shrink-0 align-middle', className)}>
      <input
        type="checkbox"
        data-slot="checkbox"
        ref={(el) => {
          if (el) el.indeterminate = indeterminate;
        }}
        className="peer size-3.5 cursor-pointer appearance-none rounded-[4px] border border-foreground/35 bg-card transition-colors outline-none checked:border-primary checked:bg-primary indeterminate:border-primary indeterminate:bg-primary hover:border-foreground/60 focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
        {...props}
      />
      <Check
        strokeWidth={3}
        className="pointer-events-none absolute inset-0.5 hidden size-2.5 text-primary-foreground peer-checked:block peer-indeterminate:hidden"
      />
      <Minus
        strokeWidth={3}
        className="pointer-events-none absolute inset-0.5 hidden size-2.5 text-primary-foreground peer-indeterminate:block"
      />
    </span>
  );
}

export { Checkbox };
