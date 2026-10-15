import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { getQueue } from '../../api';
import { putQueueMutation } from '../../api/@tanstack/react-query.gen';
import type { Queue, QueueConfig } from '../../api';
import { FormGroup, FormRow, Unit } from '../../components/FormRows';
import { errorMessage } from '../../lib/api';
import { RETRY_4XX } from '../../lib/policy';
import { Button } from '../../primitives/button';
import { Dialog, DialogContent, DialogTitle } from '../../primitives/dialog';
import { Input } from '../../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../primitives/select';

type Form = Record<
  | 'max_attempts'
  | 'min_backoff_seconds'
  | 'max_backoff_seconds'
  | 'backoff_multiplier'
  | 'timeout_seconds'
  | 'rate_per_second'
  | 'max_concurrency',
  string
> & { retry_on_4xx: boolean };

function toForm(q?: Queue): Form {
  const str = (n?: number) => (n === undefined ? '' : String(n));
  return {
    max_attempts: str(q?.max_attempts),
    min_backoff_seconds: str(q?.min_backoff_seconds),
    max_backoff_seconds: str(q?.max_backoff_seconds),
    backoff_multiplier: str(q?.backoff_multiplier),
    timeout_seconds: str(q?.timeout_seconds),
    retry_on_4xx: q?.retry_on_4xx ?? false,
    rate_per_second: str(q?.rate_per_second),
    max_concurrency: str(q?.max_concurrency),
  };
}

const LABELS: Record<Exclude<keyof Form, 'retry_on_4xx'>, string> = {
  max_attempts: 'Max attempts',
  min_backoff_seconds: 'Min backoff',
  max_backoff_seconds: 'Max backoff',
  backoff_multiplier: 'Backoff multiplier',
  timeout_seconds: 'Timeout',
  rate_per_second: 'Rate',
  max_concurrency: 'Max concurrency',
};

/**
 * The whole policy, as PUT expects: an empty rate means no limit. A new
 * queue may leave any field empty, for the server's default.
 */
function toConfig(f: Form, creating: boolean): QueueConfig | string {
  const num = (key: Exclude<keyof Form, 'retry_on_4xx'>, optional = creating): number | undefined | string => {
    const v = f[key].trim();
    if (v === '') return optional ? undefined : `${LABELS[key]} is required.`;
    const n = Number(v);
    return Number.isFinite(n) ? n : `${LABELS[key]} must be a number.`;
  };
  const values = {
    max_attempts: num('max_attempts'),
    min_backoff_seconds: num('min_backoff_seconds'),
    max_backoff_seconds: num('max_backoff_seconds'),
    backoff_multiplier: num('backoff_multiplier'),
    timeout_seconds: num('timeout_seconds'),
    rate_per_second: num('rate_per_second', true),
    max_concurrency: num('max_concurrency'),
  };
  const problem = Object.values(values).find((v) => typeof v === 'string');
  if (problem !== undefined) return problem as string;
  return {
    ...(values as Record<string, number | undefined>),
    retry_on_4xx: f.retry_on_4xx,
  } as QueueConfig;
}

/** Edits a queue's policy, or without `queue` creates a queue. */
export function EditPolicy({ queue, open, onClose }: { queue?: Queue; open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      {/* Mounted per opening, so the form starts from the queue's current policy. */}
      {open && <PolicyForm queue={queue} onClose={onClose} />}
    </Dialog>
  );
}

export const NAME_RE = /^[a-z0-9_-]{1,64}$/;

