import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, mock, test } from 'node:test';

import { SignatureVerificationError, sign, verifySignature } from '../src/index.ts';

const NOW = 1790000000;

beforeEach(() => mock.timers.enable({ apis: ['Date'], now: NOW * 1000 }));
afterEach(() => mock.timers.reset());

test('sign matches the server', async () => {
  // Same vector as internal/signing/signing_test.go.
  assert.deepEqual(await sign('{"user_id":"123"}', 'secret', NOW), {
    'Workly-Timestamp': '1790000000',
    'Workly-Signature': 'v1=00f7255c88a43b3e2d47b211ff8e71cf935601cabff5e7adad0de2cc72a4e3c6',
  });
});

test('valid', async () => {
  await verifySignature('hello', await sign('hello', 'secret'), 'secret');
  const bytes = new TextEncoder().encode('hello');
  await verifySignature(bytes, await sign(bytes, 'secret'), 'secret');
  await verifySignature(bytes.buffer, await sign('hello', 'secret'), 'secret');
});

test('header shapes', async () => {
  const signed = await sign('hello', 'secret');
  // Fetch API Headers (Next.js, Hono, Workers).
  await verifySignature('hello', new Headers(signed), 'secret');
  // Node's IncomingHttpHeaders (Express): lowercase, values may be arrays.
  const lower = Object.fromEntries(Object.entries(signed).map(([k, v]) => [k.toLowerCase(), v]));
  await verifySignature('hello', lower, 'secret');
  await verifySignature('hello', { ...lower, 'workly-signature': [signed['Workly-Signature']!] }, 'secret');
});

test('rotation list', async () => {
  const signed = await sign('hello', 'secret');
  signed['Workly-Signature'] = `v1=00ff, v1=zz, v0=abcd, ${signed['Workly-Signature']}`;
  await verifySignature('hello', signed, 'secret');
});

describe('secret from the environment', () => {
  afterEach(() => delete process.env.WORKLY_SIGNING_SECRET);

  test('set', async () => {
    process.env.WORKLY_SIGNING_SECRET = 'secret';
    await verifySignature('hello', await sign('hello', 'secret'));
  });

  test('unset', async () => {
    await assert.rejects(verifySignature('hello', await sign('hello', 'secret')), /WORKLY_SIGNING_SECRET/);
  });
});

test('tolerance', async () => {
  const old = await sign('hello', 'secret', NOW - 6 * 60);
  await assert.rejects(verifySignature('hello', old, 'secret'), /tolerance/);
  await verifySignature('hello', old, 'secret', { toleranceSeconds: 600 });
  await verifySignature('hello', old, 'secret', { toleranceSeconds: 0 });
  const future = await sign('hello', 'secret', NOW + 6 * 60);
  await assert.rejects(verifySignature('hello', future, 'secret'), /tolerance/);
});

describe('invalid', () => {
  for (const [name, secret, body, change] of [
    ['wrong secret', 'other', 'hello', {}],
    ['tampered body', 'secret', 'hellO', {}],
    ['tampered timestamp', 'secret', 'hello', { 'Workly-Timestamp': String(NOW + 1) }],
  ] as const) {
    test(name, async () => {
      const headers = { ...(await sign('hello', 'secret')), ...change };
      await assert.rejects(verifySignature(body, headers, secret), SignatureVerificationError);
    });
  }

  test('unknown scheme', async () => {
    const headers = await sign('hello', 'secret');
    headers['Workly-Signature'] = headers['Workly-Signature']!.replace('v1=', 'v0=');
    await assert.rejects(verifySignature('hello', headers, 'secret'), /does not match/);
  });
});

describe('malformed', () => {
  for (const headers of [
    {},
    { 'Workly-Timestamp': String(NOW) },
    { 'Workly-Signature': 'v1=00' },
    { 'Workly-Timestamp': 'soon', 'Workly-Signature': 'v1=00' },
    { 'Workly-Timestamp': '1e9', 'Workly-Signature': 'v1=00' },
  ]) {
    test(JSON.stringify(headers), async () => {
      await assert.rejects(verifySignature('hello', headers, 'secret'), /missing or malformed/);
    });
  }
});
