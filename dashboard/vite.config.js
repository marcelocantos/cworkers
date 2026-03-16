import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { viteSingleFile } from 'vite-plugin-singlefile';

export default defineConfig({
  plugins: [svelte(), viteSingleFile()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:4242',
      '/mcp': 'http://localhost:4242',
    },
  },
});
