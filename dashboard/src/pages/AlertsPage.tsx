import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Mail, MessageSquare, Plus, Send, Trash2 } from 'lucide-react';
import {
  createAlertChannelMutation,
  deleteAlertChannelMutation,
  listAlertChannelsOptions,
  listAlertChannelsQueryKey,
  testAlertChannelMutation,
} from '../api/@tanstack/react-query.gen';
import type { AlertChannel, AlertChannelType } from '../api';
import { ConfirmButton } from '../components/ConfirmButton';
import { DisabledReason } from '../components/DisabledReason';
import { PageHeader } from '../components/PageHeader';
import { ErrorText, Muted } from '../components/QueryState';
import { RelativeTime } from '../components/RelativeTime';
import { Section } from '../components/Section';
import { errorMessage } from '../lib/api';
import { Button } from '../primitives/button';
import { Input } from '../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../primitives/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../primitives/table';

const MAX_CHANNELS = 10;

/** Where alerts about failed tasks and schedule runs go: email addresses and Slack webhooks. */
export function AlertsPage() {
  const { data, error, isPending } = useQuery(listAlertChannelsOptions());
  const channels = data?.channels ?? [];

  return (
    <>
      <PageHeader
        title="Alerts"
        description="Hear about failed tasks and schedule runs without watching the dashboard."
      />
      {isPending ? (
        <Muted>Loading…</Muted>
      ) : error ? (
        <ErrorText error={error} />
      ) : (
        <div className="flex max-w-3xl flex-col gap-8">
          <Section title="Channels">
            {channels.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No channels yet, so nobody hears when tasks fail. Add an email address or a Slack webhook below.
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Type</TableHead>
                    <TableHead className="w-full">Sends to</TableHead>
                    <TableHead>Added</TableHead>
                    <TableHead className="w-0">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {channels.map((c) => (
                    <ChannelRow key={c.id} channel={c} emailEnabled={data.email_enabled} />
                  ))}
                </TableBody>
              </Table>
            )}
            <AddChannel emailEnabled={data.email_enabled} full={channels.length >= MAX_CHANNELS} />
          </Section>
          <Section title="How alerts work">
            <ul className="flex list-disc flex-col gap-1.5 pl-4 text-sm text-muted-foreground">
              <li>
                When a task or a schedule run fails, after its last attempt, every channel gets an alert within about a
                minute, with the errors and a link to the failures.
              </li>
              <li>
                After that, a queue or schedule gets at most one alert every 30 minutes, covering what failed since the
                previous one. Failures in several queues at once arrive as one alert.
              </li>
              <li>Retries that later succeed, and cancelled tasks, never alert.</li>
            </ul>
          </Section>
        </div>
      )}
    </>
  );
}

const TYPES: { value: AlertChannelType; label: string }[] = [
  { value: 'email', label: 'Email' },
  { value: 'slack', label: 'Slack' },
];

const NO_EMAIL = "This server can't send email. Set WORKLY_SMTP_URL and WORKLY_SMTP_FROM to add email channels.";

