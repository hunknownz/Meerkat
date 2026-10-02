import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Self-contained IIFE: React bundled in, only global is window.MeerkatUI; CSS emitted as a companion file.
export default defineConfig({
  plugins: [react()],
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    outDir: 'dist/mount', emptyOutDir: true, sourcemap: false, cssCodeSplit: false,
    lib: { entry: 'src/mount.tsx', name: 'MeerkatUI', formats: ['iife'], fileName: () => 'meerkat-ui.js'},
    rollupOptions: { external: [], output: { extend: false, assetFileNames: 'meerkat-ui[extname]' } },
  },
});
