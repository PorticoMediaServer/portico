import {NativeModules, Platform} from 'react-native';

/** The image types a Reading page can actually produce. */
export type CoverImageMime = 'image/jpeg' | 'image/png' | 'image/webp';
export type CoverImageLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
/** An opaque, exact-owned private cache file. Only its own dispose deletes it. */
export type CoverImageTemp = Readonly<{
  handle: string;
  /** A private app-cache file URI; callers may pass it only while their lease is current. */
  uri: string;
  size: number;
  dispose: () => Promise<void>;
}>;

type CoverBridge = {
  beginCoverImage?: (mime: string) => Promise<unknown>;
  appendCoverImage?: (handle: string, base64: string) => Promise<unknown>;
  finishCoverImage?: (handle: string, size: number) => Promise<unknown>;
  deleteCoverImage?: (handle: string) => Promise<unknown>;
};
const bridge = (): CoverBridge | undefined => Platform.isTV ? undefined : NativeModules.PorticoDownloads as CoverBridge | undefined;
const MAX_BYTES = 10 * 1024 * 1024;
const failure = (code: string) => Object.assign(new Error(code), {code});

export const coverImageSupported = (): boolean => {
  const native = bridge();
  return !!native && typeof native.beginCoverImage === 'function' && typeof native.appendCoverImage === 'function'
    && typeof native.finishCoverImage === 'function' && typeof native.deleteCoverImage === 'function';
};

/** The actual raster signature, never a caller-declared MIME. */
export function coverImageMime(bytes: Uint8Array): CoverImageMime | undefined {
  const at = (index: number, value: number) => index < bytes.length && bytes[index] === value;
  if (at(0, 0xff) && at(1, 0xd8) && at(2, 0xff)) return 'image/jpeg';
  if (at(0, 0x89) && at(1, 0x50) && at(2, 0x4e) && at(3, 0x47) && at(4, 0x0d) && at(5, 0x0a) && at(6, 0x1a) && at(7, 0x0a)) return 'image/png';
  if (at(0, 0x52) && at(1, 0x49) && at(2, 0x46) && at(3, 0x46) && at(8, 0x57) && at(9, 0x45) && at(10, 0x42) && at(11, 0x50)) return 'image/webp';
  return undefined;
}

// Full chunks end on a three-byte boundary, so only the last one needs padding.
const CHUNK_BYTES = 24 * 1024;
function base64(bytes: Uint8Array): string {
  let binary = '';
  // Keep the call argument count small on Hermes and avoid a string append per byte.
  for (let at = 0; at < bytes.length; at += 1024) binary += String.fromCharCode(...bytes.subarray(at, Math.min(at + 1024, bytes.length)));
  return globalThis.btoa(binary);
}

/** Stage one admitted page image as a bounded private temp file. The caller
 * keeps the CBZ session/page witness and lease: it must be current before and
 * after this call, and again before handing `uri` to the upload. */
export async function stageCoverImage(bytes: Uint8Array, mime: CoverImageMime, lifetime: CoverImageLifetime): Promise<CoverImageTemp | undefined> {
  const native = bridge();
  if (!native?.beginCoverImage || !native.appendCoverImage || !native.finishCoverImage || !native.deleteCoverImage) throw failure('cover_image_unavailable');
  const current = () => !lifetime.signal.aborted && lifetime.isCurrent();
  if (!current()) return undefined;
  if (coverImageMime(bytes) !== mime) throw failure('unsupported_image');
  if (bytes.length === 0 || bytes.length > MAX_BYTES) throw failure('invalid_image');
  // The caller's Uint8Array is mutable. Keep the exact admitted page bytes across
  // event-loop yields, including if the caller later replaces or detaches its buffer.
  const admitted = bytes.slice(), size = admitted.length;
  // Native staging takes bounded chunks (IM01): the whole image never exists here as one string.
  const begun = await native.beginCoverImage(mime) as {handle?: unknown} | undefined;
  const handle = typeof begun?.handle === 'string' ? begun.handle : undefined;
  if (!handle) throw failure('invalid_image');
  const discard = async () => {try {await native.deleteCoverImage!(handle);} catch { /* the module keeps bookkeeping for teardown */ }};
  let value: {handle?: unknown; uri?: unknown; size?: unknown} | undefined;
  try {
    for (let offset = 0; offset < size; offset += CHUNK_BYTES) {
      if (!current()) {await discard(); return undefined;}
      const end = Math.min(offset + CHUNK_BYTES, size);
      if (await native.appendCoverImage(handle, base64(admitted.subarray(offset, end))) !== end) throw failure('invalid_image');
      // Queued UI work runs between chunks.
      if (end < size) await new Promise<void>(resolve => setTimeout(resolve, 0));
    }
    if (!current()) {await discard(); return undefined;}
    value = await native.finishCoverImage(handle, size) as typeof value;
  } catch (error) {
    await discard();
    throw error;
  }
  if (value?.handle !== handle || typeof value.uri !== 'string' || !/^file:\/\//.test(value.uri) || value.size !== size) {
    await discard();
    throw failure('invalid_image');
  }
  const uri = value.uri;
  let disposed = false, onAbort: (() => void) | undefined;
  const dispose = async () => {
    if (disposed) return;
    disposed = true;
    if (onAbort) {lifetime.signal.removeEventListener('abort', onAbort); onAbort = undefined;}
    try {await native.deleteCoverImage!(handle);} catch { /* the module keeps bookkeeping for teardown */ }
  };
  onAbort = () => {void dispose();};
  lifetime.signal.addEventListener('abort', onAbort, {once: true});
  if (lifetime.signal.aborted || !lifetime.isCurrent()) {await dispose(); return undefined;}
  return Object.freeze({handle, uri, size, dispose});
}
