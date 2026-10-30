import {execFileSync} from 'node:child_process';
import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';
import {readerPdfAssets} from './scripts/pdf-assets.mjs';
import {enUS} from '../packages/i18n/src/catalog/en-US.ts';
import {aliases, assetNames, browserCheck, browserFloor, kitFamilies, vendoredFiles} from './scripts/config.mjs';
// O-159: every build names the commit it came from, so a report names both the web build and the
// server it talked to. A deploy may set VITE_BUILD_ID itself; outside Git the build has none.
function buildId() {
  if (process.env.VITE_BUILD_ID) return process.env.VITE_BUILD_ID;
  try { return execFileSync('git', ['rev-parse', '--short=9', 'HEAD'], {encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore']}).trim(); } catch { return ''; }
}
export default defineConfig(() => ({
  define: {'import.meta.env.VITE_BUILD_ID': JSON.stringify(buildId())},
  plugins: [react(), readerPdfAssets(), vendoredFiles(), browserCheck(enUS), kitFamilies()],
  resolve: {alias: aliases, dedupe: ['react', 'react-dom']},
  server: {
    host: '127.0.0.1', port: 5174, strictPort: true,
    fs: {allow: ['..']},
    proxy: {'/v1': {target: process.env.PORTICO_DEV_SERVER_URL ?? 'http://127.0.0.1:32500', changeOrigin: false}},
  },
  build: {sourcemap: false, target: browserFloor, cssTarget: browserFloor, assetsInlineLimit: 0, rolldownOptions: {output: assetNames}},
}));
