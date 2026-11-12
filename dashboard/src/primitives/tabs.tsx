import { Tabs as TabsPrimitive } from '@base-ui/react/tabs';
import { cn } from 'cn';

// Underline tabs: a hairline under the list, the active tab in foreground with an orange underline.
function Tabs({ className, ...props }: TabsPrimitive.Root.Props) {
  return <TabsPrimitive.Root className={cn('max-w-full min-w-0', className)} {...props} />;
}

function TabsList({ className, ...props }: TabsPrimitive.List.Props) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn(
        // More tabs than fit (a phone) scroll sideways. The hairline is a shadow, not
        // a border, so the scroll container doesn't clip the active tab's underline.
        'flex items-center gap-4 overflow-x-auto overflow-y-hidden shadow-[inset_0_-1px_0_var(--border)] [scrollbar-width:none]',
        className,
      )}
      {...props}
    />
  );
}

function TabsTab({ className, ...props }: TabsPrimitive.Tab.Props) {
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-tab"
      className={cn(
        'h-8 shrink-0 cursor-pointer border-b-2 border-transparent text-sm text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:text-foreground data-active:border-primary data-active:text-foreground',
        className,
      )}
      {...props}
    />
  );
}

export { Tabs, TabsList, TabsTab };
