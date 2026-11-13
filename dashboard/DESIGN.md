# Design guidelines

Live reference: **`/design`** in the dashboard (`src/pages/DesignPage.tsx`). Tokens live in `src/tokens.css` (plain CSS, also imported by the docs site), wired into Tailwind in `src/index.css`, and building blocks live in `src/primitives/`.

## Direction

**Flat like Modal, with contrast like Linear.**

- Two surface levels: sidebar and header on `background`, the main content on a slightly lighter `surface`. Beyond that, depth comes from small fills and 1px hairlines, not shadows or nested panels.
- Low contrast by default. Things stand out through weight, color level, and a single accent, not through borders and boxes.

## Principles

1. **No pure black or white.** Charcoal background and off-white text in dark mode. Near-white background and near-black text in light mode.
2. **Three text levels:** `foreground` (content), `muted-foreground` (labels, secondary), `faint` (meta, placeholders, icons).
3. **Weight 400 by default, 500 for emphasis.** Never semibold or bold.
4. **Separate with fills or hairlines, never boxes.** Tables have no outer border. Cards are a fill plus a hairline, with no shadow.
5. **One accent (orange).** Use it only for tints (`bg-primary/12`), dots, icons in active state, focus rings, running tasks and the rare primary CTA.
6. **Status colors are tinted, not solid:** `success`, `warning`, `destructive`, each as `bg-x/12 text-x`. Warning is yellow, not amber, so it never reads as the accent.
7. **Mono (JetBrains Mono) for machine values:** IDs, URLs, status codes, durations, counts. Numbers are tabular.
8. **Dense but breathable.** 28–32px controls, 40px table rows, 24px page gutter.

## Tokens

| token                                 | dark                   | light           | use                                           |
| ------------------------------------- | ---------------------- | --------------- | --------------------------------------------- |
| `background`                          | oklch(0.205 0 0)       | oklch(0.99 0 0) | sidebar, header                               |
| `surface`                             | oklch(0.232 0.0015 17) | 0.99            | main content area                             |
| `card`                                | 0.26                   | 0.97            | cards, inputs, secondary buttons, code panels |
| `muted`                               | 0.295                  | 0.94            | hover, active nav, neutral badge              |
| `border`                              | white 7%               | black 8%        | hairlines                                     |
| `input`                               | white 10%              | black 12%       | control borders                               |
| `foreground`                          | 0.93                   | 0.21            | primary text                                  |
| `muted-foreground`                    | 0.65                   | 0.5             | secondary text, table headers                 |
| `faint`                               | 0.5                    | 0.65            | meta, placeholders, icons                     |
| `primary`                             | orange                 | orange (darker) | accent                                        |
| `success` / `warning` / `destructive` | green / yellow / red   |                 | status tints                                  |
| `warning-fill`                        | = warning              | brighter yellow | warning as a solid mark (run bars)            |

## Type scale

| role                        | class                                           |
| --------------------------- | ----------------------------------------------- |
| Page title                  | `text-xl` (20px, regular)                       |
| Section titles (`Section`)  | default size, `font-medium`                     |
| Emphasis / titles           | `font-medium`                                   |
| Body                        | default (`text-sm`, 14px)                       |
| Labels, table headers, meta | `text-xs text-muted-foreground` or `text-faint` |
| Numbers / IDs               | `font-mono text-xs`                             |

## Components

```tsx
<Button variant="default">Retry now</Button>      // solid orange: at most one per view
<Button>Load more</Button>                        // secondary (default): card fill
<Button variant="outline" | "ghost" | "destructive" size="sm" | "icon" />

<Badge variant="success" dot>succeeded</Badge>    // neutral | accent | success | warning | danger
<Input icon={<Search />} placeholder="Task ID or idempotency key" />
<Select items={[{ value: 'all', label: 'All states' }]}>…</Select>  // always pass items so the trigger shows labels

<Table>…</Table>                                  // no wrapper box
<Card><CardDescription>Due</CardDescription><div className="font-mono text-xl">42</div></Card>
```

Nav: an icon plus a label. Inactive items are `text-muted-foreground`. The active item is `bg-muted text-foreground` with an orange icon.

### Workly conventions

