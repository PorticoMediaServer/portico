import {ReaderError, throwIfAborted} from '@portico/reader-core';
import type {ReaderHostController, ReaderMountAuthority} from './mount';

export type ReadPagePreview = Readonly<{url: string; width: number; height: number; dispose: () => void}>;
export type ReadPagePreviewOptions<P extends object> = Readonly<{
  controller: ReaderHostController<P>; index: number; width: number;
  authority: ReaderMountAuthority; signal: AbortSignal;
}>;

/** One visible preview from the current canonical document. Callers mount a
 * bounded visible window; no archive, second document or preview cache lives here. */
export async function loadReadPagePreview<P extends object>({controller, index, width, authority, signal}: ReadPagePreviewOptions<P>): Promise<ReadPagePreview> {
  const initial = controller.getSnapshot(), life = new AbortController();
  const active = life.signal;
  let url: string | undefined, disposed = false;
  let offController = () => {}, offAuthority = () => {};
  const current = () => {
    const snapshot = controller.getSnapshot();
    return !disposed && authority.isCurrent() && snapshot.status === 'ready'
      && snapshot.sourceId === initial.sourceId && snapshot.sourceRevision === initial.sourceRevision;
  };
  const dispose = () => {
    if (disposed) return;
    disposed = true; life.abort(signal.reason); offController(); offAuthority(); signal.removeEventListener('abort', dispose);
    if (url !== undefined) { URL.revokeObjectURL(url); url = undefined; }
  };
  const retire = () => { if (!current()) dispose(); };
  signal.addEventListener('abort', dispose, {once: true});
  if (signal.aborted) dispose();
  offController = controller.subscribe(retire);
  offAuthority = authority.subscribe(retire);
  if (disposed) { offController(); offAuthority(); }
  try {
    if (!current()) throw new ReaderError('closed');
    throwIfAborted(active);
    const image = await controller.page(index, {width, signal: active});
    throwIfAborted(active); if (!current()) throw new ReaderError('aborted');
    url = URL.createObjectURL(new Blob([image.bytes.slice() as Uint8Array<ArrayBuffer>], {type: image.mime}));
    return Object.freeze({url, width: image.width, height: image.height, dispose});
  } catch (error) { dispose(); throw error; }
}
