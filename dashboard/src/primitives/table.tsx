import * as React from "react"
import { ArrowDown, ArrowUp } from "lucide-react"
import { cn } from "cn"

// Flat table: no outer box, hairline row dividers, muted header.
function Table({ className, ...props }: React.ComponentProps<"table">) {
  return (
    <div data-slot="table-container" className="relative w-full overflow-x-auto">
      <table data-slot="table" className={cn("w-full text-sm", className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return <thead data-slot="table-header" className={cn("[&_tr]:hover:bg-transparent", className)} {...props} />
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return <tbody data-slot="table-body" className={className} {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn("border-b border-border transition-colors hover:bg-muted/60", className)}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        "h-9 px-3 text-left align-middle text-xs font-normal whitespace-nowrap text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}

// Clickable header for a sortable column; `direction` is set on the active column only.
// Inactive columns show a faint arrow on hover, pointing the way a click sorts.
function SortableTableHead({
  direction,
  next = "desc",
  onSort,
  children,
  ...props
}: React.ComponentProps<"th"> & {
  direction?: "asc" | "desc"
  /** The direction a click on an inactive column sorts in. */
  next?: "asc" | "desc"
  onSort: () => void
}) {
  const Arrow = (direction ?? next) === "asc" ? ArrowUp : ArrowDown
  return (
    <TableHead aria-sort={direction ? `${direction}ending` : undefined} {...props}>
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "group/sort -mx-1 inline-flex items-center gap-1 rounded-sm px-1 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40",
          direction && "text-foreground"
        )}
      >
        {children}
        <Arrow
          className={cn(
            "size-3 transition-opacity",
            direction
              ? "text-primary"
              : "text-faint opacity-0 group-hover/sort:opacity-100 group-focus-visible/sort:opacity-100"
          )}
        />
      </button>
    </TableHead>
  )
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return (
    <td
      data-slot="table-cell"
      className={cn("h-10 px-3 align-middle whitespace-nowrap", className)}
      {...props}
    />
  )
}

export { Table, TableHeader, TableBody, TableHead, SortableTableHead, TableRow, TableCell }
