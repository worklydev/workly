/**
 * JSON values as YAML, the way a workly.yaml is written by hand: block
 * mappings and sequences, lists of plain values on one line (`[a, b]`), and
 * strings quoted only when YAML would read them as something else. Only for
 * showing: it emits, it doesn't parse.
 */
export function toYAML(value: unknown): string {
  return lines(value, 0).join('\n') + '\n';
}

const pad = (n: number) => '  '.repeat(n);

function lines(value: unknown, depth: number): string[] {
  if (Array.isArray(value)) {
    if (value.length === 0 || value.every(isScalar)) return [pad(depth) + inline(value)];
    return value.flatMap((item) => {
      if (isScalar(item) || isEmpty(item)) return [`${pad(depth)}- ${inline(item)}`];
      // The first key shares the dash's line: "- name: prices".
      const [first, ...rest] = lines(item, depth + 1);
      return [`${pad(depth)}- ${first.trimStart()}`, ...rest];
    });
  }
  if (isObject(value)) {
    return Object.entries(value)
      .filter(([, v]) => v !== undefined)
      .flatMap(([k, v]) => {
        const key = `${pad(depth)}${scalar(k)}:`;
        if (isScalar(v) || isEmpty(v) || (Array.isArray(v) && v.every(isScalar))) return [`${key} ${inline(v)}`];
        return [key, ...lines(v, depth + 1)];
      });
  }
  return [pad(depth) + inline(value)];
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const isScalar = (v: unknown) => v === null || typeof v !== 'object';
const isEmpty = (v: unknown) => (Array.isArray(v) ? v.length === 0 : isObject(v) && Object.keys(v).length === 0);

function inline(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map((x) => scalar(x, true)).join(', ')}]`;
  if (isObject(v)) return '{}';
  return scalar(v);
}

// Words YAML 1.1 and 1.2 parsers read as booleans or null.
const KEYWORDS = /^(true|false|yes|no|on|off|y|n|null|~)$/i;
// Leading characters YAML gives a meaning to.
const INDICATORS = /^[-?:,[\]{}#&*!|>'"%@`\s]/;

/** In a flow sequence (`[a, b]`), commas and brackets need quotes too. */
function scalar(v: unknown, flow = false): string {
  if (v === null) return 'null';
  if (typeof v !== 'string') return String(v);
  const plain =
    v !== '' &&
    !KEYWORDS.test(v) &&
    !INDICATORS.test(v) &&
    !/\s$/.test(v) &&
    !v.includes(': ') &&
    !v.includes(' #') &&
    !v.endsWith(':') &&
    !/[\n\t]/.test(v) &&
    !(flow && /[,[\]{}]/.test(v)) &&
    Number.isNaN(Number(v));
  return plain ? v : JSON.stringify(v);
}
