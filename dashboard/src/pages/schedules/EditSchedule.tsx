import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { getSchedule } from '../../api';
import { putScheduleMutation } from '../../api/@tanstack/react-query.gen';
import type { HttpMethod, MissedPolicy, OverlapPolicy, Schedule, ScheduleConfig } from '../../api';
import { FormGroup, FormRow, Unit } from '../../components/FormRows';
import { errorMessage } from '../../lib/api';
import { RETRY_4XX } from '../../lib/policy';
import { describeCron } from '../../lib/cron';
import { CronPreview } from './CronPreview';
import { bodyFields, formatBody, formatHeaders, parseHeaders } from '../../lib/request';
import { Button } from '../../primitives/button';
import { Dialog, DialogContent, DialogTitle } from '../../primitives/dialog';
import { Input } from '../../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../primitives/select';
import { Textarea } from '../../primitives/textarea';

type NumberField =
  'timeout_seconds' | 'max_attempts' | 'min_backoff_seconds' | 'max_backoff_seconds' | 'backoff_multiplier';

type Form = Record<
  NumberField | 'cron' | 'timezone' | 'url' | 'workflow' | 'headers' | 'body' | 'callback_url',
  string
> & {
  /** Whether each run calls a URL or starts a workflow run. */
  target: 'url' | 'workflow';
  method: HttpMethod;
  retry_on_4xx: boolean;
  overlap: OverlapPolicy;
  missed: MissedPolicy;
};

function toForm(s?: Schedule): Form {
  if (!s) {
    // A new schedule: empty numbers take the server's defaults.
    return {
      cron: '',
      timezone: 'UTC',
      target: 'url',
      method: 'POST',
      url: '',
      workflow: '',
      headers: '',
      body: '',
      timeout_seconds: '',
      max_attempts: '',
      min_backoff_seconds: '',
      max_backoff_seconds: '',
      backoff_multiplier: '',
      retry_on_4xx: false,
      overlap: 'skip',
      missed: 'skip',
      callback_url: '',
    };
  }
  return {
    cron: s.cron,
    timezone: s.timezone,
    target: s.workflow ? 'workflow' : 'url',
    method: s.method,
    url: s.url,
    workflow: s.workflow ?? '',
    headers: formatHeaders(s.headers),
    body: formatBody(s),
    timeout_seconds: String(s.timeout_seconds),
    max_attempts: String(s.max_attempts),
    min_backoff_seconds: String(s.min_backoff_seconds),
    max_backoff_seconds: String(s.max_backoff_seconds),
    backoff_multiplier: String(s.backoff_multiplier),
    retry_on_4xx: s.retry_on_4xx,
    overlap: s.overlap,
    missed: s.missed,
    callback_url: s.on_complete?.url ?? '',
  };
}

const LABELS: Record<NumberField, string> = {
  timeout_seconds: 'Timeout',
  max_attempts: 'Max attempts',
  min_backoff_seconds: 'Min backoff',
  max_backoff_seconds: 'Max backoff',
  backoff_multiplier: 'Backoff multiplier',
};

/**
 * The whole schedule, as PUT expects. Headers, Content-Type included, go back
 * as they were, so a body keeps its type; a JSON body without a Content-Type
 * is sent as a payload, so it gets application/json.
 */
