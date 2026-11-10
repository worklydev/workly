import type { ReactNode } from 'react';
import { Popover as PopoverPrimitive } from '@base-ui/react/popover';

/**
 * Explains why the control inside is disabled. Actions stay visible when they
 * don't apply, so people can see they exist, and the reason opens on hover,
 * on a tap (touch has no hover) and from the keyboard (the wrapper takes
 * focus, since a disabled button can't). A disabled button gets no pointer
 * events itself, hence the wrapper.
 */
export function DisabledReason({ reason, children }: { reason?: string; children: ReactNode }) {
  if (!reason) return children;
  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        openOnHover
        delay={150}
        nativeButton={false}
        render={<span tabIndex={0} />}
        className="inline-flex cursor-not-allowed rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        {children}
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Positioner side="bottom" align="center" sideOffset={6} className="isolate z-50">
          <PopoverPrimitive.Popup className="max-w-64 origin-(--transform-origin) rounded-md border border-input bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-lg shadow-black/20 outline-none duration-100 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0">
            {reason}
          </PopoverPrimitive.Popup>
        </PopoverPrimitive.Positioner>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  );
}
