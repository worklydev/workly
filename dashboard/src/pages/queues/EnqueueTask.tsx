import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { createTaskMutation } from '../../api/@tanstack/react-query.gen';
import type { EnqueueRequest, HttpMethod, Task } from '../../api';
import { FormGroup, FormRow, Unit } from '../../components/FormRows';
import { errorMessage } from '../../lib/api';
import { bodyFields, formatBody, formatHeaders, parseHeaders } from '../../lib/request';
import { Button } from '../../primitives/button';
import { Dialog, DialogContent, DialogTitle } from '../../primitives/dialog';
import { Input } from '../../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../primitives/select';
import { Textarea } from '../../primitives/textarea';

type When = 'now' | 'delay' | 'at';

type Form = {
  method: HttpMethod;
  url: string;
  headers: string;
  body: string;
  when: When;
  delay: string;
  at: string;
  idempotency_key: string;
  max_attempts: string;
  timeout_seconds: string;
};

/** A blank form, or `from`'s request again: its URL, headers and body, but not its idempotency key. */
function toForm(from?: Task): Form {
  return {
    method: from?.method ?? 'POST',
    url: from?.url ?? '',
    headers: from ? formatHeaders(ownHeaders(from)) : '',
    body: from ? formatBody(from) : '',
    when: 'now',
    delay: '',
    at: '',
    idempotency_key: '',
    max_attempts: from?.max_attempts !== undefined ? String(from.max_attempts) : '',
    timeout_seconds: from?.timeout_seconds !== undefined ? String(from.timeout_seconds) : '',
  };
}

/**
 * A task's headers to send again. A JSON payload's Content-Type was added
 * by Workly and comes back with the payload, which then goes out compact as
 * it did, not as the pretty-printed text in the form.
 */
function ownHeaders(t: Task): Record<string, string> {
  if (t.payload === undefined) return t.headers;
  return Object.fromEntries(Object.entries(t.headers).filter(([k]) => k.toLowerCase() !== 'content-type'));
}

function toRequest(f: Form): EnqueueRequest | string {
  const url = f.url.trim();
  if (!url) return 'URL is required.';
  const headers = parseHeaders(f.headers);
  if (typeof headers === 'string') return headers;
  const req: EnqueueRequest = { url, method: f.method, headers, ...bodyFields(f.body, headers) };

  if (f.when === 'delay') {
    const n = Number(f.delay.trim());
    if (f.delay.trim() === '' || !Number.isFinite(n) || n < 0) return 'Delay must be a number of seconds.';
    req.delay_seconds = n;
  } else if (f.when === 'at') {
    const t = new Date(f.at);
    if (!f.at || Number.isNaN(t.getTime())) return 'Pick a time to run at.';
    req.run_at = t.toISOString();
  }
  if (f.idempotency_key.trim()) req.idempotency_key = f.idempotency_key.trim();
  for (const [key, label] of [
    ['max_attempts', 'Max attempts'],
    ['timeout_seconds', 'Timeout'],
  ] as const) {
    const v = f[key].trim();
    if (v === '') continue;
    const n = Number(v);
    if (!Number.isFinite(n)) return `${label} must be a number.`;
    req[key] = n;
  }
  return req;
}

/** Enqueues a task to `queue` from a form, optionally starting from another task's request. */
export function EnqueueTask({
  queue,
  from,
  open,
  onClose,
}: {
  queue: string;
  from?: Task;
  open: boolean;
  onClose: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      {/* Mounted per opening, so the form starts fresh (or from the task) each time. */}
      {open && <EnqueueForm queue={queue} from={from} onClose={onClose} />}
    </Dialog>
  );
}

const METHODS: HttpMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
const WHEN: { value: When; label: string }[] = [
  { value: 'now', label: 'Now' },
  { value: 'delay', label: 'After a delay' },
  { value: 'at', label: 'At a time' },
];