function toConfig(f: Form, creating: boolean, current?: Schedule): ScheduleConfig | string {
  const config: ScheduleConfig = {
    cron: f.cron.trim(),
    timezone: f.timezone.trim() || 'UTC',
    retry_on_4xx: f.retry_on_4xx,
    overlap: f.overlap,
    missed: f.missed,
  };
  if (!config.cron) return 'Cron is required.';
  if (f.target === 'workflow') {
    config.workflow = f.workflow.trim();
    if (!config.workflow) return 'Workflow is required.';
  } else {
    config.method = f.method;
    config.url = f.url.trim();
    if (!config.url) return 'URL is required.';
  }
  // The form edits the callback's URL; headers set through the API stay.
  const callback = f.callback_url.trim();
  if (callback) config.on_complete = { ...current?.on_complete, url: callback };
  for (const key of Object.keys(LABELS) as NumberField[]) {
    const v = f[key].trim();
    const n = Number(v);
    if (v === '' && creating) continue;
    if (v === '') return `${LABELS[key]} is required.`;
    if (!Number.isFinite(n)) return `${LABELS[key]} must be a number.`;
    config[key] = n;
  }

  if (f.target === 'workflow') {
    // The body is the run's input, so it has to be JSON.
    if (!f.body.trim()) return config;
    try {
      return { ...config, payload: JSON.parse(f.body) };
    } catch {
      return 'Input must be JSON.';
    }
  }
  const headers = parseHeaders(f.headers);
  if (typeof headers === 'string') return headers;
  config.headers = headers;
  return { ...config, ...bodyFields(f.body, headers) };
}

/** Edits a schedule, or without `schedule` creates one. */
export function EditSchedule({ schedule, open, onClose }: { schedule?: Schedule; open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      {/* Mounted per opening, so the form starts from the schedule as it is. */}
      {open && <ScheduleForm schedule={schedule} onClose={onClose} />}
    </Dialog>
  );
}

const METHODS: HttpMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
const TARGETS = [
  { value: 'url', label: 'Calls a URL' },
  { value: 'workflow', label: 'Starts a workflow' },
];
const OVERLAP = [
  { value: 'skip', label: 'Skip the slot' },
  { value: 'allow', label: 'Run alongside' },
];
const MISSED = [
  { value: 'skip', label: 'Skip, record it' },
  { value: 'catch_up', label: 'Catch up once' },
];
const TIMEZONES = Intl.supportedValuesOf('timeZone');

const NAME_RE = /^[a-z0-9_-]{1,64}$/;

