import type { Delivery } from '../api';

/** Single-quotes for POSIX shells. */
function quote(s: string): string {
  return `'${s.replaceAll("'", `'\\''`)}'`;
}

/** A curl command that sends the delivery exactly: same headers, same body bytes. */
export function toCurl(d: Delivery): string {
  const lines = [`curl -X ${d.method} ${quote(d.url)}`];
  for (const [k, v] of Object.entries(d.headers).sort(([a], [b]) => a.localeCompare(b))) {
    lines.push(`-H ${quote(`${k}: ${v}`)}`);
  }
  if (d.body) lines.push(`--data-raw ${quote(d.body)}`);
  return lines.join(' \\\n  ');
}

/** Whether the URL's path has an empty segment ("//"), usually from joining a base URL and a path. */
export function hasDoubleSlash(url: string): boolean {
  try {
    return new URL(url).pathname.includes('//');
  } catch {
    return false;
  }
}
