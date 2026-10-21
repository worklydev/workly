// Copies ../docs into the Starlight content collection, so the docs stay
// plain Markdown that reads well on GitHub:
//
//   - a page's first "# Heading" becomes its title,
//   - its description, for search results, comes from `descriptions` below,
//   - links between docs (quickstart.md#install) become site links,
//   - links to other repo files (../deploy/...) point at GitHub, at DOCS_REF,
//   - api/openapi.yaml is published next to the API reference page.
import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, posix, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const site = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repo = resolve(site, '..');
export const docsDir = join(repo, 'docs');
const outDir = join(site, 'src/content/docs');

// The tag being published (vX.Y.Z), so links to repo files match the docs.
const ref = process.env.DOCS_REF || 'main';
const github = `https://github.com/worklydev/workly`;

// Where the site is served (astro.config.mjs `base`): Astro doesn't prefix
// links written in Markdown.
const base = '/docs';

/** Site links for repo paths that the site serves itself. */
const served = { 'api/openapi.yaml': `${base}/api/` };

// Meta descriptions, kept here rather than in frontmatter, which GitHub would
// show as a table. Written for a search result: what the page answers, in at
// most about 155 characters. A new page needs one, or the build fails.
const descriptions = {
  'README.md':
    'Workly is an open-source HTTP task queue and cron scheduler: enqueue a task with a URL, and Workly delivers it with retries, rate limits and full history.',
  'quickstart.md':
    'Run Workly locally with workly dev, enqueue a task from Python, TypeScript or curl, and handle it in your own endpoint. About five minutes.',
  'schedules.md':
    'Call an HTTP endpoint on a cron timetable, in your timezone, with retries, signed requests, run history and a run-now button: cron for HTTP endpoints.',
  'groups.md':
    "Fan out tasks and get one callback when they've all finished, with counts of what succeeded and failed: fan-out and fan-in for an HTTP task queue.",
  'alerts.md': "Get an email or a Slack message when Workly tasks or schedule runs fail, so you don't have to watch the dashboard.",
  'deploy.md':
    'Keep Workly queues and schedules in a workly.yaml next to your code: validate it in CI, see the plan, and apply it with workly deploy.',
  'self-hosting.md':
    'Run Workly in production as one container plus Postgres, on Docker, Docker Compose or Kubernetes, next to the apps it delivers to.',
  'mcp.md':
    'Connect Claude Code, Cursor, VS Code or Codex to Workly over MCP, so coding agents can enqueue test tasks, read failures and retry them.',
  'configuration.md':
    'Every Workly server setting: the WORKLY_* environment variables for the database, signing secrets, API key, retention, payload limits, email and logging.',
  'metrics.md':
    "Workly's Prometheus metrics: queue depth, delivery latency, the age of the oldest due task and schedule health, with example alerting rules.",
  'performance.md':
    "Workly's load test results on SQLite and Postgres: over 1,000 tasks a second on one server with millisecond delivery latency, and how to reproduce them.",
};

function slug(file) {
  return file === 'README.md' ? '' : file.replace(/\.md$/, '');
}

function rewriteLink(target) {
  if (/^([a-z]+:|#|\/)/i.test(target)) return target;
  const [path, hash = ''] = target.split('#');
  const anchor = hash ? `#${hash}` : '';
  const fromRoot = posix.normalize(posix.join('docs', path));
  if (served[fromRoot]) return served[fromRoot] + anchor;
  if (fromRoot.startsWith('docs/') && fromRoot.endsWith('.md')) {
    const s = slug(fromRoot.slice('docs/'.length));
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

function convert(file, text) {
  const heading = text.match(/^# (.+)\n+/);
  if (!heading) throw new Error(`docs/${file}: the first line must be a "# Title" heading`);
  const body = text
    .slice(heading[0].length)
    // Inline links and images; code spans and blocks don't contain "](".
    .replace(/(\]\()([^)\s]+)(\))/g, (_, open, target, close) => open + rewriteLink(target) + close);
  const description = descriptions[file];
  if (!description) throw new Error(`docs/${file}: add a description to docs-site/scripts/sync.mjs`);
  // JSON strings are valid YAML double-quoted strings.
  return `---\ntitle: ${JSON.stringify(heading[1])}\ndescription: ${JSON.stringify(description)}\neditUrl: ${github}/edit/main/docs/${file}\n---\n\n${body}`;
}

export function sync() {
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true });
  for (const file of readdirSync(docsDir).filter((f) => f.endsWith('.md'))) {
    const name = file === 'README.md' ? 'index.md' : file;
    writeFileSync(join(outDir, name), convert(file, readFileSync(join(docsDir, file), 'utf8')));
  }
  mkdirSync(join(site, 'public/api'), { recursive: true });
  cpSync(join(repo, 'api/openapi.yaml'), join(site, 'public/api/openapi.yaml'));
}

if (import.meta.url === `file://${process.argv[1]}`) sync();