function ScheduleForm({ schedule, onClose }: { schedule?: Schedule; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const creating = schedule === undefined;
  const [name, setName] = useState('');
  const [form, setForm] = useState(() => toForm(schedule));
  const [problem, setProblem] = useState<string>();
  const save = useMutation({
    ...putScheduleMutation(),
    onSuccess: async (s) => {
      await qc.invalidateQueries();
      onClose();
      if (creating) navigate(`/schedules/${s.name}`);
    },
  });
  const set = (key: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setForm({ ...form, [key]: e.target.value });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const config = toConfig(form, creating, schedule);
    if (typeof config === 'string') return setProblem(config);
    const target = creating ? name.trim() : schedule.name;
    if (creating) {
      if (!NAME_RE.test(target)) return setProblem("Name: 1-64 lowercase letters, digits, '-' or '_'.");
      // PUT would quietly replace an existing schedule.
      const existing = await getSchedule({ path: { schedule: target } });
      if (existing.response?.ok) return setProblem(`Schedule ${target} already exists.`);
    }
    setProblem(undefined);
    save.mutate({ path: { schedule: target }, body: config });
  };

  const error = problem ?? (save.error ? errorMessage(save.error) : undefined);
  const inWords = describeCron(form.cron);

  return (
    <DialogContent className="w-[min(44rem,calc(100vw-2rem))]">
      <form onSubmit={submit} className="flex flex-col gap-5">
        <div className="flex flex-col gap-1 pr-8">
          <DialogTitle className="text-base font-medium">{creating ? 'New schedule' : 'Edit schedule'}</DialogTitle>
          <p className="text-xs text-muted-foreground">
            {creating ? (
              'Calls a URL on a cron timetable, with retries and a history of every run. Empty retry settings take the defaults.'
            ) : (
              <>
                Replaces the settings of <span className="font-mono">{schedule.name}</span>. Saving doesn't pause or
                resume it.
              </>
            )}
          </p>
        </div>

        {creating && (
          <FormGroup title="Schedule">
            <FormRow label="Name" hint="Lowercase letters, digits, '-' and '_'. It can't be changed later.">
              <Input
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="nightly-report"
                className="w-64 font-mono"
              />
            </FormRow>
          </FormGroup>
        )}

        <FormGroup title="Timetable">
          <FormRow
            label="Cron"
            hint={inWords ?? 'Minute, hour, day of month, month, day of week; or @hourly, @daily, @weekly, @monthly.'}
          >
            <Input value={form.cron} onChange={set('cron')} placeholder="0 3 * * *" className="w-64 font-mono" />
          </FormRow>
          <FormRow label="Timezone" hint="IANA name, e.g. Europe/Stockholm.">
            <Input
              value={form.timezone}
              onChange={set('timezone')}
              placeholder="UTC"
              list="schedule-timezones"
              className="w-64 font-mono"
            />
            <datalist id="schedule-timezones">
              {TIMEZONES.map((tz) => (
                <option key={tz} value={tz} />
              ))}
            </datalist>
          </FormRow>
          <FormRow label="If still running" hint="When a slot comes up while the previous run is unfinished.">
            <Choice
              items={OVERLAP}
              value={form.overlap}
              onChange={(v) => setForm({ ...form, overlap: v as OverlapPolicy })}
            />
          </FormRow>
          <FormRow label="If missed" hint="Slots missed while the server was down. At most one run either way.">
            <Choice
              items={MISSED}
              value={form.missed}
              onChange={(v) => setForm({ ...form, missed: v as MissedPolicy })}
            />
          </FormRow>
          <FormRow label="Next runs">
            <CronPreview cron={form.cron} timezone={form.timezone} />
          </FormRow>
        </FormGroup>

        <FormGroup title="Request">
          <FormRow label="Each run">
            <Choice
              items={TARGETS}
              value={form.target}
              onChange={(v) => setForm({ ...form, target: v as Form['target'] })}
            />
          </FormRow>
          {form.target === 'workflow' ? (
            <>
              <FormRow label="Workflow" hint="Its name, as in workly.yaml.">
                <Input value={form.workflow} onChange={set('workflow')} className="font-mono" />
              </FormRow>
              <FormRow label="Input" hint="JSON, the run's input: sent to every step without a body of its own.">
                <Textarea
                  value={form.body}
                  onChange={set('body')}
                  placeholder='{"kind": "daily"}'
                  className="font-mono"
                  rows={Math.min(12, Math.max(3, form.body.split('\n').length))}
                />
              </FormRow>
            </>
          ) : (
            <>
              <FormRow label="Endpoint">
                <div className="flex items-center gap-2">
                  <Choice
                    items={METHODS.map((m) => ({ value: m, label: m }))}
                    value={form.method}
                    onChange={(v) => setForm({ ...form, method: v as HttpMethod })}
                    className="w-28 font-mono"
                  />
                  <Input value={form.url} onChange={set('url')} placeholder="https://" className="font-mono" />
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
              <FormRow
                label="Body"
                hint="JSON is sent as application/json unless a Content-Type header says otherwise."
              >
                <Textarea
                  value={form.body}
                  onChange={set('body')}
                  placeholder='{"kind": "daily"}'
                  rows={Math.min(12, Math.max(3, form.body.split('\n').length))}
                />
              </FormRow>
            </>
          )}
        </FormGroup>

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
            <Choice
              items={RETRY_4XX}
              value={form.retry_on_4xx ? 'all' : 'some'}
              onChange={(v) => setForm({ ...form, retry_on_4xx: v === 'all' })}
            />
          </FormRow>
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

        <FormGroup title="Callback">
          <FormRow
            label="URL"
            hint="Optional. Workly POSTs a summary of each run here when it succeeds, fails or is cancelled."
          >
            <Input
              value={form.callback_url}
              onChange={set('callback_url')}
              placeholder="https://"
              className="font-mono"
            />
          </FormRow>
        </FormGroup>

        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {error && <span className="mr-auto text-xs text-destructive">{error}</span>}
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="default" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : creating ? 'Create schedule' : 'Save'}
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
  className = 'w-48',
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
