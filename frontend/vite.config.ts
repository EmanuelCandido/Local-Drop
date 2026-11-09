import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8088',
      '/ws': { target: 'ws://127.0.0.1:8088', ws: true },
    },
  },
  build: { outDir: 'dist' },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
});
