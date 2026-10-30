import {ReadingError, type ReadingHostSession} from '../../../../../packages/client-core/src';
import {captureReadingAnnotationMutation, type ReadingAnnotationContent, type ReadingAnnotationMutation, type ReadingAnnotationPort} from '../../../../../packages/client-core/src/reading-annotations';
import {validateReadingLocation} from '../../../../../packages/client-core/src/reading';
import type {NativeAnnotationJournal, NativeAnnotationIdentity, NativeAnnotationSource} from './annotation-journal';

/** Offline annotation authoring for one downloaded publication (P1-READ-OFFLINE-ANNOTATION-SYNC).
 *
 * The OfflineCore publication lease is the only source while the device is offline, and its
 * reader session has no network annotation port. This adapter gives the existing annotation
 * bridge exactly that port for that exact source: the last server page this device saw plus
 * every queued local edit, and mutations that freeze the person's original content into the
 * private journal bound to that lease. The reader never gets a fabricated server revision or
 * a claim that the edit reached the server; the queued edit is merged later by the real
 * session and the reader's own state stays honest.
 *
 * The offline manifest advertises annotations because this port really accepts them. A source
 * replacement is not re-anchored: the lease's own source/revision binding decides that, and
 * the merge refuses anything that no longer matches the captured revision. */

const content = (mutation: Extract<ReadingAnnotationMutation, {operation: 'upsert'}>): ReadingAnnotationContent => {
  const {expectedRevision: _revision, operation: _operation, ...rest} = mutation;
  return rest;
};

export function nativeOfflineAnnotationSession(options: Readonly<{
  session: ReadingHostSession;
  journal: NativeAnnotationJournal;
  identity: NativeAnnotationIdentity;
  source: NativeAnnotationSource;
  /** The captured authority/session fence; every write is refused once it ends. */
  fresh: () => boolean;
  now?: () => number;
  onQueued?: () => void;
}>): ReadingHostSession {
  const {session, journal, identity, source, fresh, now = Date.now, onQueued} = options;
  const sessionSignal = session.signal;
  const assertCurrent = (signal?: AbortSignal): void => {
    if (sessionSignal.aborted || signal?.aborted || !fresh()) throw new ReadingError('cancelled');
  };
  const manifest = Object.freeze({...session.manifest, canAnnotate: true as const});
  const validate = (location: Parameters<typeof validateReadingLocation>[0]) => validateReadingLocation(location, manifest);
  const annotations: ReadingAnnotationPort['annotations'] = async (cursor = '', revision?: number, signal?: AbortSignal) => {
    assertCurrent(signal);
    const view = await journal.view(identity, source);
    assertCurrent(signal);
    if (revision !== undefined && revision !== view.revision) throw new ReadingError('conflict');
    // The bridge's own page shape: at most 50 ids above the cursor, with the cursor naming the
    // last id. The local view is bounded, so this is the honest whole set in pages.
    const remaining = view.entries.filter(entry => entry.id > cursor).slice(0, 50);
    const more = remaining.length === 50 && view.entries.some(entry => entry.id > remaining[remaining.length - 1]!.id);
    return Object.freeze({sourceId: source.sourceId, sourceRevision: source.sourceRevision, revision: view.revision, entries: Object.freeze(remaining),
      nextCursor: more ? remaining[remaining.length - 1]!.id : ''});
  };
  const mutateAnnotation: ReadingAnnotationPort['mutateAnnotation'] = async (request, signal) => {
    assertCurrent(signal);
    const captured = captureReadingAnnotationMutation(request, validate);
    const observed = await journal.snapshot(identity);
    assertCurrent(signal);
    const group = observed?.groups.find(value => value.source.itemId === source.itemId && value.source.sourceId === source.sourceId
      && value.source.sourceRevision === source.sourceRevision);
    const view = await journal.view(identity, source);
    assertCurrent(signal);
    if (captured.expectedRevision !== view.revision) throw new ReadingError('conflict');
    const revision = await journal.localMutate(identity, source, {
      id: captured.id,
      delete: captured.operation === 'delete',
      ...(captured.operation === 'delete' ? {} : {content: content(captured)}),
      observedAt: now(),
      // The server base this edit is really based on: the last page this device saw, or 0
      // when the annotation never came from the server (it only exists locally).
      baseRevision: group?.cache?.revision ?? 0,
      baseUpdatedAt: group?.cache?.entries.find(entry => entry.id === captured.id)?.updatedAt ?? 0,
    }, captured.expectedRevision, () => assertCurrent(signal));
    assertCurrent(signal);
    onQueued?.();
    assertCurrent(signal);
    return Object.freeze({revision});
  };
  return {manifest, signal: session.signal, headPDF: session.headPDF, readRange: session.readRange, readAsset: session.readAsset,
    page: session.page, progress: session.progress, saveProgress: session.saveProgress,
    ...(session.openSequential ? {openSequential: session.openSequential} : {}), annotations, mutateAnnotation};
}
