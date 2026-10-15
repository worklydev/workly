type Props = {
  title: React.ReactNode;
  description?: React.ReactNode;
  /** Right side: badges, actions. */
  children?: React.ReactNode;
};

export function PageHeader({ title, description, children }: Props) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1">
        <h2 className="truncate text-xl">{title}</h2>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {children && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </div>
  );
}