function ChannelRow({ channel: c, emailEnabled }: { channel: AlertChannel; emailEnabled: boolean }) {
  const qc = useQueryClient();
  const test = useMutation(testAlertChannelMutation());
  const remove = useMutation({
    ...deleteAlertChannelMutation(),
    onSuccess: () => qc.invalidateQueries({ queryKey: listAlertChannelsQueryKey() }),
  });
  const Icon = c.type === 'email' ? Mail : MessageSquare;
  const cantSend = c.type === 'email' && !emailEnabled ? NO_EMAIL : undefined;
  const result = remove.error
    ? errorMessage(remove.error)
    : test.error
      ? errorMessage(test.error)
      : test.isSuccess
        ? 'Sent. Check it arrived.'
        : undefined;

  return (
    <TableRow>
      <TableCell className="text-muted-foreground">
        <span className="flex items-center gap-2">
          <Icon className="size-4 text-faint" />
          {c.type === 'email' ? 'Email' : 'Slack'}
        </span>
      </TableCell>
      <TableCell className="max-w-0">
        <div className="truncate font-mono text-xs" title={c.target}>
          {c.target}
        </div>
        {(result || cantSend) && (
          <div
            className={`mt-0.5 truncate text-xs ${test.isSuccess && !remove.error ? 'text-success' : 'text-destructive'}`}
            title={result ?? cantSend}
          >
            {result ?? cantSend}
          </div>
        )}
      </TableCell>
      <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
        <RelativeTime iso={c.created_at} />
      </TableCell>
      <TableCell className="py-0">
        <span className="flex items-center justify-end gap-1">
          <DisabledReason reason={cantSend}>
            <Button
              variant="ghost"
              size="sm"
              title="Send a test alert to this channel now"
              disabled={test.isPending || !!cantSend}
              onClick={() => test.mutate({ path: { id: c.id } })}
            >
              <Send />
              {test.isPending ? 'Sending…' : 'Send test'}
            </Button>
          </DisabledReason>
          <ConfirmButton
            size="icon"
            title="Remove this channel"
            confirmLabel="Remove"
            disabled={remove.isPending}
            onConfirm={() => remove.mutate({ path: { id: c.id } })}
          >
            <Trash2 />
          </ConfirmButton>
        </span>
      </TableCell>
    </TableRow>
  );
}

function AddChannel({ emailEnabled, full }: { emailEnabled: boolean; full: boolean }) {
  const qc = useQueryClient();
  const [type, setType] = useState<AlertChannelType>(emailEnabled ? 'email' : 'slack');
  const [target, setTarget] = useState('');
  const add = useMutation({
    ...createAlertChannelMutation(),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: listAlertChannelsQueryKey() });
      setTarget('');
    },
  });
  const blocked = full
    ? `A workspace has at most ${MAX_CHANNELS} channels. Remove one to add another.`
    : type === 'email' && !emailEnabled
      ? NO_EMAIL
      : !target.trim()
        ? type === 'email'
          ? 'Enter an email address.'
          : 'Paste a Slack webhook URL.'
        : undefined;

  return (
    <form
      className="flex flex-col gap-2 pt-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (!blocked) add.mutate({ body: { type, target: target.trim() } });
      }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Select
          items={TYPES}
          value={type}
          onValueChange={(v) => {
            setType(v as AlertChannelType);
            add.reset();
          }}
        >
          <SelectTrigger className="w-28" aria-label="Channel type">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TYPES.map((t) => (
              <SelectItem key={t.value} value={t.value} disabled={t.value === 'email' && !emailEnabled}>
                {t.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          placeholder={type === 'email' ? 'oncall@example.com' : 'https://hooks.slack.com/services/…'}
          aria-label={type === 'email' ? 'Email address' : 'Slack webhook URL'}
          type={type === 'email' ? 'email' : 'url'}
          className="min-w-64 flex-1 font-mono"
          disabled={full}
        />
        <DisabledReason reason={blocked}>
          <Button type="submit" variant="default" disabled={!!blocked || add.isPending}>
            <Plus />
            {add.isPending ? 'Adding…' : 'Add channel'}
          </Button>
        </DisabledReason>
      </div>
      {add.error ? (
        <p className="text-xs text-destructive">{errorMessage(add.error)}</p>
      ) : (
        <p className="text-xs text-faint">
          {type === 'email' ? (
            emailEnabled ? (
              'Alerts arrive as plain-text email.'
            ) : (
              NO_EMAIL
            )
          ) : (
            <>
              In Slack, create an{' '}
              <a
                href="https://api.slack.com/messaging/webhooks"
                target="_blank"
                rel="noreferrer"
                className="underline hover:text-muted-foreground"
              >
                incoming webhook
              </a>{' '}
              for the channel alerts should go to, and paste its URL. It's kept secret: only its end is shown here.
            </>
          )}
        </p>
      )}
    </form>
  );
}
