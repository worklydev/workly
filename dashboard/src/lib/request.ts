/** Headers and bodies as typed into the enqueue and schedule forms. */

/** "Name: value" lines to headers, or a problem to show. Blank lines are skipped. */
export function parseHeaders(text: string): Record<string, string> | string {
  const headers: Record<string, string> = {};
  for (const [i, line] of text.split('\n').entries()) {
    if (line.trim() === '') continue;
    const colon = line.indexOf(':');
    const name = line.slice(0, colon).trim();
    if (colon < 0 || name === '')
      return `Header line ${i + 1} needs a name and a value, like "Authorization: Bearer …".`;
    headers[name] = line.slice(colon + 1).trim();
  }
  return headers;
}

/** Headers as "Name: value" lines, for editing. */
export function formatHeaders(headers: Record<string, string>): string {
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
}

/**
 * The request fields for a typed body: JSON goes as `payload` (sent as
 * application/json) unless a Content-Type header says otherwise, so a body
 * keeps its type; anything else goes as `body`.
 */
export function bodyFields(text: string, headers: Record<string, string>): { payload?: unknown; body?: string } {
  if (text.trim() === '') return {};
  const typed = Object.keys(headers).some((k) => k.toLowerCase() === 'content-type');
  const json = typed ? undefined : parseJSON(text);
  return json !== undefined ? { payload: json } : { body: text };
}

/** A stored payload or body back as text, for editing. */
export function formatBody(r: { payload?: unknown; body?: string }): string {
  return r.payload !== undefined ? JSON.stringify(r.payload, null, 2) : (r.body ?? '');
}

function parseJSON(s: string): unknown {
  try {
    return JSON.parse(s) as unknown;
  } catch {
    return undefined;
  }
}
