import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router';
import { startWorkflowRunMutation } from '../../api/@tanstack/react-query.gen';
import type { StartWorkflowRun } from '../../api';
import { FormGroup, FormRow } from '../../components/FormRows';
import { errorMessage } from '../../lib/api';
import { Button } from '../../primitives/button';
import { Dialog, DialogContent, DialogTitle } from '../../primitives/dialog';
import { Input } from '../../primitives/input';
import { Textarea } from '../../primitives/textarea';

/** Starts a run of a workflow, with optional input, and opens it. */
export function TriggerWorkflow({ workflow, open, onClose }: { workflow: string; open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      {open && <TriggerForm workflow={workflow} onClose={onClose} />}
    </Dialog>
  );
}

function TriggerForm({ workflow, onClose }: { workflow: string; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [input, setInput] = useState('');
  const [key, setKey] = useState('');
  const [problem, setProblem] = useState<string>();
  const start = useMutation({
    ...startWorkflowRunMutation(),
    onSuccess: async (run) => {
      await qc.invalidateQueries();
      onClose();
      navigate(`/workflow-runs/${run.id}`);
    },
  });

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const body: StartWorkflowRun = {};
    if (input.trim()) {
      try {
        body.input = JSON.parse(input);
      } catch {
        return setProblem('Input must be JSON.');
      }
    }
    if (key.trim()) body.idempotency_key = key.trim();
    setProblem(undefined);
    start.mutate({ path: { workflow }, body });
  };
  const error = problem ?? (start.error ? errorMessage(start.error) : undefined);

  return (
    <DialogContent className="w-[min(40rem,calc(100vw-2rem))]">
      <form onSubmit={submit} className="flex flex-col gap-5">
        <div className="flex flex-col gap-1 pr-8">
          <DialogTitle className="text-base font-medium">Run {workflow}</DialogTitle>
          <p className="text-xs text-muted-foreground">
            Starts its first steps now, under the workflow's current definition.
          </p>
        </div>
        <FormGroup title="Run">
          <FormRow label="Input" hint="JSON, sent to every step that has no body of its own.">
            <Textarea
              autoFocus
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder='{"date": "2026-11-13"}'
              className="font-mono"
              rows={Math.min(12, Math.max(4, input.split('\n').length))}
            />
          </FormRow>
          <FormRow label="Idempotency key" hint="Optional. With a key already used, that run opens and nothing starts.">
            <Input value={key} onChange={(e) => setKey(e.target.value)} className="font-mono" />
          </FormRow>
        </FormGroup>
        <div className="flex items-center justify-end gap-2">
          {error && <span className="mr-auto text-xs text-destructive">{error}</span>}
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="default" disabled={start.isPending}>
            Start run
          </Button>
        </div>
      </form>
    </DialogContent>
  );
}
