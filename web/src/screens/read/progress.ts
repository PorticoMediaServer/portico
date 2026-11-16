import {ReadingError, type ReadingProgress, type ReadingSession} from '@portico/client-core';
import {readingLocationFromReader, type ReadingController} from '@portico/reader-core';
import type {ReaderMountAuthority} from './mount';

/** Only observed engine locations enter the canonical CAS writer. A conflict
 * never overwrites a newer server position or manufactures saved progress. */
export function bindReadingProgress({controller, session, initial, authority, reportError, held = false}: Readonly<{
  controller: ReadingController; session: Pick<ReadingSession, 'manifest' | 'signal' | 'saveProgress'>; initial: ReadingProgress;
  authority: ReaderMountAuthority; reportError: (error: unknown) => void;
  /** While the book is taken near a place from another file, nothing is saved: that place stays
   * until the person moves on from where they land (landed() marks the landing). */
  held?: boolean;
}>) {
  let holding = held;
  let expectedRevision = initial.progress?.revision ?? 0;
  let accepting = true, blocked = false, timer: ReturnType<typeof setTimeout> | undefined;
  let pending: ReturnType<typeof readingLocationFromReader> | undefined;
  let observed = initial.progress && !initial.progress.stale ? JSON.stringify(initial.progress.location) : undefined;
  let writing: Promise<void> | undefined, stopping: Promise<void> | undefined;
  const life = new AbortController();
  const current = () => !life.signal.aborted && !session.signal.aborted && authority.isCurrent();
  const flush = (): Promise<void> => {
    if (writing) return writing;
    if (timer !== undefined) {clearTimeout(timer); timer = undefined;}
    const request = new AbortController(), signal = AbortSignal.any([life.signal, request.signal]);
    // A departure must also finish when the server stops answering. The reader
    // remains usable if another blocker cancels navigation or Settings covers it.
    const deadline = setTimeout(() => request.abort(), 2000);
    writing = (async () => {
      while (pending && !blocked && current() && !signal.aborted) {
        const location = pending; pending = undefined;
        try {
          const result = await session.saveProgress({expectedRevision, location}, signal);
          if (result.progress !== null) expectedRevision = result.progress.revision;
          // saved:false/null is an actual unsaved observation, never revision+1.
        } catch (error) {
          if (error instanceof ReadingError && error.status === 412) blocked = true;
          if (current()) reportError(error);
          break;
        }
      }
    })().finally(() => {clearTimeout(deadline);writing = undefined;});
    return writing;
  };
  const update = () => {
    const snapshot = controller.getSnapshot();
    if (!accepting || blocked || !current() || snapshot.status !== 'ready' || !snapshot.location
      || snapshot.sourceId !== session.manifest.sourceId || snapshot.sourceRevision !== session.manifest.revision) return;
    const location = readingLocationFromReader(snapshot.location), key = JSON.stringify(location);
    if (key === observed) return;
    observed = key;
    if (holding) return;
    pending = location;
    if (timer === undefined) timer = setTimeout(() => {timer = undefined; void flush();}, 1000);
  };
  const off = controller.subscribe(update);
  const stop = (save = true) => {
    if (stopping) return stopping;
    accepting = false; off();
    if (timer !== undefined) {clearTimeout(timer); timer = undefined;}
    stopping = (save ? flush() : Promise.resolve()).finally(() => {life.abort(); pending = undefined;});
    return stopping;
  };
  const landed = () => {
    if (!holding) return;
    holding = false;
    const snapshot = controller.getSnapshot();
    if (snapshot.status === 'ready' && snapshot.location) observed = JSON.stringify(readingLocationFromReader(snapshot.location));
  };
  update();
  return Object.freeze({flush, stop, landed});
}
