import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// `npm run dev` proxies the API to a local `workly dev` (just dev).
const api = process.env.WORKLY_URL ?? 'http://localhost:7337';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Served from the binary, usually on a local network: one bundle is fine.
  build: { chunkSizeWarningLimit: 800 },
  server: {
    proxy: { '/v1': api, '/healthz': api },
  },
});
