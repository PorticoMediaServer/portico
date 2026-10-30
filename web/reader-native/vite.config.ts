import {defineConfig} from 'vite';
import react from '@vitejs/plugin-react';
import {readerPdfAssets} from '../scripts/pdf-assets.mjs';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {aliases, assetNames} from '../scripts/config.mjs';
/** Immutable app-only graph: no shell/account/service worker/server entry. */
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)), base: './', plugins: [react(), readerPdfAssets(), {name: 'reader-bundled-notices', generateBundle() {this.emitFile({type: 'asset', fileName: 'licences.json', source: readFileSync(new URL('../src/generated/licences.json', import.meta.url), 'utf8')});}}],
  resolve: {alias: aliases, dedupe: ['react', 'react-dom']},
  build: {outDir: process.env.PORTICO_READER_OUT_DIR ?? '../../dist/PorticoReader.bundle', emptyOutDir: true,
    target: ['chrome111', 'safari15'], cssTarget: ['chrome111', 'safari15'], assetsInlineLimit: 0,
    rolldownOptions: {output: assetNames}},
});
