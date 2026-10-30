import {Reader, ZipReader, type Entry, type FileEntry} from '@zip.js/zip.js';
import type {ReaderSource} from '../ports.ts';
import {readRange} from '../source.ts';
import {ReaderError, throwIfAborted} from '../errors.ts';
import {abortScope} from '../abort.ts';

export type EpubAsset = Readonly<{path: string; size: number; mime?: string}>;
export interface EpubAssets {
  readonly entries: readonly EpubAsset[];
  read(path: string, signal: AbortSignal): Promise<Uint8Array>;
  dispose(): void | Promise<void>;
}
const MAX_ENTRIES = 4096, MAX_ENTRY = 16 * 1024 * 1024, MAX_TOTAL = 256 * 1024 * 1024;
export function safeArchivePath(path: string): string {
  if (!path || path.length > 1024 || /[\\\x00-\x1f%?#:]/.test(path) || path.startsWith('/')
    || path.split('/').some(part => part === '.' || part === '..' || !part)) throw new ReaderError('invalid_book');
  return path;
}
class RangeReader extends Reader<ReaderSource> {
  private source: ReaderSource; private signal: AbortSignal;
  constructor(source: ReaderSource, signal: AbortSignal) {super(source); this.source = source; this.signal = signal; this.size = source.size;}
  setSignal(signal: AbortSignal) {this.signal=signal;}
  readUint8Array(start: number, length: number) {
    return readRange(this.source, {start, endExclusive: Math.min(this.size, start + length)}, this.signal);
  }
}
/** Reject huge directories before the ZIP library allocates/decodes them.
 * EPUB needs neither ZIP64 nor split volumes. These fail closed. */
async function checkDirectory(source: ReaderSource, signal: AbortSignal) {
  if (source.size > 512 * 1024 * 1024 || source.size < 22) throw new ReaderError('limit_exceeded');
  const start = Math.max(0, source.size - 65557), bytes = await readRange(source, {start, endExclusive: source.size}, signal);
  const d = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  for (let i = bytes.length - 22; i >= 0; i--) {
    if (d.getUint32(i, true) !== 0x06054b50 || i + 22 + d.getUint16(i + 20, true) !== bytes.length) continue;
    const count = d.getUint16(i + 10, true), size = d.getUint32(i + 12, true), offset = d.getUint32(i + 16, true);
    if (d.getUint16(i+4,true) || d.getUint16(i+6,true) || d.getUint16(i+8,true) !== count
      || count === 65535 || count > MAX_ENTRIES || size > 4*1024*1024 || offset + size > start+i)
      throw new ReaderError('limit_exceeded');
    return;
  }
  throw new ReaderError('invalid_book');
}
export async function openEpubArchive(source: ReaderSource, parent: AbortSignal): Promise<EpubAssets> {
  const abort = new AbortController(), scope=abortScope([parent, abort.signal]), signal=scope.signal;
  try{await checkDirectory(source, signal);}catch(error){scope.dispose();throw error;}
  const reader=new RangeReader(source,signal);
  const zip = new ZipReader(reader, {useWebWorkers: false, checkCrc32: true, strictness: 'strict'});
  const entries = new Map<string, FileEntry>(); let total = 0;
  try {
    for await (const entry of zip.getEntriesGenerator()) {
      throwIfAborted(signal);
      if (entry.directory) continue;
      const name = safeArchivePath(entry.filename);
      const attributes = entry.externalFileAttributes >>> 16;
      if (entries.has(name) || (attributes & 0xf000) === 0xa000) throw new ReaderError('invalid_book');
      if (entry.encrypted) throw new ReaderError('protected');
      if (entry.uncompressedSize > MAX_ENTRY || entry.compressedSize > MAX_ENTRY || entry.uncompressedSize > Math.max(1,entry.compressedSize)*200
        || (total += entry.uncompressedSize) > MAX_TOTAL || entries.size >= MAX_ENTRIES) throw new ReaderError('limit_exceeded');
      entries.set(name, entry as FileEntry);
    }
    if (!entries.has('mimetype') || !entries.has('META-INF/container.xml')) throw new ReaderError('invalid_book');
    let tail: Promise<unknown> = Promise.resolve();
    const assets: EpubAssets = {
      entries: Object.freeze([...entries].map(([path,e]) => Object.freeze({path, size: e.uncompressedSize}))),
      async read(path, callerSignal) {
        throwIfAborted(signal); throwIfAborted(callerSignal);
        const entry = entries.get(safeArchivePath(path)); if (!entry) return new Uint8Array();
        let cleanup = () => {};
        const cancelled = new Promise<never>((_, reject) => {
          const stop = () => {cleanup(); reject(new ReaderError('aborted'));};
          cleanup = () => callerSignal.removeEventListener('abort', stop);
          if (callerSignal.aborted) return stop();
          callerSignal.addEventListener('abort', stop, {once: true});
        });
        const reading = tail.then(async () => {
          const task=abortScope([signal, callerSignal]),readSignal=task.signal;
          try {throwIfAborted(readSignal);
          const chunks: Uint8Array[] = []; let size = 0;
          const sink = new WritableStream<Uint8Array>({write(chunk) {
            throwIfAborted(readSignal); size += chunk.length;
            if (size > MAX_ENTRY || size > entry.uncompressedSize) throw new ReaderError('limit_exceeded');
            chunks.push(chunk.slice());
          }});
          reader.setSignal(readSignal);
          try {await entry.getData(sink, {signal: readSignal, checkCrc32: true, useWebWorkers: false});}
          finally {reader.setSignal(signal);}
          throwIfAborted(readSignal);
          if (size !== entry.uncompressedSize) throw new ReaderError('invalid_book');
          const bytes = new Uint8Array(size); let at=0; for(const chunk of chunks) {bytes.set(chunk,at); at+=chunk.length;}
          return bytes;
          }finally{task.dispose();}
        });
        tail = reading.catch(() => {});
        reading.then(cleanup, cleanup);
        return Promise.race([reading, cancelled]);
      },
      async dispose() {abort.abort();scope.dispose(); await tail.catch(() => {}); entries.clear(); await zip.close();}
    };
    if (new TextDecoder().decode(await assets.read('mimetype', signal)) !== 'application/epub+zip') throw new ReaderError('invalid_book');
    return assets;
  } catch (error) {abort.abort();scope.dispose(); await zip.close(); throw error;}
}
