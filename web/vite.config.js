import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  base: '/',
  server: {
    host: '127.0.0.1',
    port: 3051,
    strictPort: true,
    headers: {
      'Cache-Control': 'no-store'
    },
    proxy: {
      '/api/': {
        target: 'http://127.0.0.1:3052',
        changeOrigin: true,
        ws: true
      },
      '/auth/': {
        target: 'http://127.0.0.1:3052',
        changeOrigin: true
      },
      '/zo/': {
        target: 'http://127.0.0.1:3052',
        changeOrigin: true
      }
    }
  },
  build: {
    outDir: '../internal/webui/assets',
    emptyOutDir: true
  }
});
