import { Tabs as TabsPrimitive } from "@base-ui/react/tabs"
import { cn } from "cn"

// Underline tabs: a hairline under the list, the active tab in foreground with an orange underline.
const Tabs = TabsPrimitive.Root

function TabsList({ className, ...props }: TabsPrimitive.List.Props) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn("flex items-center gap-4 border-b border-border", className)}
      {...props}
    />
  )
}

function TabsTab({ className, ...props }: TabsPrimitive.Tab.Props) {
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-tab"
      className={cn(
        "-mb-px h-8 cursor-pointer border-b-2 border-transparent text-sm text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:text-foreground data-active:border-primary data-active:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTab }
