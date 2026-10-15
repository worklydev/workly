// Delivery signatures. Must match internal/signing in the server:
//
//   Workly-Timestamp: <unix seconds>
//   Workly-Signature: v1=<hex HMAC-SHA256 of "<timestamp>.<body>">
//
// Several comma-separated signatures may be sent during key rotation. Uses
// Web Crypto, so it works in Node, Deno, Bun, browsers and edge runtimes.

import { SignatureVerificationError } from './errors.ts';
import { env } from './util.ts';

export const HEADER_SIGNATURE = 'Workly-Signature';
export const HEADER_TIMESTAMP = 'Workly-Timestamp';

/** Default for `verifySignature`'s `toleranceSeconds`. */
export const DEFAULT_TOLERANCE_SECONDS = 300;

export type Body = string | Uint8Array | ArrayBuffer;
export type HeadersLike = Headers | Record<string, string | string[] | undefined>;

/**
 * The signature headers Workly would send with `body`. Useful for testing
 * your endpoint without a server. `timestamp` is in Unix seconds.
 */
export async function sign(body: Body, secret: string, timestamp?: number): Promise<Record<string, string>> {
  const ts = timestamp ?? Math.floor(Date.now() / 1000);
  const mac = await crypto.subtle.sign('HMAC', await hmacKey(secret), signedBytes(ts, body));
  return { [HEADER_TIMESTAMP]: String(ts), [HEADER_SIGNATURE]: `v1=${toHex(new Uint8Array(mac))}` };
}

export interface VerifyOptions {
  /** How old the timestamp may be, which limits replays. Default 300; 0 skips the check. */
  toleranceSeconds?: number | undefined;
}

/**
 * Checks that a delivery came from Workly, and throws
 * `SignatureVerificationError` if it didn't.
 *
 * Pass the raw request body, exactly as received (not re-serialized JSON),
 * and the request headers. `secret` defaults to `WORKLY_SIGNING_SECRET`.
 */
export async function verifySignature(
  body: Body,
  headers: HeadersLike,
  secret?: string,
  options: VerifyOptions = {},
): Promise<void> {
  secret ??= env('WORKLY_SIGNING_SECRET');
  if (!secret) throw new Error('pass secret or set WORKLY_SIGNING_SECRET');

  const signature = header(headers, HEADER_SIGNATURE);
  const timestamp = header(headers, HEADER_TIMESTAMP);
  if (!signature || !timestamp || !/^\d+$/.test(timestamp)) {
    throw new SignatureVerificationError('missing or malformed signature headers');
  }
  const ts = Number(timestamp);

  const tolerance = options.toleranceSeconds ?? DEFAULT_TOLERANCE_SECONDS;
  if (tolerance > 0 && Math.abs(Date.now() / 1000 - ts) > tolerance) {
    throw new SignatureVerificationError('timestamp outside tolerance');
  }

  const key = await hmacKey(secret);
  const data = signedBytes(ts, body);
  for (const part of signature.split(',')) {
    const [scheme, value] = part.trim().split('=', 2);
    const mac = scheme === 'v1' && value !== undefined ? fromHex(value) : undefined;
    // crypto.subtle.verify compares in constant time.
    if (mac && (await crypto.subtle.verify('HMAC', key, mac, data))) return;
  }
  throw new SignatureVerificationError('signature does not match');
}

const encoder = new TextEncoder();

function hmacKey(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', encoder.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, [
    'sign',
    'verify',
  ]);
}

function signedBytes(timestamp: number, body: Body): Uint8Array<ArrayBuffer> {
  const prefix = encoder.encode(`${timestamp}.`);
  const bytes = typeof body === 'string' ? encoder.encode(body) : new Uint8Array(body);
  const out = new Uint8Array(prefix.length + bytes.length);
  out.set(prefix);
  out.set(bytes, prefix.length);
  return out;
}

function header(headers: HeadersLike, name: string): string | undefined {
  if (typeof Headers !== 'undefined' && headers instanceof Headers) return headers.get(name) ?? undefined;
  // Node's IncomingHttpHeaders are lowercase; plain objects may not be.
  const lower = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lower) return Array.isArray(value) ? value.join(',') : value;
  }
  return undefined;
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

function fromHex(hex: string): Uint8Array<ArrayBuffer> | undefined {
  if (hex.length % 2 !== 0 || !/^[0-9a-f]*$/i.test(hex)) return undefined;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return out;
}