function PolicyForm({ queue, onClose }: { queue?: Queue; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const creating = queue === undefined;
  const [name, setName] = useState('');
  const [form, setForm] = useState(() => toForm(queue));
  const [problem, setProblem] = useState<string>();
  const save = useMutation({
    ...putQueueMutation(),
    onSuccess: async (q) => {
      await qc.invalidateQueries();
      onClose();
      if (creating) navigate(`/queues/${q.name}`);
    },
  });
  const set = (key: keyof Form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [key]: e.target.value });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const config = toConfig(form, creating);
    if (typeof config === 'string') return setProblem(config);
    const target = creating ? name.trim() : queue.name;
    if (creating) {
      if (!NAME_RE.test(target)) return setProblem("Name: 1-64 lowercase letters, digits, '-' or '_'.");
      // PUT would quietly replace an existing queue's policy.
      const existing = await getQueue({ path: { queue: target } });
      if (existing.response?.ok) return setProblem(`Queue ${target} already exists.`);
    }
    setProblem(undefined);
    save.mutate({ path: { queue: target }, body: config });
  };

  const error = problem ?? (save.error ? errorMessage(save.error) : undefined);

  return (
    <DialogContent className="w-[min(40rem,calc(100vw-2rem))]">
      <form onSubmit={submit} className="flex flex-col gap-5">
        <div className="flex flex-col gap-1 pr-8">
          <DialogTitle className="text-base font-medium">{creating ? 'New queue' : 'Edit policy'}</DialogTitle>
          <p className="text-xs text-muted-foreground">
            {creating ? (
              'Enqueueing to a name creates its queue too; creating it here sets its policy first. Leave a field empty for the default.'
            ) : (
              <>
                Applies to every task in <span className="font-mono">{queue.name}</span> from its next attempt. Tasks
                that set their own max attempts or timeout keep them.
              </>
            )}
          </p>
        </div>

        {creating && (
          <FormGroup title="Queue">
            <FormRow label="Name" hint="Lowercase letters, digits, '-' and '_'. It can't be changed later.">
              <Input
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="emails"
                className="w-64 font-mono"
              />
            </FormRow>
          </FormGroup>
        )}

        <FormGroup title="Retries">
          <FormRow label="Max attempts" hint="Including the first.">
            <Input
              placeholder={creating ? 'default' : undefined}
              inputMode="numeric"
              value={form.max_attempts}
              onChange={set('max_attempts')}
              className="w-28"
            />
          </FormRow>
          <FormRow label="Backoff" hint="Wait before retry n: random, up to min × multiplierⁿ⁻¹, capped at max.">
            <div className="flex flex-wrap items-center gap-2">
              <Unit unit="s">
                <Input
                  placeholder={creating ? 'default' : undefined}
                  inputMode="decimal"
                  aria-label="Min backoff"
                  value={form.min_backoff_seconds}
                  onChange={set('min_backoff_seconds')}
                  className="w-20"
                />
              </Unit>
              <span className="text-faint">→</span>
              <Unit unit="s">
                <Input
                  placeholder={creating ? 'default' : undefined}
                  inputMode="decimal"
                  aria-label="Max backoff"
                  value={form.max_backoff_seconds}
                  onChange={set('max_backoff_seconds')}
                  className="w-24"
                />
              </Unit>
              <span className="text-faint">×</span>
              <Input
                placeholder={creating ? 'default' : undefined}
                inputMode="decimal"
                aria-label="Backoff multiplier"
                value={form.backoff_multiplier}
                onChange={set('backoff_multiplier')}
                className="w-16"
              />
            </div>
          </FormRow>
          <FormRow label="Retry 4xx" hint="Other client errors usually fail again.">
            <Select
              items={RETRY_4XX}
              value={form.retry_on_4xx ? 'all' : 'some'}
              onValueChange={(v) => setForm({ ...form, retry_on_4xx: v === 'all' })}
            >
              <SelectTrigger className="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {RETRY_4XX.map((i) => (
                  <SelectItem key={i.value} value={i.value}>
                    {i.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FormRow>
        </FormGroup>

        <FormGroup title="Delivery">
          <FormRow label="Timeout" hint="How long to wait for a response, up to 1800s.">
            <Unit unit="s">
              <Input
                placeholder={creating ? 'default' : undefined}
                inputMode="decimal"
                value={form.timeout_seconds}
                onChange={set('timeout_seconds')}
                className="w-24"
              />
            </Unit>
          </FormRow>
        </FormGroup>

        <FormGroup title="Limits">
          <FormRow label="Rate" hint="Deliveries started per second. Empty for no limit.">
            <Unit unit="/s">
              <Input
                inputMode="decimal"
                placeholder="unlimited"
                value={form.rate_per_second}
                onChange={set('rate_per_second')}
                className="w-28"
              />
            </Unit>
          </FormRow>
          <FormRow label="Max concurrency" hint="Deliveries in flight at once.">
            <Input
              placeholder={creating ? 'default' : undefined}
              inputMode="numeric"
              value={form.max_concurrency}
              onChange={set('max_concurrency')}
              className="w-28"
            />
          </FormRow>
        </FormGroup>

        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {error && <span className="mr-auto text-xs text-destructive">{error}</span>}
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="default" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : creating ? 'Create queue' : 'Save'}
          </Button>
        </div>
      </form>
    </DialogContent>
  );
}
