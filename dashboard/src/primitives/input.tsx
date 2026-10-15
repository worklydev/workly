import * as React from "react"
import { Input as InputPrimitive } from "@base-ui/react/input"
import { cn } from "cn"

// Pass `icon` for a leading icon (e.g. search).
function Input({
  className,
  icon,
  ...props
}: React.ComponentProps<"input"> & { icon?: React.ReactNode }) {
  const input = (
    <InputPrimitive
      data-slot="input"
      className={cn(
        "h-8 w-full min-w-0 rounded-md border border-input bg-card px-2.5 text-sm transition-colors outline-none placeholder:text-faint hover:border-foreground/15 focus-visible:border-ring/60 focus-visible:ring-2 focus-visible:ring-ring/20 disabled:cursor-not-allowed disabled:opacity-50",
        icon && "pl-8",
        !icon && className
      )}
      {...props}
    />
  )
  if (!icon) return input

  return (
    <div className={cn("relative", className)}>
      <span className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-faint [&_svg]:size-3.5">
        {icon}
      </span>
      {input}
    </div>
  )
}

export { Input }
