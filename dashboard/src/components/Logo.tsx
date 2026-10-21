import { useId } from 'react';

/**
 * The Workly mark: three slanted bars, a queue draining, on an orange
 * gradient. Its source of truth is brand/mark.svg in workly-cloud.
 */
export function Logo({ className }: { className?: string }) {
  // Several logos can be on a page; each needs its own gradient id.
  const gradient = useId();
  return (
    <svg viewBox="0 0 100 100" className={className} aria-hidden>
      <defs>
        <linearGradient id={gradient} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#FF5A0F" />
          <stop offset="1" stopColor="#FF920A" />
        </linearGradient>
      </defs>
      <rect width="100" height="100" rx="21" fill={`url(#${gradient})`} />
      <g fill="#fff">
        <path d="M30 20h55l-12 15H18z" />
        <path d="M30 43h41l-12 15H18z" />
        <path d="M30 66h27l-12 15H18z" />
      </g>
    </svg>
  );
}
