import * as React from 'react';
import { cn } from 'cn';

// Multi-line counterpart of Input, monospaced for headers and bodies.
function Textarea({ className, ...props }: React.ComponentProps<'textarea'>) {
  return (
    <textarea
      data-slot="textarea"
      spellCheck={false}
      className={cn(
        'min-h-16 w-full min-w-0 rounded-md border border-input bg-card px-2.5 py-1.5 font-mono text-xs transition-colors outline-none placeholder:text-faint hover:border-foreground/15 focus-visible:border-ring/60 focus-visible:ring-2 focus-visible:ring-ring/20 disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
