// Copies ../docs into the Starlight content collection, so the docs stay
// plain Markdown that reads well on GitHub:
//
//   - a page's first "# Heading" becomes its title,
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
  const title = heading[1].replaceAll('"', '\\"');
  return `---\ntitle: "${title}"\neditUrl: ${github}/edit/main/docs/${file}\n---\n\n${body}`;
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
