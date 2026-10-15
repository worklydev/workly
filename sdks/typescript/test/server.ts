// Starts a real `workly dev` server for the integration tests. Uses the
// binary at WORKLY_BIN, or builds one from this checkout with `go build`, so
// the SDK is always tested against the server from the same commit.

import { execFileSync, spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('../../..', import.meta.url));

export interface Server {
  url: string;
  stop(): Promise<void>;
}

/** Undefined when neither WORKLY_BIN nor Go is available; the tests are then skipped. */
export async function startServer(): Promise<Server | undefined> {
  const bin = process.env.WORKLY_BIN ?? build();
  if (!bin) return undefined;

  const data = mkdtempSync(join(tmpdir(), 'workly-sdk-'));
  const env = Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('WORKLY_')));
  const proc = spawn(bin, ['dev', '--port', '0', '--database-url', `sqlite://${data}/dev.db`], {
    cwd: data,
    env,
    stdio: ['ignore', 'ignore', 'pipe'],
  });

  const url = await new Promise<string>((resolve, reject) => {
    // Reading every line also keeps the pipe drained.
    createInterface({ input: proc.stderr }).on('line', (line) => {
      const match = /API:\s+(\S+)/.exec(line);
      if (match?.[1]) resolve(match[1]);
    });
    proc.on('exit', (code) => reject(new Error(`workly dev exited (${code}) before it was ready`)));
  });

  return {
    url,
    stop: () =>
      new Promise((resolve) => {
        proc.on('exit', () => resolve());
        proc.kill('SIGTERM');
      }),
  };
}

function build(): string | undefined {
  const out = join(mkdtempSync(join(tmpdir(), 'workly-bin-')), 'workly');
  try {
    execFileSync('go', ['build', '-o', out, './cmd/workly'], { cwd: repo, stdio: 'inherit' });
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return undefined;
    throw err;
  }
  return out;
}
