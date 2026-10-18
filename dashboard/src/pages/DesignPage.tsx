import type { ReactNode } from 'react';
import { Pause, RotateCcw, Search } from 'lucide-react';
import { CodePanel } from '../components/CodePanel';
import { ConfirmButton } from '../components/ConfirmButton';
import { OutcomeBadge, StatusCode, TaskStateBadge } from '../components/states';
import { StatCard } from '../components/StatCard';
import { ThemeToggle } from '../components/ThemeToggle';
import { Badge } from '../primitives/badge';
import { Button } from '../primitives/button';
import { Card, CardDescription, CardHeader, CardTitle } from '../primitives/card';
import { Input } from '../primitives/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../primitives/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../primitives/table';
import { Tabs, TabsList, TabsTab } from '../primitives/tabs';

const SURFACES = ['background', 'surface', 'card', 'muted', 'border', 'input'];
const TEXT = ['foreground', 'muted-foreground', 'faint'];
const SIGNALS = ['primary', 'success', 'warning', 'destructive'];
const SELECT_ITEMS = [
  { value: 'all', label: 'All states' },
  { value: 'failed', label: 'Failed' },
  { value: 'pending', label: 'Pending' },
];

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4 border-t border-border pt-6">
      <h2 className="text-xs text-muted-foreground">{title}</h2>
      {children}
    </section>
  );
}

function Swatch({ token }: { token: string }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="h-14 w-28 rounded-md border border-border" style={{ background: `var(--${token})` }} />
      <span className="font-mono text-xs text-muted-foreground">{token}</span>
    </div>
  );
}

/** Live reference for DESIGN.md, at /design. */
export function DesignPage() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex max-w-4xl flex-col gap-10 px-6 py-12">
        <header className="flex items-center justify-between">
          <div>
            <h1 className="text-xl">Design system</h1>
            <p className="text-muted-foreground">Flat surfaces, hairlines, one accent. See DESIGN.md.</p>
          </div>
          <ThemeToggle />
        </header>

        <Section title="Color">
          {[SURFACES, TEXT, SIGNALS].map((group) => (
            <div key={group[0]} className="flex flex-wrap gap-4">
              {group.map((token) => (
                <Swatch key={token} token={token} />
              ))}
            </div>
          ))}
        </Section>

        <Section title="Type">
          <div className="flex flex-col gap-3">
            <span className="text-xl">Page title — 20 / regular</span>
            <span className="font-medium">Emphasis — 14 / medium</span>
            <span>Body — 14 / regular</span>
            <span className="text-muted-foreground">Secondary — 14 / muted</span>
            <span className="text-xs text-faint">Meta — 12 / faint</span>
            <span className="font-mono">task_01J9ZK3M5Q · 503 · 1.2s · 10k+</span>
          </div>
        </Section>

        <Section title="Button">
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="default">
              <RotateCcw />
              Retry
            </Button>
            <Button>
              <Pause />
              Pause
            </Button>
            <Button variant="outline">Outline</Button>
            <Button variant="ghost">Ghost</Button>
            <Button variant="destructive">Destructive</Button>
            <Button size="sm">Small</Button>
            <Button size="icon" variant="ghost" aria-label="Search">
              <Search />
            </Button>
            <ConfirmButton confirmLabel="Cancel 42 pending?" onConfirm={() => {}}>
              Cancel pending
            </ConfirmButton>
          </div>
        </Section>

        <Section title="Badge">
          <div className="flex flex-wrap items-center gap-2">
            <Badge>neutral</Badge>
            <Badge variant="accent">accent</Badge>
            <Badge variant="success">success</Badge>
            <Badge variant="warning">paused</Badge>
            <Badge variant="danger">danger</Badge>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {(['pending', 'scheduled', 'retrying', 'running', 'succeeded', 'failed', 'cancelled'] as const).map((s) => (
              <TaskStateBadge key={s} state={s} />
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {(['success', 'http_error', 'timeout', 'network_error', 'lost'] as const).map((o) => (
              <OutcomeBadge key={o} outcome={o} />
            ))}
          </div>
        </Section>

        <Section title="Input, select & tabs">
          <div className="flex flex-wrap items-center gap-2">
            <Input icon={<Search />} placeholder="Task ID or idempotency key" className="w-64" />
            <Select items={SELECT_ITEMS} defaultValue="all">
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SELECT_ITEMS.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Tabs defaultValue="all">
            <TabsList>
              <TabsTab value="all">All</TabsTab>
              <TabsTab value="pending">Pending</TabsTab>
              <TabsTab value="failed">Failed</TabsTab>
            </TabsList>
          </Tabs>
        </Section>

        <Section title="Table">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Task</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Last status</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow>
                <TableCell className="font-mono text-xs">task_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A</TableCell>
                <TableCell>
                  <TaskStateBadge state="succeeded" />
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <StatusCode code={200} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">3m ago</TableCell>
              </TableRow>
              <TableRow>
                <TableCell className="font-mono text-xs">task_01J9ZK3M5Q8R2T4V6W8X0Y2Z4B</TableCell>
                <TableCell>
                  <TaskStateBadge state="failed" />
                </TableCell>
                <TableCell className="text-right font-mono text-xs">
                  <StatusCode code={503} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">5m ago</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </Section>

        <Section title="Card & code">
          <div className="grid grid-cols-3 gap-3">
            <StatCard label="Pending" value="1,204" detail="1,180 now · 24 scheduled" />
            <Card className="col-span-2">
              <CardHeader>
                <CardTitle>Card title</CardTitle>
                <CardDescription>Cards are a fill plus a hairline. No shadow.</CardDescription>
              </CardHeader>
            </Card>
          </div>
          <CodePanel title={<span className="font-mono">POST https://api.example.com/send-email</span>}>
            {'Content-Type: application/json\n\n{\n  "to": "ada@example.com"\n}'}
          </CodePanel>
        </Section>
      </div>
    </div>
  );
}