function EnqueueForm({ queue, from, onClose }: { queue: string; from?: Task; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [form, setForm] = useState(() => toForm(from));
  const [problem, setProblem] = useState<string>();
  const enqueue = useMutation({
    ...createTaskMutation(),
    onSuccess: async (task) => {
      await qc.invalidateQueries();
      onClose();
      // Its page shows the delivery as it happens.
      navigate(`/tasks/${task.id}`);
    },
  });
  const set = (key: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setForm({ ...form, [key]: e.target.value });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const req = toRequest(form);
    if (typeof req === 'string') return setProblem(req);
    setProblem(undefined);
    enqueue.mutate({ path: { queue }, body: req });
  };
  const error = problem ?? (enqueue.error ? errorMessage(enqueue.error) : undefined);

  return (
    <DialogContent className="w-[min(44rem,calc(100vw-2rem))]">
      <form onSubmit={submit} className="flex flex-col gap-5">
        <div className="flex flex-col gap-1 pr-8">
          <DialogTitle className="text-base font-medium">{from ? 'Duplicate task' : 'Enqueue task'}</DialogTitle>
          <p className="text-xs text-muted-foreground">
            {from ? (
              <>
                A new task in <span className="font-mono">{queue}</span> with the request of{' '}
                <span className="font-mono">{from.id}</span>. Change anything before sending.
              </>
            ) : (
              <>
                Workly delivers it to the URL with the <span className="font-mono">{queue}</span> queue's retries and
                limits.
              </>
            )}
          </p>
        </div>

        <FormGroup title="Request">
          <FormRow label="Endpoint">
            <div className="flex items-center gap-2">
              <Choice
                items={METHODS.map((m) => ({ value: m, label: m }))}
                value={form.method}
                onChange={(v) => setForm({ ...form, method: v as HttpMethod })}
                className="w-28 font-mono"
              />
              <Input autoFocus value={form.url} onChange={set('url')} placeholder="https://" className="font-mono" />
            </div>
          </FormRow>
          <FormRow label="Headers" hint="One per line. Workly-* headers are reserved.">
            <Textarea
              value={form.headers}
              onChange={set('headers')}
              placeholder="Authorization: Bearer …"
              rows={Math.min(6, Math.max(2, form.headers.split('\n').length))}
            />
          </FormRow>
          <FormRow label="Body" hint="JSON is sent as application/json unless a Content-Type header says otherwise.">
            <Textarea
              value={form.body}
              onChange={set('body')}
              placeholder='{"user_id": 42}'
              rows={Math.min(12, Math.max(3, form.body.split('\n').length))}
            />
          </FormRow>
        </FormGroup>

        <FormGroup title="Delivery">
          <FormRow label="Run">
            <div className="flex flex-wrap items-center gap-2">
              <Choice items={WHEN} value={form.when} onChange={(v) => setForm({ ...form, when: v as When })} />
              {form.when === 'delay' && (
                <Unit unit="s">
                  <Input
                    inputMode="decimal"
                    aria-label="Delay"
                    value={form.delay}
                    onChange={set('delay')}
                    className="w-24"
                  />
                </Unit>
              )}
              {form.when === 'at' && (
                <Input
                  type="datetime-local"
                  aria-label="Run at"
                  value={form.at}
                  onChange={set('at')}
                  className="w-56"
                />
              )}
            </div>
          </FormRow>
          <FormRow
            label="Idempotency key"
            hint="Enqueueing the same key to this queue again returns the existing task instead of a new one."
          >
            <Input
              value={form.idempotency_key}
              onChange={set('idempotency_key')}
              placeholder="optional"
              className="w-64 font-mono"
            />
          </FormRow>
          <FormRow label="Max attempts" hint="Including the first. Empty for the queue's.">
            <Input
              inputMode="numeric"
              value={form.max_attempts}
              onChange={set('max_attempts')}
              placeholder="queue's"
              className="w-28"
            />
          </FormRow>
          <FormRow label="Timeout" hint="Empty for the queue's.">
            <Unit unit="s">
              <Input
                inputMode="decimal"
                value={form.timeout_seconds}
                onChange={set('timeout_seconds')}
                placeholder="queue's"
                className="w-24"
              />
            </Unit>
          </FormRow>
        </FormGroup>

        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {error && <span className="mr-auto text-xs text-destructive">{error}</span>}
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="default" disabled={enqueue.isPending}>
            {enqueue.isPending ? 'Enqueueing…' : 'Enqueue'}
          </Button>
        </div>
      </form>
    </DialogContent>
  );
}

function Choice({
  items,
  value,
  onChange,
  className = 'w-40',
}: {
  items: { value: string; label: string }[];
  value: string;
  onChange: (value: string) => void;
  className?: string;
}) {
  return (
    <Select items={items} value={value} onValueChange={(v) => onChange(String(v))}>
      <SelectTrigger className={className}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((i) => (
          <SelectItem key={i.value} value={i.value}>
            {i.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
