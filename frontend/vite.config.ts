import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Normal browser static bundle (served by the Go binary at /).
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist/app', emptyOutDir: true, assetsInlineLimit: 0, sourcemap: false },
});
