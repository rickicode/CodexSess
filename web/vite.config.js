import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  base: '/',
  server: {
    host: '127.0.0.1',
    port: 3051,
    strictPort: true,
    proxy: {
      '/api': 'http://127.0.0.1:3052',
      '/auth': 'http://127.0.0.1:3052'
    }
  },
  build: {
    outDir: '../internal/webui/assets',
    emptyOutDir: true
  }
});
