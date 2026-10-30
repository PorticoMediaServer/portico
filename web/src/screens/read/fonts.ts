import {loadLiterataFont, ReaderError, throwIfAborted, type EpubAdapterOptions} from '@portico/reader-core';
import {sha256} from '@noble/hashes/sha2.js';

const manropeUrl = new URL('../../kit/fonts/manrope-var-latin.woff2', import.meta.url).href;
const manropeSize = 24836;
const manropeHash = 'a30ddcd349703aff7464c34bef3fffdff405ee50c113440d7c8693c02d210972';
export type ReadFonts = Readonly<{fonts: EpubAdapterOptions['fonts']; dispose: () => void}>;

/** Trusted bundled app asset, never the selected server or a book-provided URL. */
async function loadManrope(signal: AbortSignal): Promise<Blob> {
  const response = await fetch(manropeUrl, {credentials: 'omit', signal});
  if (!response.ok || !response.body) throw new ReaderError('source_unavailable');
  const reader = response.body.getReader(), chunks: Uint8Array<ArrayBuffer>[] = [];
  let size = 0;
  const cancel = () => {void reader.cancel().catch(() => {});};
  signal.addEventListener('abort', cancel, {once: true});
  try {
    for (;;) {
      throwIfAborted(signal);
      const part = await reader.read(); if (part.done) break;
      size += part.value.byteLength;
      if (size > manropeSize) throw new ReaderError('invalid_source');
      chunks.push(part.value.slice());
    }
    if (size !== manropeSize) throw new ReaderError('invalid_source');
    const blob = new Blob(chunks, {type: 'font/woff2'});
    const hash = Array.from(sha256(new Uint8Array(await blob.arrayBuffer())), value => value.toString(16).padStart(2, '0')).join('');
    throwIfAborted(signal);
    if (hash !== manropeHash) throw new ReaderError('invalid_source');
    return blob;
  } finally { signal.removeEventListener('abort', cancel); await reader.cancel().catch(() => {}); reader.releaseLock(); }
}

/** Call from the actual connected host lifetime. One verified Literata Blob
 * supplies both the app FontFace and createEpubAdapter({fonts}); no copied font. */
export async function loadReadFonts({document, signal}: Readonly<{document: Document; signal: AbortSignal}>): Promise<ReadFonts> {
  const life = new AbortController(), active = life.signal;
  let face: FontFace | undefined, disposed = false, timedOut = false;
  let deadline: ReturnType<typeof setTimeout> | undefined;
  const clearDeadline = () => {if (deadline !== undefined) {clearTimeout(deadline); deadline = undefined;}};
  const dispose = () => {
    if (disposed) return;
    disposed = true; clearDeadline(); life.abort(signal.reason); signal.removeEventListener('abort', dispose);
    if (face) document.fonts.delete(face);
  };
  signal.addEventListener('abort', dispose, {once: true});
  deadline = setTimeout(() => {timedOut = true; dispose();}, 30_000);
  if (signal.aborted) dispose();
  let onAbort: (() => void) | undefined;
  const aborted = new Promise<never>((_, reject) => {
    onAbort = () => reject(new ReaderError(timedOut ? 'source_unavailable' : 'aborted'));
    if (active.aborted) onAbort(); else active.addEventListener('abort', onAbort, {once: true});
  });
  const bootstrap = async () => {
    throwIfAborted(active);
    const [literata, manrope] = await Promise.all([loadLiterataFont({signal: active}), loadManrope(active)]);
    const bytes = await literata.arrayBuffer(); throwIfAborted(active);
    face = new FontFace('Literata', bytes, {style: 'normal', weight: '200 900'});
    await face.load(); throwIfAborted(active);
    document.fonts.add(face);
    return Object.freeze({fonts: Object.freeze({literata, manrope}), dispose});
  };
  try {
    const result = await Promise.race([bootstrap(), aborted]);
    clearDeadline();
    return result;
  } catch (error) { dispose(); throw error; }
  finally {if (onAbort) active.removeEventListener('abort', onAbort);}
}
