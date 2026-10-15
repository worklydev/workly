# Design guidelines

Live reference: **`/design`** in the dashboard (`src/pages/DesignPage.tsx`). Tokens live in `src/index.css`, and building blocks live in `src/primitives/`.

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
| token | dark | light | use |
|---|---|---|---|
| `background` | oklch(0.205 0 0) | oklch(0.99 0 0) | sidebar, header |
| `surface` | oklch(0.232 0.0015 17) | 0.99 | main content area |
| `card` | 0.26 | 0.97 | cards, inputs, secondary buttons, code panels |
| `muted` | 0.295 | 0.94 | hover, active nav, neutral badge |
| `border` | white 7% | black 8% | hairlines |
| `input` | white 10% | black 12% | control borders |
| `foreground` | 0.93 | 0.21 | primary text |
| `muted-foreground` | 0.65 | 0.5 | secondary text, table headers |
| `faint` | 0.5 | 0.65 | meta, placeholders, icons |
| `primary` | orange | orange (darker) | accent |
| `success` / `warning` / `destructive` | green / yellow / red | | status tints |

## Type scale
| role | class |
|---|---|
| Page title | `text-xl` (20px, regular) |
| Emphasis / titles | `font-medium` |
| Body | default (`text-sm`, 14px) |
| Labels, table headers, meta | `text-xs text-muted-foreground` or `text-faint` |
| Numbers / IDs | `font-mono text-xs` |

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
- **Schedules are their own section**, not part of Queues: Schedules → schedule → run, with the cron expression in words (`describeCron`) and its raw form and timezone underneath. Runs reuse the task building blocks (attempt table, state badges, request panel), never the queue pages. A strip of recent runs (`RecentRuns`) shows a schedule's health at a glance.
- **Task links** carry where they were opened from (`useFromHere`), so a task opened from a queue keeps `Queues / emails / task_…` in the breadcrumbs and Queues active in the sidebar.
- **Attempt outcomes:** `success` success; `http_error`, `timeout` and `network_error` danger; `lost` warning (the result is unknown).
- **Attempts:** a table, newest first. Manual retries are divider rows (`bg-muted/40`). The Response column previews the error or body; a row opens the full response in a dialog, where ↑/↓ move between attempts.
- **Code panels** (request body, headers, response): `bg-card` with a hairline, `font-mono text-xs`, JSON pretty-printed.
- **Destructive actions** (cancel pending, cancel) use `ConfirmButton`: a second click confirms. In table rows it is an icon (`size="icon"`) that widens to its confirm label.
- **Task tables** have a checkbox column and per-row Retry and Cancel. The bulk bar above stays visible, with its buttons disabled until tasks are selected, and each bulk action applies only to the selected tasks it can act on.
- **Charts** (the Queues page's tasks-created chart): plain flex bars, no chart library. Series use `chart-1`…`chart-5`, muted hues clear of the accent and the status colors, so a queue never reads as failed or selected; the busiest queues get a color each and the rest share `chart-other`. Hovering a bar shows its counts; hovering a legend item fades the other series.
- **Actions never disappear.** Render every action for an entity; when it doesn't apply, disable it and say why with `DisabledReason`. A hidden button reads as a missing feature.

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
