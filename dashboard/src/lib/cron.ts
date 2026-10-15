// Cron expressions in words, for the common shapes: "Every 15 minutes",
// "At 03:00 on Mon–Fri". Anything else returns undefined, and the expression
// is shown as is. The server is the authority on what an expression means;
// this only labels it.

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const MACROS: Record<string, string> = {
  '@hourly': 'Every hour',
  '@daily': 'Every day at 00:00',
  '@midnight': 'Every day at 00:00',
  '@weekly': 'Every Sunday at 00:00',
  '@monthly': 'On the 1st of every month at 00:00',
  '@yearly': 'Every year on Jan 1 at 00:00',
  '@annually': 'Every year on Jan 1 at 00:00',
};

export function describeCron(expr: string): string | undefined {
  const trimmed = expr.trim();
  if (trimmed.startsWith('@')) return MACROS[trimmed.toLowerCase()];
  const fields = trimmed.split(/\s+/);
  if (fields.length !== 5) return undefined;
  const [minute, hour, dom, month, dow] = fields;

  const time = timePart(minute, hour);
  if (time === undefined) return undefined;
  let days: string | undefined = '';
  if (dom !== '*' && dow !== '*') days = undefined;
  else if (dow !== '*') days = names(dow, DAYS, 0, true) && `on ${names(dow, DAYS, 0, true)}`;
  else if (dom !== '*') days = numbers(dom) && `on day ${numbers(dom)} of the month`;
  const months = month === '*' ? '' : names(month, MONTHS, 1, false) && `in ${names(month, MONTHS, 1, false)}`;
  if (days === undefined || months === undefined) return undefined;

  const parts = [time];
  if (days) parts.push(days);
  else if (time.startsWith('At')) parts.push('every day');
  if (months) parts.push(months);
  return parts.join(' ');
}

function timePart(minute: string, hour: string): string | undefined {
  const step = (f: string) => /^\*\/(\d+)$/.exec(f)?.[1];
  const num = (f: string) => (/^\d+$/.test(f) ? Number(f) : undefined);
  const m = num(minute);
  const pad = (n: number) => String(n).padStart(2, '0');

  if (minute === '*' && hour === '*') return 'Every minute';
  if (step(minute) && hour === '*') return `Every ${step(minute)} minutes`;
  if (m !== undefined && hour === '*') return m === 0 ? 'Every hour' : `Every hour at :${pad(m)}`;
  if (m !== undefined && step(hour)) return `Every ${step(hour)} hours at :${pad(m)}`;
  const hours = hour.split(',').map(num);
  if (m !== undefined && hours.length <= 4 && hours.every((h) => h !== undefined)) {
    return `At ${hours.map((h) => `${pad(h!)}:${pad(m)}`).join(', ')}`;
  }
  const range = /^(\d+)-(\d+)$/.exec(hour);
  if (step(minute) && range) {
    return `Every ${step(minute)} minutes from ${pad(Number(range[1]))}:00 to ${pad(Number(range[2]))}:59`;
  }
  return undefined;
}

/** "Mon–Fri", "Mon, Wed": lists and ranges of names or numbers. */
function names(field: string, labels: string[], first: number, weekday: boolean): string | undefined {
  const label = (v: string) => {
    const i = labels.findIndex((l) => l.toLowerCase() === v.toLowerCase());
    if (i >= 0) return labels[i];
    if (!/^\d+$/.test(v)) return undefined;
    const n = Number(v) - first;
    return labels[weekday && n === 7 ? 0 : n];
  };
  const out: string[] = [];
  for (const item of field.split(',')) {
    const [a, b, extra] = item.split('-');
    if (extra !== undefined || item.includes('/')) return undefined;
    const from = label(a);
    const to = b === undefined ? from : label(b);
    if (!from || !to) return undefined;
    out.push(from === to ? from : `${from}–${to}`);
  }
  return out.join(', ');
}

function numbers(field: string): string | undefined {
  return /^[\d,-]+$/.test(field) ? field.replaceAll(',', ', ').replaceAll('-', '–') : undefined;
}
