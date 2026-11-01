import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';
import {readerPdfAssets} from './scripts/pdf-assets.mjs';
import {enUS} from '../packages/i18n/src/catalog/en-US.ts';
import {aliases, assetNames, browserCheck, browserFloor, kitFamilies, vendoredFiles} from './scripts/config.mjs';
// O-169 (C7): this bundle is served before sign-in, so it carries no build id. The owner reads
// the server's build from the authenticated updates report after signing in.
export default defineConfig(() => ({
  plugins: [react(), readerPdfAssets(), vendoredFiles(), browserCheck(enUS), kitFamilies()],
  resolve: {alias: aliases, dedupe: ['react', 'react-dom']},
  server: {
    host: '127.0.0.1', port: 5174, strictPort: true,
    fs: {allow: ['..']},
    proxy: {'/v1': {target: process.env.PORTICO_DEV_SERVER_URL ?? 'http://127.0.0.1:32500', changeOrigin: false}},
  },
  build: {sourcemap: false, target: browserFloor, cssTarget: browserFloor, assetsInlineLimit: 0, rolldownOptions: {output: assetNames}},
}));
