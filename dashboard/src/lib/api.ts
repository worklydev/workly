import type { Error as ApiError } from '../api';
import { streaming } from './live';

/**
 * Options for live views: refetched on change events (see useLiveUpdates),
 * and polled, rarely while events arrive.
 */
export const LIVE = {
  meta: { live: true },
  refetchInterval: () => (streaming() ? 30_000 : 3000),
} as const;

/** The API error's message, or `fallback` for network errors and the like. */
export function errorMessage(error: unknown, fallback = 'Something went wrong.'): string {
  const message = (error as Partial<ApiError> | null)?.error?.message;
  return typeof message === 'string' ? message : fallback;
}

export function errorCode(error: unknown): string | undefined {
  return (error as Partial<ApiError> | null)?.error?.code;
}
