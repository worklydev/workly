type Props = {
  title: React.ReactNode;
  description?: React.ReactNode;
  /** Next to the title: the entity's state, e.g. failed or paused. */
  badge?: React.ReactNode;
  /**
   * Right side: actions, in this order: ghost actions, then the main action
   * (a filled button), then destructive ones (ConfirmButton) last.
   */
  children?: React.ReactNode;
};

/** On narrow screens the actions wrap below the title rather than squeezing it. */
export function PageHeader({ title, description, badge, children }: Props) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
      <div className="flex min-w-0 grow basis-64 flex-col gap-1">
        <div className="flex min-w-0 items-center gap-2.5">
          <h2 className="min-w-0 truncate text-xl">{title}</h2>
          {badge}
        </div>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
    </div>
  );
}
