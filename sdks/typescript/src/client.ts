import { Alerts } from './alerts.ts';
import { APIConnectionError, APIError, apiError } from './errors.ts';
import { Queues } from './queues.ts';
import { Runs, Schedules } from './schedules.ts';
import { Tasks } from './tasks.ts';
import { env, fromAPI, toAPI } from './util.ts';
import { VERSION } from './version.ts';

export const DEFAULT_URL = 'http://localhost:7337';

export interface WorklyOptions {
  /** Defaults to `WORKLY_URL`, then http://localhost:7337 (`workly dev`). */
  url?: string | undefined;
  /** Defaults to `WORKLY_API_KEY`. `workly dev` needs none. */
  apiKey?: string | undefined;
  /** Per request, including retries of it. Default 30 seconds. */
  timeoutMs?: number | undefined;
  /** Retries of requests that are safe to repeat. Default 2. */
  maxRetries?: number | undefined;
  /** A custom fetch, e.g. for proxies or tests. */
  fetch?: typeof fetch | undefined;
}

/** Options every method takes as its last argument. */
export interface RequestOptions {
  signal?: AbortSignal | undefined;
}

/** @internal */
export interface Request {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  path: string;
  query?: Record<string, string | undefined>;
  /** camelCase; sent as snake_case. */
  body?: unknown;
  headers?: Record<string, string>;
  /** Safe to resend even if the server may have processed it. */
  idempotent?: boolean;
  signal?: AbortSignal | undefined;
}

// Gateway errors, retried for requests that are safe to repeat. A 429 is
// retried for every request: the server refused it without doing anything.
const retryStatuses = new Set([502, 503, 504]);
const maxDelayMs = 8000;
// Errors that mean the request never reached the server, so it can always
// be resent (undici's codes, as fetch's `cause`).
const notSentCodes = new Set(['ECONNREFUSED', 'ENOTFOUND', 'EAI_AGAIN', 'UND_ERR_CONNECT_TIMEOUT']);

/**
 * Client for the Workly API.
 *
 * ```ts
 * const workly = new Workly(); // WORKLY_URL, WORKLY_API_KEY
 * const task = await workly.tasks.enqueue('emails', { url: 'https://api.example.com/send', payload: { userId: 1 } });
 * ```
 */
export class Workly {
  readonly url: string;
  readonly queues: Queues;
  readonly tasks: Tasks;
  readonly schedules: Schedules;
  readonly runs: Runs;
  readonly alerts: Alerts;

  #headers: Record<string, string>;
  #timeoutMs: number;
  #maxRetries: number;
  #fetch: typeof fetch;

  constructor(options: WorklyOptions = {}) {
    this.url = (options.url ?? env('WORKLY_URL') ?? DEFAULT_URL).replace(/\/+$/, '');
    const apiKey = options.apiKey ?? env('WORKLY_API_KEY');
    this.#headers = { Accept: 'application/json', 'User-Agent': `workly-typescript/${VERSION}` };
    if (apiKey) this.#headers.Authorization = `Bearer ${apiKey}`;
    this.#timeoutMs = options.timeoutMs ?? 30_000;
    this.#maxRetries = options.maxRetries ?? 2;
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.queues = new Queues(this);
    this.tasks = new Tasks(this);
    this.schedules = new Schedules(this);
    this.runs = new Runs(this);
    this.alerts = new Alerts(this);
  }

  /** @internal */
  async send<T>(req: Request): Promise<T> {
    const url = new URL(this.url + req.path);
    for (const [key, value] of Object.entries(req.query ?? {})) {
      if (value !== undefined) url.searchParams.set(key, value);
    }
    const headers = { ...this.#headers, ...req.headers };
    if (req.body !== undefined) headers['Content-Type'] = 'application/json';
    const body = req.body === undefined ? undefined : JSON.stringify(toAPI(req.body));

    const timeout = AbortSignal.timeout(this.#timeoutMs);
    const signal = req.signal ? AbortSignal.any([req.signal, timeout]) : timeout;
    const where = `${req.method} ${req.path}`;

    for (let attempt = 0; ; attempt++) {
      let response: Response;
      try {
        response = await this.#fetch(url, { method: req.method, headers, body: body ?? null, signal });
      } catch (err) {
        if (signal.aborted) throw this.#abortReason(req, where);
        if (attempt < this.#maxRetries && (req.idempotent || notSent(err))) {
          await this.#sleep(backoff(attempt), req, signal, where);
          continue;
        }
        throw new APIConnectionError(`${where}: ${describe(err)}`, { cause: err });
      }
      const retry = response.status === 429 || (req.idempotent && retryStatuses.has(response.status));
      if (attempt < this.#maxRetries && retry) {
        await response.body?.cancel();
        await this.#sleep(backoff(attempt, response), req, signal, where);
        continue;
      }
      try {
        return await decode<T>(response);
      } catch (err) {
        // The body can time out too.
        if (signal.aborted && !(err instanceof APIError)) throw this.#abortReason(req, where);
        throw err;
      }
    }
  }

  // The caller's own abort wins; otherwise it was our timeout.
  #abortReason(req: Request, where: string): unknown {
    if (req.signal?.aborted) return req.signal.reason;
    return new APIConnectionError(`${where}: timed out after ${this.#timeoutMs}ms`);
  }

  async #sleep(ms: number, req: Request, signal: AbortSignal, where: string): Promise<void> {
    try {
      await sleep(ms, signal);
    } catch {
      throw this.#abortReason(req, where);
    }
  }
}

async function decode<T>(response: Response): Promise<T> {
  const text = await response.text();
  if (response.status >= 400) {
    let code = 'unknown';
    let message = text || response.statusText;
    try {
      const err = (JSON.parse(text) as { error?: { code?: unknown; message?: unknown } }).error;
      if (typeof err?.code === 'string' && typeof err.message === 'string') {
        code = err.code;
        message = err.message;
      }
    } catch {
      // Not JSON, e.g. from a proxy: keep the raw text.
    }
    throw apiError(response.status, code, message);
  }
  return (text ? fromAPI(JSON.parse(text)) : undefined) as T;
}

function notSent(err: unknown): boolean {
  const code = (err as { cause?: { code?: unknown } } | undefined)?.cause?.code;
  return typeof code === 'string' && notSentCodes.has(code);
}

function backoff(attempt: number, response?: Response): number {
  const retryAfter = Number(response?.headers.get('Retry-After') ?? NaN);
  if (Number.isFinite(retryAfter) && retryAfter >= 0) return Math.min(retryAfter * 1000, maxDelayMs);
  return Math.min(500 * 2 ** attempt, maxDelayMs) * (0.5 + Math.random() / 2);
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

function describe(err: unknown): string {
  if (!(err instanceof Error)) return String(err);
  const cause = err.cause instanceof Error ? `: ${err.cause.message}` : '';
  return err.message + cause;
}
