import { Link, useLocation } from 'react-router';
import { cn } from 'cn';
import { useSlots, type NavItem } from '../context';
import { ITEMS } from '../lib/nav';
import { useFrom } from '../lib/from';
import { Logo } from './Logo';

export function Sidebar() {
  const { nav } = useSlots();
  const location = useLocation();
  const from = useFrom();
  // A task opened from a queue belongs to Queues; a run always belongs to Schedules.
  const path =
    (location.pathname.startsWith('/tasks/') && from?.split('?')[0]) ||
    (location.pathname.startsWith('/runs/') && '/schedules') ||
    location.pathname;
  const items = [...ITEMS, ...nav];
  const active = items.find((item) => path === item.to || path.startsWith(item.to + '/'));

  return (
    <aside className="hidden w-56 shrink-0 flex-col border-r border-border sm:flex">
      <Link to="/" className="flex h-14 items-center gap-1.5 px-4 font-mono tracking-[-0.02em]">
        <Logo className="size-5" />
        Workly
      </Link>
      <nav className="flex flex-col gap-0.5 px-2">
        {items.map((item) => (
          <NavLink key={item.to} item={item} active={item === active} />
        ))}
      </nav>
    </aside>
  );
}

function NavLink({ item: { to, label, icon: Icon }, active }: { item: NavItem; active: boolean }) {
  return (
    <Link
      to={to}
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
