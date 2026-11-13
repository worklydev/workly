import type { ReactNode } from 'react';
import { ArrowRight } from 'lucide-react';
import { Link } from 'react-router';

export function Section({
  title,
  actions,
  link,
  children,
}: {
  title: ReactNode;
  actions?: ReactNode;
  /** A page with the whole of what the section shows a part of: "All queues →". */
  link?: { to: string; label: string };
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <h3 className="font-medium">{title}</h3>
        {actions}
        {link && (
          <Link
            to={link.to}
            className="group/link inline-flex items-center gap-1 rounded-sm text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            {link.label}
            <ArrowRight className="size-3 transition-transform group-hover/link:translate-x-0.5" />
          </Link>
        )}
      </div>
      {children}
    </section>
  );
}
