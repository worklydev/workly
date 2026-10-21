import starlight from '@astrojs/starlight';
import { defineConfig } from 'astro/config';
import starlightLlmsTxt from 'starlight-llms-txt';
import { docsDir, sync } from './scripts/sync.mjs';

/** Keeps src/content/docs in step with ../docs, including while `astro dev` runs. */
const docs = {
  name: 'workly-docs-sync',
  hooks: {
    'astro:config:setup': () => sync(),
    'astro:server:setup': ({ server }) => {
      server.watcher.add(docsDir);
      server.watcher.on('all', (_, path) => {
        if (path.startsWith(docsDir)) sync();
      });
    },
  },
};

export default defineConfig({
  // Served at workly.run/docs by a Workers route, so search engines and
  // assistants see one site. `base` only prefixes links; outDir puts the files
  // under docs/ too, since the route passes the full path to the Worker.
  site: 'https://workly.run',
  base: '/docs',
  outDir: './dist/docs',
  integrations: [
    docs,
    starlight({
      title: 'Workly',
      description: 'The open-source HTTP task queue.',
      favicon: '/favicon.svg',
      logo: { src: './public/favicon.svg', alt: '' },
      customCss: ['./src/theme.css'],
      // /docs/llms.txt, llms-full.txt and llms-small.txt, so an assistant can
      // read all the docs in one fetch. workly.run/llms.txt links here.
      plugins: [
        starlightLlmsTxt({
          description:
            'Workly is an open-source HTTP task queue and cron scheduler with built-in diagnostics. Your app enqueues a task with a URL and a JSON payload; Workly delivers it as a signed HTTP POST, retries it with backoff until it gets a 2xx, and keeps every attempt. It runs as one binary (SQLite locally, Postgres in production), in your own infrastructure, or hosted at workly.run.',
          details: [
            '- Handlers are plain HTTP endpoints in any language; there are Python (`pip install workly`) and TypeScript (`npm install @worklydev/sdk`) SDKs, a CLI and an MCP server.',
            '- `workly dev` runs a local server and dashboard on localhost:7337 with no configuration. Hosted Workly is at https://api.workly.run with an API key.',
            '- Delivery is at least once: handlers should be idempotent, using the `Workly-Task-Id` header.',
          ].join('\n'),
          // Starlight's "Section titled …" heading links are noise in plain text.
          customSelectors: { all: ['.sl-anchor-link'] },
          optionalLinks: [
            { label: 'API reference', url: 'https://workly.run/docs/api/openapi.yaml', description: 'the OpenAPI spec for the HTTP API' },
            { label: 'workly.run', url: 'https://workly.run/llms.txt', description: 'the product, pricing and comparisons with other tools' },
            { label: 'Source', url: 'https://github.com/worklydev/workly' },
          ],
        }),
      ],
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/worklydev/workly' }],
      sidebar: [
        {
          label: 'Start here',
          items: [
            { label: 'Overview', link: '/' },
            { slug: 'quickstart' },
          ],
        },
        {
          label: 'Guides',
          items: [{ slug: 'schedules' }, { slug: 'groups' }, { slug: 'alerts' }, { slug: 'deploy' }, { slug: 'self-hosting' }, { slug: 'mcp', label: 'MCP' }],
        },
        {
          label: 'Reference',
          items: [
            { slug: 'configuration' },
            { label: 'API reference', link: '/api/' },
            { slug: 'metrics' },
            { slug: 'performance' },
          ],
        },
        { label: 'workly.run', link: 'https://workly.run' },
      ],
    }),
  ],
});
