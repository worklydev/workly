/** The Workly mark: a queue of three bars. */
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden>
      <rect width="32" height="32" rx="8" className="fill-primary" />
      <path d="M8 11h16M8 16h11M8 21h6" className="stroke-primary-foreground" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}
