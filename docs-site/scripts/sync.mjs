// Copies ../docs, and the SDKs' READMEs, into the Starlight content
// collection, so the docs stay plain Markdown that reads well on GitHub:
//
//   - a page's first "# Heading" becomes its title,
//   - its description, for search results, comes from `descriptions` below,
//   - links between docs (quickstart.md#install) become site links,
//   - links to other repo files (../deploy/...) point at GitHub, at DOCS_REF,
//   - the SDK READMEs become /docs/sdks/python/ and /docs/sdks/typescript/,
//     so the docs, llms-full.txt and their Markdown copies include every
//     method, and links to the READMEs go there,
//   - api/openapi.yaml is published next to the API reference page,
//   - each page is also published as Markdown (/docs/quickstart.md), and
//     its <head> links to it, so agents get the page with its code examples
//     rather than a summary of the HTML. Its links are absolute, since a
//     copy read on its own has no page to resolve them against.
import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, posix, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const site = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repo = resolve(site, '..');
export const docsDir = join(repo, 'docs');
const outDir = join(site, 'src/content/docs');

// The tag being published (vX.Y.Z), so links to repo files match the docs.
// The header shows it too (src/components/SiteTitle.astro).
export const ref = process.env.DOCS_REF || 'main';
const github = `https://github.com/worklydev/workly`;

// Where the site is served (astro.config.mjs `base`): Astro doesn't prefix
// links written in Markdown.
const base = '/docs';
const origin = 'https://workly.run';

// Pages from outside docs/: repo path → where they go in the collection.
const extra = {
  'sdks/python/README.md': 'sdks/python.md',
  'sdks/typescript/README.md': 'sdks/typescript.md',
};

/** Site links for repo paths that the site serves itself. */
const served = {
  'api/openapi.yaml': `${base}/api/`,
  ...Object.fromEntries(Object.entries(extra).map(([from, to]) => [from, `${base}/${to.replace(/\.md$/, '/')}`])),
};

// Meta descriptions, kept here rather than in frontmatter, which GitHub would
// show as a table. Written for a search result: what the page answers, in at
// most about 155 characters. A new page needs one, or the build fails.
const descriptions = {
  // Keyed by where the page goes in the collection.
  'index.md':
    'Workly is an open-source HTTP task queue and cron scheduler: enqueue a task with a URL, and Workly delivers it with retries, rate limits and full history.',
  'quickstart.md':
    'Run Workly locally with workly dev, enqueue a task from Python, TypeScript or curl, and handle it in your own endpoint. About five minutes.',
  'concepts.md':
    'How Workly queues, tasks and attempts work: retries and backoff, at-least-once delivery, delivery headers, and verifying signatures without an SDK.',
  'schedules.md':
    'Call an HTTP endpoint on a cron timetable, in your timezone, with retries, signed requests, run history and a run-now button: cron for HTTP endpoints.',
  'groups.md':
    "Fan out tasks and get one callback when they've all finished, with counts of what succeeded and failed: fan-out and fan-in for an HTTP task queue.",
  'workflows.md':
    'Run HTTP steps in dependency order, each delivered and retried as a task, with steps that wait for a task group: DAG workflows for an HTTP task queue.',
  'callbacks.md':
    'Have Workly POST a summary to your endpoint each time a task or scheduled run succeeds, fails or is cancelled: callbacks for an HTTP task queue.',
  'alerts.md': "Get an email or a Slack message when Workly tasks or schedule runs fail, so you don't have to watch the dashboard.",
  'deploy.md':
    'Keep Workly queues, workflows and schedules in a workly.yaml next to your code: validate it in CI, see the plan, and apply it with workly deploy.',
  'self-hosting.md':
    'Run Workly in production as one container plus Postgres, on Docker, Docker Compose or Kubernetes, next to the apps it delivers to.',
  'mcp.md':
    'Connect Claude Code, Cursor, VS Code or Codex to Workly over MCP, so coding agents can enqueue test tasks, read failures and retry them.',
  'configuration.md':
    'Every Workly server setting: the WORKLY_* environment variables for the database, signing secrets, API key, retention, payload limits, email and logging.',
  'metrics.md':
    "Workly's Prometheus metrics: queue depth, delivery latency, the age of the oldest due task and schedule health, with example alerting rules.",
  'performance.md':
    "Workly's load test results on SQLite and Postgres: enqueue and delivery rates and latencies on one server, batched enqueues, what limits them and how to reproduce them.",
  'cli.md':
    'Every workly command and flag: the dev server, serve, and the CLI for queues, tasks, groups, workflows, schedules and runs, validate, plan and deploy.',
  'sdks/python.md':
    'The Workly Python SDK: enqueue tasks, verify signatures, and manage queues, schedules, task groups, workflows and alerts, sync or async.',
  'sdks/typescript.md':
    'The Workly TypeScript SDK for Node, Bun, Deno and edge runtimes: enqueue tasks, verify signatures, and manage queues, schedules, groups and workflows.',
};

