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
        changeOrigin: false,
        ws: true
      },
      '/auth/': {
        target: 'http://127.0.0.1:3052',
        changeOrigin: false
      },
      '/zo/': {
        target: 'http://127.0.0.1:3052',
        changeOrigin: false
      }
    }
  },
  build: {
    rolldownOptions: {
      checks: {
        pluginTimings: false
      }
    },
    outDir: '../internal/webui/assets',
    emptyOutDir: true
  }
});
