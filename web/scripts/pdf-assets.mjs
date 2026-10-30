import {lstatSync, readFileSync, readdirSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

/** The matching pinned decoder graph is immutable app code, never PDF bytes or
 * a selected-server URL. Keep SDK basenames: PDF.js requests them by name. */
export function readerPdfAssets() {
  const root = dirname(fileURLToPath(import.meta.resolve('pdfjs-dist/package.json')));
  const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  if (manifest.version !== '6.4.299') throw new Error('reader_pdf_codec_version_mismatch');
  const directory = join(root, 'wasm'), files = new Map(); let total = 0;
  for (const name of readdirSync(directory).sort()) {
    if (!/^[a-zA-Z0-9_-]+\.(?:wasm|js)$/.test(name)) continue;
    const target = join(directory, name), info = lstatSync(target);
    if (!info.isFile() || info.size < 1 || info.size > 16 * 1024 * 1024) throw new Error('reader_pdf_codec_asset_invalid');
    const bytes = readFileSync(target); total += bytes.length;
    if (bytes.length !== info.size || files.size >= 32 || total > 64 * 1024 * 1024) throw new Error('reader_pdf_codec_asset_limit');
    files.set(name, bytes);
  }
  if (!files.has('jbig2.wasm') || !files.has('jbig2_nowasm_fallback.js')) throw new Error('reader_pdf_jbig2_assets_missing');
  return {
    name: 'portico-reader-pdf-codecs',
    generateBundle() {
      for (const [name, source] of files) this.emitFile({type: 'asset', fileName: `assets/pdfjs/${manifest.version}/wasm/${name}`, source});
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const match = /^\/__portico_pdf_codecs__\/([a-zA-Z0-9_-]+\.(?:wasm|js))$/.exec((request.url ?? '').split('?')[0]);
        if (!match) return next();
        const bytes = files.get(match[1]);
        if (!bytes || request.method !== 'GET' && request.method !== 'HEAD') {response.statusCode = 404; response.end(); return;}
        response.setHeader('Content-Type', match[1].endsWith('.wasm') ? 'application/wasm' : 'text/javascript');
        response.setHeader('Content-Length', bytes.length);
        response.setHeader('Cache-Control', 'no-cache');
        response.end(request.method === 'HEAD' ? undefined : bytes);
      });
    },
  };
}
