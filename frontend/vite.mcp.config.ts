import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// MCP Apps bundle: single self-contained IIFE (React, ext-apps SDK and CSS inlined, no dynamic imports).
// scripts/copy-assets.mjs wraps it into the single-file resource internal/web/assets/mcp/meerkat-app.html.
export default defineConfig({
  plugins: [react()],
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    outDir: 'dist/mcp', emptyOutDir: true, sourcemap: false, cssCodeSplit: false, assetsInlineLimit: Number.MAX_SAFE_INTEGER,
    lib: { entry: 'src/mcp-app.tsx', name: 'MeerkatMcpApp', formats: ['iife'], fileName: () => 'meerkat-app.js' },
    rollupOptions: { external: [], output: { extend: false, inlineDynamicImports: true, assetFileNames: 'meerkat-app[extname]' } },
  },
});
