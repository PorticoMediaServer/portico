import type {NativeOfflinePublicationLease} from './offline-reading-authority';
import {NativeModules, Platform} from 'react-native';
import {decodeGetReadingSessionsSessionIdProgressResponse} from '../../../packages/contracts/src';
import type {ReadingManifest, ReadingLocation, ReadingProgress, ReadingSavedProgress} from '../../../packages/client-core/src/reading';
import type {ReadingHostSession} from '../../../packages/client-core/src/reading-host';
import {ReadingError, parseReadingManifest} from '../../../packages/client-core/src/reading';
import {nativeRouteCrypto} from '../src-next/platform/server-connections';

export type PublicationBinding = Readonly<{sourceId: string; revision: string; format: 'epub' | 'pdf' | 'cbz'; documentSha256: string}>;
export type PublicationResource = Readonly<{path: string; mime: string; size: number; sha256: string; offset: number; compressedSize: number; compression: 0 | 8}>;
export type OfflinePublication = Readonly<{manifest: ReadingManifest; resources: readonly PublicationResource[]; initialProgress: ReadingProgress}>;
export type OfflineReadingSession = ReadingHostSession & {offline?: NativeOfflinePublicationLease; close(signal?: AbortSignal): Promise<void>};
type NativeReads = {
  publicationOpen(handle: string, id: string, owner: string, digest: string): Promise<number>;
  publicationRead(handle: string, operation: number, offset: number, length: number): Promise<string>;
  publicationMember(handle: string, operation: number, offset: number, compressed: number, size: number, method: number, digest: string): Promise<number>;
  publicationMemberRead(handle: string, operation: number, offset: number, length: number): Promise<string>;
  publicationCancel(handle: string, operation: number): void;
  publicationClose(handle: string): void;
};
const native = NativeModules.PorticoDownloads as NativeReads | undefined;
export const nativePublicationReadsAvailable = () => !Platform.isTV && !!native && ['publicationOpen', 'publicationRead', 'publicationMember', 'publicationMemberRead', 'publicationCancel', 'publicationClose'].every(key => typeof Reflect.get(native, key) === 'function');
function fail(code = 'reading_unavailable'): never {throw new ReadingError(code);}
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const integer = (v: unknown, min: number, max: number): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max;
const digest = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{64}$/.test(v);
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
export function publicationUTF8(value: string): Uint8Array {try {const encoded = unescape(encodeURIComponent(value)); return Uint8Array.from(encoded, character => character.charCodeAt(0));} catch {return fail();}}
const text = (v: unknown, max: number): v is string => typeof v === 'string' && publicationUTF8(v).length <= max;
const path = (v: unknown): v is string => text(v, 1024) && v.length > 0 && !/[\\\x00-\x1f\x7f%?#:]/.test(v) && !v.startsWith('/') && v.split('/').every(part => part !== '' && part !== '.' && part !== '..');
function keys(v: unknown, required: readonly string[], optional: readonly string[] = [], nullable: readonly string[] = []): asserts v is Record<string, unknown> {
  if (!object(v) || required.some(key => !Object.prototype.hasOwnProperty.call(v, key)) || Object.keys(v).some(key => !required.includes(key) && !optional.includes(key)) || Object.entries(v).some(([key, value]) => value === null && !nullable.includes(key))) fail();
}
export function publicationBinding(raw: unknown): PublicationBinding {
  keys(raw, ['sourceId', 'revision', 'format', 'documentSha256']);
  if (!id(raw.sourceId) || !digest(raw.revision) || !digest(raw.documentSha256)) fail();
  const format = raw.format; if (format !== 'epub' && format !== 'pdf' && format !== 'cbz') fail();
  return Object.freeze({sourceId: raw.sourceId, revision: raw.revision, documentSha256: raw.documentSha256, format});
}
export function validateOfflineLocation(raw: unknown, manifest: ReadingManifest, stale = false): ReadingLocation {
  if (!object(raw) || raw.format !== manifest.format || typeof raw.progression !== 'number' || !Number.isFinite(raw.progression) || raw.progression < 0 || raw.progression > 1) fail('invalid_location');
  if (raw.format === 'epub') {
    keys(raw, ['format', 'cfi', 'progression'], ['section']);
    if (!text(raw.cfi, 4096) || !raw.cfi.startsWith('epubcfi(/') || !raw.cfi.endsWith(')') || /[\p{Cc}<>]/u.test(raw.cfi) || raw.section !== undefined && (!path(raw.section) || !stale && !manifest.spine?.includes(raw.section))) fail('invalid_location');
    return Object.freeze({format: 'epub', cfi: raw.cfi, progression: raw.progression, ...(typeof raw.section === 'string' ? {section: raw.section} : {})});
  }
  keys(raw, ['format', 'pageIndex', 'progression']);
  if ((raw.format !== 'pdf' && raw.format !== 'cbz') || !integer(raw.pageIndex, 0, 1000000) || raw.format === 'cbz' && !stale && raw.pageIndex >= (manifest.pageNames?.length ?? 0)) fail('invalid_location');
  return Object.freeze({format: raw.format, pageIndex: raw.pageIndex, progression: raw.progression});
}
function validProgress(raw: unknown, manifest: ReadingManifest): ReadingProgress {
  const value = decodeGetReadingSessionsSessionIdProgressResponse(raw), p = value.progress;
  if (p !== null) {if (p.sourceId !== manifest.sourceId || !digest(p.sourceRevision) || p.stale !== (p.sourceRevision !== manifest.revision) || !integer(p.revision, 1, Number.MAX_SAFE_INTEGER) || !integer(p.observedAt, 1, Number.MAX_SAFE_INTEGER)) fail(); validateOfflineLocation(p.location, manifest, p.stale);}
  return value;
}
export async function parseOfflinePublication(raw: string, binding: PublicationBinding, itemId: string, bytes: number, handle: string): Promise<OfflinePublication> {
  const encoded = publicationUTF8(raw); if (!encoded.length || encoded.length > 2 << 20 || Array.from(await nativeRouteCrypto.sha256(encoded), byte => byte.toString(16).padStart(2, '0')).join('') !== binding.documentSha256) fail();
  const doc: unknown = JSON.parse(raw);
  keys(doc, ['itemId', 'sourceId', 'revision', 'format', 'size', 'metadata', 'resources', 'spine', 'pageNames', 'contents', 'progress'], [], ['progress']);
  if (doc.itemId !== itemId || doc.sourceId !== binding.sourceId || doc.revision !== binding.revision || doc.format !== binding.format || doc.size !== bytes || !integer(bytes, 1, 2 ** 31)) fail();
  keys(doc.metadata, ['title'], ['author', 'language', 'direction']);
  if (!text(doc.metadata.title, 4096) || doc.metadata.author !== undefined && !text(doc.metadata.author, 4096) || doc.metadata.language !== undefined && !text(doc.metadata.language, 128) || doc.metadata.direction !== undefined && doc.metadata.direction !== 'ltr' && doc.metadata.direction !== 'rtl' && doc.metadata.direction !== 'vertical') fail();
  if (!Array.isArray(doc.resources) || doc.resources.length > 4096 || !Array.isArray(doc.spine) || doc.spine.length > 4096 || !Array.isArray(doc.pageNames) || doc.pageNames.length > 4096 || !Array.isArray(doc.contents) || doc.contents.length > 512) fail();
  const names = new Set<string>(); let expanded = 0;
  const resources = doc.resources.map((value: unknown): PublicationResource => {
    keys(value, ['path', 'mime', 'size', 'sha256', 'offset', 'compressedSize', 'compression']);
    if (!path(value.path) || names.has(value.path) || !text(value.mime, 128) || !/^[a-zA-Z0-9][a-zA-Z0-9.+-]*\/[a-zA-Z0-9][a-zA-Z0-9.+-]*$/.test(value.mime) || !digest(value.sha256) || !integer(value.size, 0, 32 << 20) || !integer(value.offset, 0, bytes) || !integer(value.compressedSize, 0, bytes - value.offset) || (value.compression !== 0 && value.compression !== 8) || value.compression === 0 && value.size !== value.compressedSize) fail();
    expanded += value.size; if (expanded > 512 << 20) fail(); names.add(value.path);
    return Object.freeze({path: value.path, mime: value.mime, size: value.size, sha256: value.sha256, offset: value.offset, compressedSize: value.compressedSize, compression: value.compression});
  });
  if (doc.spine.some(value => !path(value) || !names.has(value)) || new Set(doc.spine).size !== doc.spine.length || doc.pageNames.some(value => !path(value) || !names.has(value)) || new Set(doc.pageNames).size !== doc.pageNames.length) fail();
  if (binding.format === 'epub' ? !resources.length || !doc.spine.length || doc.pageNames.length : binding.format === 'cbz' ? !doc.pageNames.length || doc.pageNames.length !== resources.length || doc.spine.length || doc.contents.length : resources.length || doc.spine.length || doc.pageNames.length || doc.contents.length) fail();
  for (const value of doc.contents) {keys(value, ['title', 'section', 'depth'], ['fragment']); if (!text(value.title, 4096) || !value.title.length || !path(value.section) || !names.has(value.section) || !integer(value.depth, 0, 16) || value.fragment !== undefined && !text(value.fragment, 1024)) fail();}
  const manifest = parseReadingManifest({sessionId: handle, itemId, sourceId: binding.sourceId, revision: binding.revision, format: binding.format, size: bytes, metadata: doc.metadata,
    ...(binding.format === 'epub' ? {resources: resources.map(({path, mime, size}) => ({path, mime, size})), spine: doc.spine, ...(doc.contents.length ? {contents: doc.contents} : {})} : {}), ...(binding.format === 'cbz' ? {pageNames: doc.pageNames} : {})});
  const initialProgress = validProgress({progress: doc.progress}, manifest);
  return Object.freeze({manifest, resources: Object.freeze(resources), initialProgress});
}
function decodeChunk(value: unknown, length: number): Uint8Array {
  if (typeof value !== 'string' || value.length !== Math.ceil(length / 3) * 4 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) fail();
  const raw = globalThis.atob(value); if (raw.length !== length) fail(); return Uint8Array.from(raw, char => char.charCodeAt(0));
}
export async function openOfflineReadingSession(options: Readonly<{
  authority: NativeOfflinePublicationLease; id: string; owner: string; artifact: {sha256: string; bytes: number}; raw: string; binding: PublicationBinding; itemId: string;
  current: () => boolean; signal: AbortSignal; progress: (initial: ReadingProgress) => ReadingProgress;
  save: (request: {expectedRevision: number; location: ReadingLocation}, initial: ReadingProgress, startedAt: number, current: () => boolean) => Promise<ReadingSavedProgress>;
  now: () => number; onClose?: () => void;
}>): Promise<OfflineReadingSession> {
  if (!nativePublicationReadsAvailable() || !native) return fail();
  const handle = 'pub_' + Array.from(nativeRouteCrypto.random(20), byte => byte.toString(16).padStart(2, '0')).join('');
  const life = new AbortController(); let closed = false, operation = 1, serial: Promise<unknown> = Promise.resolve();
  const close = async () => {if (closed) return; closed = true; life.abort(); native.publicationClose(handle); options.signal.removeEventListener('abort', retire); options.onClose?.();};
  const retire = () => {void close();}; options.signal.addEventListener('abort', retire, {once: true});
  const check = (signal?: AbortSignal) => {if (closed || life.signal.aborted || options.signal.aborted || signal?.aborted || !options.current()) fail('cancelled');};
  const task = <T,>(signal: AbortSignal | undefined, work: (operation: number) => Promise<T>): Promise<T> => {
    if (operation >= Number.MAX_SAFE_INTEGER) return Promise.reject(new ReadingError('reading_unavailable'));
    const own = ++operation;
    const run = async () => {check(signal); const cancel = () => native.publicationCancel(handle, own); signal?.addEventListener('abort', cancel, {once: true});
      try {check(signal); const result = await work(own); check(signal); return result;} finally {signal?.removeEventListener('abort', cancel);}};
    const result = serial.then(run); serial = result.catch(() => {}); return result;
  };
  try {
    check(); const doc = await parseOfflinePublication(options.raw, options.binding, options.itemId, options.artifact.bytes, handle); check();
    const bytes = await native.publicationOpen(handle, options.id, options.owner, options.artifact.sha256); check(); if (bytes !== options.artifact.bytes) fail();
    let progress = validProgress(options.progress(doc.initialProgress), doc.manifest); const startedAt = options.now();
    const member = async (value: PublicationResource, own: number, signal?: AbortSignal) => {
      check(signal); if (await native.publicationMember(handle, own, value.offset, value.compressedSize, value.size, value.compression, value.sha256) !== value.size) fail(); check(signal);
      const out = new Uint8Array(value.size);
      for (let offset = 0; offset < value.size; offset += 49152) {check(signal); const length = Math.min(49152, value.size - offset); out.set(decodeChunk(await native.publicationMemberRead(handle, own, offset, length), length), offset); check(signal);}
      return out;
    };
    const session: OfflineReadingSession = {manifest: doc.manifest, signal: life.signal, close,
      headPDF: async signal => {check(signal); if (doc.manifest.format !== 'pdf') fail();},
      readRange: (range, signal) => task(signal, async own => {
        if (doc.manifest.format !== 'pdf' || !integer(range.start, 0, bytes - 1) || !integer(range.endExclusive, range.start + 1, bytes) || range.endExclusive - range.start > 65536) fail('invalid_range');
        const out = new Uint8Array(range.endExclusive - range.start);
        for (let offset = 0; offset < out.length; offset += 49152) {check(signal); const length = Math.min(49152, out.length - offset); out.set(decodeChunk(await native.publicationRead(handle, own, range.start + offset, length), length), offset); check(signal);} return out;
      }),
      readAsset: (path, signal) => task(signal, own => {if (doc.manifest.format !== 'epub') return fail(); const value = doc.resources.find(value => value.path === path); if (!value) return fail(); return member(value, own, signal);}),
      page: (index, request) => task(request.signal, async own => {if (doc.manifest.format !== 'cbz' || !integer(index, 0, (doc.manifest.pageNames?.length ?? 0) - 1) || !integer(request.width, 32, 2048)) fail();
        const name = doc.manifest.pageNames![index], value = doc.resources.find(value => value.path === name); if (!value || (value.mime !== 'image/jpeg' && value.mime !== 'image/png' && value.mime !== 'image/webp')) return fail();
        return Object.freeze({index, name, bytes: await member(value, own, request.signal), mime: value.mime});}),
      progress: async signal => {check(signal); return progress;},
      saveProgress: (request, signal) => task(signal, async () => {if (!integer(request.expectedRevision, 0, Number.MAX_SAFE_INTEGER) || request.expectedRevision !== (progress.progress?.revision ?? 0)) fail('reading_conflict');
        const location = validateOfflineLocation(request.location, doc.manifest); const saved = await options.save({expectedRevision: request.expectedRevision, location}, progress, startedAt, () => {try {check(signal); return true;} catch {return false;}}); check(signal); progress = {progress: saved.progress}; return saved;}),
    };
    return Object.freeze(session);
  } catch (error) {await close(); throw error;}
}