- **Task and run states:** `pending` neutral, `running` accent with a dot (live), `succeeded` success, `failed` danger, `cancelled` neutral and faint, and for schedule runs `skipped` warning (a slot that wasn't delivered; say why: overlap or missed). A pending task due later shows as `scheduled` (neutral), or `retrying` (warning) once an attempt has failed, to match the backlog's Due and Scheduled (`displayState`).
- **Why, not just what:** a failed task says why Workly stopped (attempts used up, or a response it doesn't retry) and where to change that. Lists show the last result as status plus reason, or the outcome when there was no response. A paused queue's pending tasks say "paused" instead of when they'd run.
- **Schedules are their own section**, not part of Queues: Schedules → schedule → run, with the cron expression in words (`describeCron`) and its raw form and timezone underneath. Runs reuse the task building blocks (attempt table, state badges, request panel), never the queue pages. A strip of recent runs (`RecentRuns`) shows a schedule's health at a glance. The Schedules page opens on an **Overview** (`WeekOverview`; the table is under List): a row per schedule with its name and next run, When (the cron in words, the expression and timezone), then Mon–Sun saying when it runs each day, in the schedule's own timezone so it matches the expression: the times when there are up to three ("3:30 AM"), otherwise "every 15m" and, unless all day, the window. It's a timetable, not a history: no run results, no paging between weeks. Expressions that go by the date (monthly, yearly) span the days with their next run. When an expression fires comes from the server (`/v1/cron/preview` with `from` and `until`), never from evaluating cron in the browser.
- **Task links** carry where they were opened from (`useFromHere`), so a task opened from a queue keeps `Queues / emails / task_…` in the breadcrumbs and Queues active in the sidebar.
- **Attempt outcomes:** `success` success; `http_error`, `timeout` and `network_error` danger; `lost` warning (the result is unknown).
- **Attempts:** a table, newest first. Manual retries are divider rows (`bg-muted/40`). The Response column previews the error or body; a row opens the full response in a dialog, where ↑/↓ move between attempts.
- **Code panels** (request body, headers, response): `bg-card` with a hairline, `font-mono text-xs`, JSON pretty-printed.
- **Destructive actions** (cancel pending, cancel) use `ConfirmButton`: a second click confirms. In table rows it is an icon (`size="icon"`) that widens to its confirm label. In a page header they go in the `MoreActions` "⋯" menu as `ConfirmItem`s, which ask twice the same way and, when they can't act, stay listed with the reason underneath.
- **Sorting:** time and count columns sort by clicking their header (`SortableTableHead`): the active column's arrow is orange, others show a faint arrow on hover. Next to the list's filters, `SortSummary` says what the order is and whether it's the default, with Reset once changed. The sort lives in the URL. Pending tasks default to Next run, soonest first, the order they're delivered in; everything else to newest first.
- **Task tables** have a checkbox column and per-row Retry and Cancel. The bulk bar above stays visible, with its buttons disabled until tasks are selected, and each bulk action applies only to the selected tasks it can act on.
- **Charts** (the Queues page's tasks-created chart): plain flex bars, no chart library. The top is a dashed line labelled with the scale, the tallest bar rounded up to 1, 2 or 5 × 10ⁿ and at least 5, so a handful of tasks doesn't read as a spike. Series use `chart-1`…`chart-5`, muted hues clear of the accent and the status colors, so a queue never reads as failed or selected; the busiest queues get a color each and the rest share `chart-other`. Hovering a bar shows its counts; hovering a legend item fades the other series.
- **Empty charts** (`ChartEmpty` in `components/Chart.tsx`) keep their frame, height and time axis, so nothing jumps when data arrives. Faint ghost bars, a fixed wave faded out towards the middle, stand in for the data behind a centered message: what would appear here, and how to make it appear (a link or a button, e.g. "Show the last 7 days"). The scale and legend are hidden. Loading (`ChartSkeleton`) is the same ghost bars, pulsing, without the message. A section with no rows uses `EmptyState`: an icon, what would be here, and how to get it there (success-tinted when empty is good news, as with "Nothing failing").
- **Actions never disappear.** Render every action for an entity; when it doesn't apply, disable it and say why with `DisabledReason`, which opens on hover, tap and keyboard focus. A hidden button reads as a missing feature.
- **Page headers** (`PageHeader`): the entity's state badge sits next to the title (`badge`), not among the actions. Actions go ghost ones first (Edit leads), then the main action (a filled button), then the "⋯" menu with the destructive ones. On narrow screens they wrap below the title.
- **Lists** show what tells rows apart: long IDs shortened to their last characters (`shortId`, the full ID in the title), targets without the scheme, and only the path when every row calls the same host (`formatTarget`, `sharedHost`). A pager appears only when there's more than one page, a search box only from 10 items. Rows with two-line cells get `py-2`.
- **Stats** that belong together are one `StatStrip`: cells divided by hairlines, not a box each. Stats about now and stats over a range are separate strips, side by side, and the range tabs head only the range's ("Now" | "Last 24h"), so the labels don't repeat the range. When a range count and an all-time count of the same thing are both on the page (failed in 24h against the Failed tab), the stat says both.
- **Settings line** (`InlineFields`): a queue's policy or a schedule's delivery is one line of label–value pairs under its title. Values are mono, except words (`prose`: "only 408 and 429", "skip the slot"). A task table has a single row of controls above it (selection, sort, bulk actions).
- **Counts in tables** (`Count`): zeros are faint, so rows with something in them stand out; failed counts are red when non-zero. A success rate is red below `LOW_SUCCESS` (90%), wherever it's shown.
- **Last result** (`LastResult`): one component for tasks and runs: the status code in mono with its reason, or the outcome in words, or why a run was skipped. A state is never a big mono word; in a stat it's a badge with its time.
- **Recent runs** (`RecentRuns`): always the same number of slots, empty ones as faint stubs, newest on the right, so strips line up down a list. Fewer on phones in tables.
- **Run times:** a run's cron slot shows as a time (`Nov 12, 08:00`), the relative time on hover; when it finished stays relative.
- **Top-level pages** (Queues, Tasks, …) show their name once, as the page title, with a one-line description under it; the header's breadcrumbs start on detail pages. A detail page's title carries its key facts underneath in mono (a queue's limits, a schedule's cron and timezone).
- **Header actions are one size** (`PageHeader` sets it), so only the fill marks the main action. A disabled destructive action turns grey, not pale red.
- **Clickable rows** show an arrow next to the name on hover (`RowHint`), and their row actions brighten.
- **Checkboxes** use the `Checkbox` primitive, not the browser's, which is pure white in dark mode.
- **Home** (`/home`, first in the sidebar, where the logo and `/` lead) answers in order: does anything need me, what's happening now, how have deliveries gone, what's in the queues and on the schedule.
  - **Status** comes first: one sentence with a dot in its tone ("3 things need attention", "All quiet", or grey "Quiet" when nothing was enqueued in the range), then one row per queue or schedule that needs a look, worst first: failed tasks (with the biggest error), a failed last run, a queue behind, then paused work, which never raises the tone. Failures are named only here, so no number on Home contradicts another.
  - **Now** is the queues' work at this moment: Due now (with how long the oldest has waited), Scheduled (delayed or retrying) and Running (deliveries in flight). **Last {range}** counts tasks created in it and tasks that _finished_ in it, and says so. The **Deliveries** chart draws the same finished tasks over time, succeeded receding and failed in red on top.
  - Then a short **Queues** table (ones with trouble first, then the busiest) beside **Schedules** as a timeline (next run, a dot for the last run, recent runs), each linking to its page; **groups in progress** only when some have tasks left.
  - Until a first task has been delivered (as far as retention remembers), Home is a checklist instead, whose first step watches the newest task and says why it failed.
- **Phones:** below `sm` the sidebar becomes a drawer opened from the header (`MobileNav`), and the gutter shrinks to 16px. Wide tables scroll sideways inside their container, never the page, but first drop columns so the ones that say whether something is healthy stay in view (the schedules list keeps Last run and Recent, and moves When under the name).

### Don't

- Wrap tables or lists in bordered boxes.
- Use `font-semibold`/`font-bold`, or `shadow-*` on surfaces.
- Use raw Tailwind colors (`amber-500`, `zinc-800`). Use tokens.
- Put solid accent fills on anything that isn't the primary CTA.

---

## Inspiration

Modal (primary), Linear, Trigger.dev.

- **Modal**: charcoal background, flat single surface, the accent used sparingly (active nav tint, tabs), rows separated by a subtle fill, generous whitespace. Code panels look like small windows.
- **Linear**: low-contrast layers only a few % of lightness apart, hairline borders, strict text hierarchy (near-white titles, gray meta). Color appears only in small status icons. The activity feed (no dividers, just spacing) is the model for the attempt timeline.
- Across all of them: commit to one accent, use mono for stats and labels, and use a colored dot + label for "live".
