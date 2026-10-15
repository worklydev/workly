// Environment variables where the runtime has them (Node, Bun, Deno with
// --allow-env); undefined elsewhere, e.g. in browsers.
export function env(name: string): string | undefined {
  const process = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
  try {
    return process?.env?.[name] || undefined;
  } catch {
    return undefined; // Deno without env permission
  }
}

export function seg(value: string): string {
  return encodeURIComponent(value);
}

export function timestamp(value: Date | string | undefined, name: string): string | undefined {
  if (value === undefined || typeof value === 'string') return value;
  if (Number.isNaN(value.getTime())) throw new TypeError(`${name} is an invalid Date`);
  return value.toISOString();
}

// The API's JSON is snake_case and the SDK's objects are camelCase. These keys
// hold the caller's own data (a JSON payload, header names, queue names), so
// their values are passed through as they are.
const verbatim = new Set(['payload', 'headers', 'responseHeaders', 'counts']);

/** The API's JSON as SDK objects: snake_case keys to camelCase, recursively. */
export function fromAPI(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(fromAPI);
  if (!isPlainObject(value)) return value;
  const out: Record<string, unknown> = {};
  for (const [key, v] of Object.entries(value)) {
    const name = key.replace(/_([a-z0-9])/g, (_, c: string) => c.toUpperCase());
    out[name] = verbatim.has(name) ? v : fromAPI(v);
  }
  return out;
}

/** SDK objects as the API's JSON: camelCase keys to snake_case, recursively. Drops undefined values. */
export function toAPI(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(toAPI);
  if (!isPlainObject(value)) return value;
  const out: Record<string, unknown> = {};
  for (const [key, v] of Object.entries(value)) {
    if (v === undefined) continue;
    // retryOn4xx -> retry_on_4xx: the API puts an underscore before every number.
    const name = key.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`).replace(/([a-z])(\d)/g, '$1_$2');
    out[name] = verbatim.has(key) ? v : toAPI(v);
  }
  return out;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) return false;
  const proto: unknown = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}
