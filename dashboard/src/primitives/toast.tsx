import type { ReactNode } from 'react';
import { Toast as ToastPrimitive } from '@base-ui/react/toast';
import { X } from 'lucide-react';
import { cn } from 'cn';

/**
 * Short notices about something the user just did ("Started run_…"), in the
 * bottom right corner, gone after a few seconds. Errors stay until closed.
 */
export function Toaster({ children }: { children: ReactNode }) {
  return (
    <ToastPrimitive.Provider timeout={5000} limit={3}>
      {children}
      <ToastPrimitive.Portal>
        <ToastPrimitive.Viewport className="fixed right-4 bottom-4 z-50 flex w-[calc(100vw-2rem)] max-w-sm flex-col gap-2 outline-none">
          <Toasts />
        </ToastPrimitive.Viewport>
      </ToastPrimitive.Portal>
    </ToastPrimitive.Provider>
  );
}

function Toasts() {
  const { toasts } = ToastPrimitive.useToastManager();
  return toasts.map((t) => (
    <ToastPrimitive.Root
      key={t.id}
      toast={t}
      className={cn(
        'flex items-start gap-3 rounded-md border border-input bg-popover px-3 py-2.5 text-sm text-popover-foreground shadow-lg shadow-black/20 transition-[opacity,translate] duration-200',
        'data-ending-style:translate-y-2 data-ending-style:opacity-0 data-starting-style:translate-y-2 data-starting-style:opacity-0',
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <ToastPrimitive.Title className={cn('font-medium', t.type === 'error' && 'text-destructive')} />
        <ToastPrimitive.Description className="text-xs text-muted-foreground" />
      </div>
      <ToastPrimitive.Close aria-label="Close" className="-mr-1 rounded-xs p-0.5 text-faint hover:text-foreground">
        <X className="size-3.5" />
      </ToastPrimitive.Close>
    </ToastPrimitive.Root>
  ));
}
