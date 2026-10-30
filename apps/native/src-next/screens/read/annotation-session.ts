import {ReadingError, type ReadingHostSession} from '../../../../../packages/client-core/src';
import type {ReadingAnnotationContent, ReadingAnnotationMutation, ReadingAnnotationPort} from '../../../../../packages/client-core/src/reading-annotations';
import type {NativeAnnotationJournal, NativeAnnotationIdentity, NativeAnnotationSource} from './annotation-journal';
import {nativeAnnotationLeaseOf} from './annotation-journal';
import {NATIVE_OFFLINE_ANNOTATION_AUTHORING} from './annotation-authoring';
import {nativeOfflineAnnotationSession} from './annotation-offline-port';

/** The existing annotation bridge with the offline path this family owns.
 *
 * A real server session keeps delivering through `mutateAnnotation`; a transport failure
 * queues the person's original edit in the private journal instead of losing it, and the
 * guest still receives the real failure (nothing is reported as saved while it is only
 * queued). A downloaded publication's lease session, which has no network annotation port,
 * gets the offline authoring port for that exact source. No session, store or engine is
 * duplicated: the same bridge and guest UI serve both. */

/** Failures worth keeping: transport, offline and server-unavailable. A refusal the server
 * itself explained (invalid, forbidden, conflict, changed source) is never queued. */
export function nativeAnnotationQueueable(error: unknown): boolean {
  if (!(error instanceof ReadingError)) return true; // fetch/offline rejection
  if (error.code === 'cancelled') return false;
  return error.status === undefined || error.status >= 500;
}

const content = (mutation: Extract<ReadingAnnotationMutation, {operation: 'upsert'}>): ReadingAnnotationContent => {
  const {expectedRevision: _revision, operation: _operation, ...rest} = mutation;
  return rest;
};

type ServerAnnotationSession = ReadingHostSession & Readonly<{
  annotations: ReadingAnnotationPort['annotations'];
  mutateAnnotation: ReadingAnnotationPort['mutateAnnotation'];
}>;

type BridgeOptions = Readonly<{
  session: ReadingHostSession;
  journal: NativeAnnotationJournal;
  identity: NativeAnnotationIdentity;
  source: NativeAnnotationSource;
  /** The captured authority/session fence; the journal write is refused once it ends. */
  fresh: () => boolean;
  now?: () => number;
  onQueued?: () => void;
}>;

/** Choose the port this exact reader session can honestly offer. */
export function nativeAnnotationBridge(options: BridgeOptions): ReadingHostSession {
  const {session} = options;
  // Containment: the reader keeps the canonical server annotation port only until the
  // functional journal/merge corrections are reviewed. No offline synthesis, no fallback.
  if (!NATIVE_OFFLINE_ANNOTATION_AUTHORING) return session;
  if (session.manifest.canAnnotate === true && typeof session.annotations === 'function' && typeof session.mutateAnnotation === 'function')
    return nativeOnlineAnnotationSession({...options, session: session as ServerAnnotationSession});
  if (nativeAnnotationLeaseOf(session)) return nativeOfflineAnnotationSession(options);
  return session; // No annotation port here: the host withholds the capability instead of faking one.
}

function nativeOnlineAnnotationSession(options: BridgeOptions & Readonly<{session: ServerAnnotationSession}>): ReadingHostSession {
  const {session, journal, identity, source, fresh, now = Date.now, onQueued} = options;
  const sessionSignal = session.signal;
  const assertCurrent = (signal?: AbortSignal): void => {
    if (sessionSignal.aborted || signal?.aborted || !fresh()) throw new ReadingError('cancelled');
  };
  // The bridge projection is always the real session; only the annotation methods differ.
  const base: ReadingHostSession = {manifest: session.manifest, signal: session.signal, headPDF: session.headPDF, readRange: session.readRange,
    readAsset: session.readAsset, page: session.page, progress: session.progress, saveProgress: session.saveProgress,
    ...(session.openSequential ? {openSequential: session.openSequential} : {})};
  /** The server's own updatedAt for each id at the revision the guest last read. */
  let observed: Readonly<{revision: number; updatedAt: ReadonlyMap<string, number>}> | undefined;
  const annotations: ReadingAnnotationPort['annotations'] = async (cursor = '', revision?: number, signal?: AbortSignal) => {
    assertCurrent(signal);
    const page = await session.annotations(cursor, revision, signal);
    assertCurrent(signal);
    if (!cursor) observed = {revision: page.revision, updatedAt: new Map(page.entries.map(entry => [entry.id, entry.updatedAt]))};
    else if (observed && observed.revision === page.revision) {
      const updatedAt = new Map(observed.updatedAt);
      for (const entry of page.entries) updatedAt.set(entry.id, entry.updatedAt);
      observed = {revision: page.revision, updatedAt};
    }
    return page;
  };
  const mutateAnnotation: ReadingAnnotationPort['mutateAnnotation'] = async (mutation, signal) => {
    assertCurrent(signal);
    try {
      const saved = await session.mutateAnnotation(mutation, signal);
      assertCurrent(signal);
      // The server now holds a newer entry than the guest's cached page; a later queued
      // edit must not claim that stale base, so the next write waits for a fresh read.
      observed = undefined;
      return saved;
    } catch (error) {
      assertCurrent(signal);
      const base = observed;
      if (!nativeAnnotationQueueable(error) || !base || base.revision !== mutation.expectedRevision) throw error;
      await journal.enqueue(identity, source, {
        id: mutation.id,
        delete: mutation.operation === 'delete',
        ...(mutation.operation === 'delete' ? {} : {content: content(mutation)}),
        observedAt: now(),
        baseRevision: mutation.expectedRevision,
        baseUpdatedAt: base.updatedAt.get(mutation.id) ?? 0,
      }, () => assertCurrent(signal));
      assertCurrent(signal);
      onQueued?.();
      throw error;
    }
  };
  return {...base, annotations, mutateAnnotation};
}
