import { useSearchParams } from 'react-router';
import { useNow } from './useNow';

export type Range = '1h' | '24h' | '7d';

export const RANGES: { value: Range; seconds: number; per: string }[] = [
  { value: '1h', seconds: 3600, per: '5 min' },
  { value: '24h', seconds: 86_400, per: 'hour' },
  { value: '7d', seconds: 7 * 86_400, per: '6 hours' },
];

const DEFAULT: Range = '24h';

/**
 * The time range the Queues and queue pages count over, kept in the URL
 * (`range`) so the chart, the queue list and a queue's activity agree, and
 * the choice survives opening a queue and going back.
 */
export function useRange() {
  const [params, setParams] = useSearchParams();
  const range = RANGES.find((r) => r.value === params.get('range')) ?? RANGES.find((r) => r.value === DEFAULT)!;
  const setRange = (r: Range) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (r === DEFAULT) next.delete('range');
        else next.set('range', r);
        return next;
      },
      { replace: true },
    );
  // Carried on links to another page that counts over the range.
  const query = range.value === DEFAULT ? '' : `?range=${range.value}`;
  return { ...range, setRange, query };
}

/**
 * The start of the range as an RFC 3339 time. It moves once a minute, so
 * query keys built from it stay stable between polls.
 */
export function useRangeSince(): string {
  const { seconds } = useRange();
  return new Date(Math.floor(useNow(60_000) / 60_000) * 60_000 - seconds * 1000).toISOString();
}