/** The collection path for docs/<file>. */
function target(file) {
  return file === 'README.md' ? 'index.md' : file;
}

/** Rewrites a link in the repo file at `from` (a repo path) for the site. */
function rewriteLink(link, from) {
  if (/^([a-z]+:|#|\/)/i.test(link)) return link;
  const [path, hash = ''] = link.split('#');
  const anchor = hash ? `#${hash}` : '';
  const fromRoot = posix.normalize(posix.join(posix.dirname(from), path));
  if (served[fromRoot]) return served[fromRoot] + anchor;
  if (fromRoot.startsWith('docs/') && fromRoot.endsWith('.md')) {
    const s = target(fromRoot.slice('docs/'.length)).replace(/(^|\/)?index\.md$|\.md$/, '');
    return `${base}/${s ? `${s}/` : ''}` + anchor;
  }
  let kind = 'tree';
  try {
    if (statSync(join(repo, fromRoot)).isFile()) kind = 'blob';
  } catch {
    // A path that doesn't exist yet still gets a sensible link.
  }
  return `${github}/${kind}/${ref}/${fromRoot}${anchor}`;
}

/**
 * Converts the repo file at `from` into the page at `to` (a collection path,
 * which is also where its Markdown copy is served).
 */
function convert(from, to, text) {
  const heading = text.match(/^# (.+)\n+/);
  if (!heading) throw new Error(`${from}: the first line must be a "# Title" heading`);
  // Inline links and images; code spans and blocks don't contain "](".
  const links = (rewrite) => text.slice(heading[0].length).replace(/(\]\()([^)\s]+)(\))/g, (_, open, link, close) => open + rewrite(rewriteLink(link, from)) + close);
  const body = links((link) => link);
  const absolute = links((link) => (link.startsWith('/') ? origin + link : link));
  const description = descriptions[to];
  if (!description) throw new Error(`${from}: add a description for ${to} to docs-site/scripts/sync.mjs`);
  const alternate = { tag: 'link', attrs: { rel: 'alternate', type: 'text/markdown', href: `${base}/${to}` } };
  // JSON is valid YAML, for the strings and the head entry alike.
  const page = `---\ntitle: ${JSON.stringify(heading[1])}\ndescription: ${JSON.stringify(description)}\neditUrl: ${github}/edit/main/${from}\nhead: [${JSON.stringify(alternate)}]\n---\n\n${body}`;
  return { page, markdown: `# ${heading[1]}\n\n${absolute}` };
}

export function sync() {
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true });
  const publicDir = join(site, 'public');
  for (const file of readdirSync(publicDir).filter((f) => f.endsWith('.md'))) rmSync(join(publicDir, file));
  rmSync(join(publicDir, 'sdks'), { recursive: true, force: true });
  const pages = [
    ...readdirSync(docsDir)
      .filter((f) => f.endsWith('.md'))
      .map((f) => [`docs/${f}`, target(f)]),
    ...Object.entries(extra),
  ];
  for (const [from, to] of pages) {
    const { page, markdown } = convert(from, to, readFileSync(join(repo, from), 'utf8'));
    for (const [dir, text] of [[outDir, page], [publicDir, markdown]]) {
      mkdirSync(dirname(join(dir, to)), { recursive: true });
      writeFileSync(join(dir, to), text);
    }
  }
  mkdirSync(join(site, 'public/api'), { recursive: true });
  cpSync(join(repo, 'api/openapi.yaml'), join(site, 'public/api/openapi.yaml'));
}

if (import.meta.url === `file://${process.argv[1]}`) sync();
