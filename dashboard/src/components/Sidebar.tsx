import { useState } from 'react';
import { Dialog as DialogPrimitive } from '@base-ui/react/dialog';
import { Menu, X } from 'lucide-react';
import { Link, useLocation } from 'react-router';
import { cn } from 'cn';
import { useSlots, type NavItem } from '../context';
import { ITEMS } from '../lib/nav';
import { useFrom } from '../lib/from';
import { Button } from '../primitives/button';
import { Logo } from './Logo';

export function Sidebar() {
  return (
    <aside className="hidden w-56 shrink-0 flex-col border-r border-border sm:flex">
      <SidebarContent />
    </aside>
  );
}

/**
 * Below `sm` the sidebar is hidden, so the header opens it as a drawer from
 * the left. Following a link closes it.
 */
export function MobileNav() {
  const [open, setOpen] = useState(false);
  return (
    <DialogPrimitive.Root open={open} onOpenChange={setOpen}>
      <DialogPrimitive.Trigger
        render={
          <Button variant="ghost" size="icon" aria-label="Open navigation" className="-ml-2 shrink-0 sm:hidden" />
        }
      >
        <Menu className="size-4" />
      </DialogPrimitive.Trigger>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop className="fixed inset-0 z-50 bg-black/40 duration-150 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0" />
        <DialogPrimitive.Popup
          aria-label="Navigation"
          className="fixed inset-y-0 left-0 z-50 flex w-64 max-w-[85vw] flex-col border-r border-input bg-background text-foreground shadow-lg shadow-black/20 outline-none duration-150 data-open:animate-in data-open:slide-in-from-left data-closed:animate-out data-closed:slide-out-to-left"
        >
          <SidebarContent onNavigate={() => setOpen(false)} />
          <DialogPrimitive.Close
            aria-label="Close navigation"
            className="absolute top-4 right-3 cursor-pointer rounded-md p-1 text-faint hover:bg-muted hover:text-foreground"
          >
            <X className="size-4" />
          </DialogPrimitive.Close>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const { nav } = useSlots();
  const location = useLocation();
  const from = useFrom();
  // A task opened from a queue belongs to Queues; a run always belongs to Schedules.
  const path =
    (location.pathname.startsWith('/tasks/') && from?.split('?')[0]) ||
    (location.pathname.startsWith('/runs/') && '/schedules') ||
    (location.pathname.startsWith('/workflow-runs/') && '/workflows') ||
    location.pathname;
  const items = [...ITEMS, ...nav];
  const active = items.find((item) => path === item.to || path.startsWith(item.to + '/'));

  return (
    <>
      <Link
        to="/home"
        onClick={onNavigate}
        className="flex h-14 shrink-0 items-center gap-1.5 px-4 font-mono tracking-[-0.02em]"
      >
        <Logo className="size-5" />
        Workly
      </Link>
      <nav className="flex flex-col gap-0.5 px-2">
        {items.map((item) => (
          <NavLink key={item.to} item={item} active={item === active} onClick={onNavigate} />
        ))}
      </nav>
    </>
  );
}

function NavLink({
  item: { to, label, icon: Icon },
  active,
  onClick,
}: {
  item: NavItem;
  active: boolean;
  onClick?: () => void;
}) {
  return (
    <Link
      to={to}
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'flex h-8 items-center gap-2.5 rounded-md px-2.5 transition-colors',
        active
          ? 'bg-muted text-foreground [&_svg]:text-primary'
          : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
      )}
    >
      <Icon className="size-4" />
      {label}
    </Link>
  );
}
