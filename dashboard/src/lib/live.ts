import { useEffect } from 'react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { client } from '../api/client.gen';

let connected = false;

/** Whether change events are arriving, so live views can poll less. */
export function streaming(): boolean {
  return connected;
}

/**
 * Refetches at most this often, however busy the workspace: refetches count
 * toward the API's rate limit, where it has one.
 */
const REFRESH_MS = 1000;
const MAX_RETRY_MS = 30_000;

/**
 * Keeps live views (queries with `meta.live`, see LIVE) up to date: the
 * server sends a `change` event on GET /v1/events whenever the workspace's
 * queues, tasks, schedules or runs change, and they refetch.
 */
export function useLiveUpdates() {
  const queries = useQueryClient();
  useEffect(() => {
    const abort = new AbortController();
    const refresh = throttle(() => refetchLive(queries), REFRESH_MS);
    void (async () => {
      let retry = 1000;
      let reconnecting = false;
      while (!abort.signal.aborted) {
        try {
          const { stream } = await client.sse.get({
            url: '/v1/events',
            security: [{ scheme: 'bearer', type: 'http' }],
            signal: abort.signal,
            // Retries are ours: the client's give up once a stream ends.
            sseMaxRetryAttempts: 1,
            onSseEvent: (event) => {
              if (event.event === 'ready') {
                connected = true;
                retry = 1000;
                // Catch up on what changed while disconnected.
                if (reconnecting) refresh();
              } else if (event.event === 'change') {
                refresh();
              }
            },
          });
          for await (const _ of stream) {
            // Events are handled in onSseEvent.
          }
        } catch {
          // Reconnect below.
        }
        if (connected) {
          connected = false;
          // Back to polling, from now rather than from the last slow poll.
          refetchLive(queries);
        }
        reconnecting = true;
        await sleep(retry, abort.signal);
        retry = Math.min(retry * 2, MAX_RETRY_MS);
      }
    })();
    return () => {
      abort.abort();
      refresh.cancel();
      connected = false;
    };
  }, [queries]);
}

function refetchLive(queries: QueryClient) {
  // Hidden tabs catch up when they're shown, as queries refetch on focus.
  if (document.hidden) return;
  void queries.invalidateQueries({ predicate: (query) => query.meta?.live === true });
}

/** Calls fn now if it's been idle for ms, else once at the end of the wait. */
function throttle(fn: () => void, ms: number) {
  let last = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const call = () => {
    last = Date.now();
    timer = undefined;
    fn();
  };
  const throttled = () => {
    if (timer) return;
    const wait = last + ms - Date.now();
    if (wait <= 0) call();
    else timer = setTimeout(call, wait);
  };
  throttled.cancel = () => clearTimeout(timer);
  return throttled;
}

function sleep(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {
      clearTimeout(timer);
      resolve();
    });
  });
}
