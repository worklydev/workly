import type { Error as ApiError } from '../api';

/** Polling interval for live views. */
export const LIVE = { refetchInterval: 3000 } as const;

/** The API error's message, or `fallback` for network errors and the like. */
export function errorMessage(error: unknown, fallback = 'Something went wrong.'): string {
  const message = (error as Partial<ApiError> | null)?.error?.message;
  return typeof message === 'string' ? message : fallback;
}

export function errorCode(error: unknown): string | undefined {
  return (error as Partial<ApiError> | null)?.error?.code;
}
