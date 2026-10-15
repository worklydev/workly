import starlight from '@astrojs/starlight';
import { defineConfig } from 'astro/config';
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
  site: 'https://docs.workly.run',
  integrations: [
    docs,
    starlight({
      title: 'Workly',
      description: 'The open-source HTTP task queue.',
      favicon: '/favicon.svg',
      logo: { src: './public/favicon.svg', alt: '' },
      customCss: ['./src/theme.css'],
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
          items: [{ slug: 'schedules' }, { slug: 'alerts' }, { slug: 'self-hosting' }, { slug: 'mcp', label: 'MCP' }],
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
