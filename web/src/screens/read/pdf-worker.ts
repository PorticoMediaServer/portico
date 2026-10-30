import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url';

/** Bundled from the same pinned PDF.js dependency as the canonical format adapter. */
export const readerPdfWorkerUrl: string = workerUrl;

/** PDF.js6.4 fetches JBIG2/JPX/colour decoders by their SDK filenames. The
 * complete immutable decoder/fallback graph is staged alongside this worker.
 * Existing CSP may reject WASM compilation; the same-version JS fallback is
 * available at the same local base without adding unsafe-eval or a network URL. */
export const readerPdfAdapterOptions = Object.freeze({
  workerUrl: readerPdfWorkerUrl,
  wasmUrl: import.meta.env.DEV ? new URL('/__portico_pdf_codecs__/', import.meta.url).href
    : new URL('./pdfjs/6.4.299/wasm/', new URL(readerPdfWorkerUrl, import.meta.url)).href,
});
