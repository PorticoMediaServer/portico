import {ReadingError, type ReadingProgress, type ReadingSession} from '@portico/client-core';
import {readingLocationFromReader, type ReadingController} from '@portico/reader-core';
import type {ReaderMountAuthority} from './mount';

/** Only observed engine locations enter the canonical CAS writer. A conflict
 * never overwrites a newer server position or manufactures saved progress. */
export function bindReadingProgress({controller, session, initial, authority, reportError}: Readonly<{
  controller: ReadingController; session: Pick<ReadingSession, 'manifest' | 'signal' | 'saveProgress'>; initial: ReadingProgress;
  authority: ReaderMountAuthority; reportError: (error: unknown) => void;
}>) {
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
    writing = (async () => {
      while (pending && !blocked && current()) {
        const location = pending; pending = undefined;
        try {
          const result = await session.saveProgress({expectedRevision, location}, life.signal);
          if (result.progress !== null) expectedRevision = result.progress.revision;
          // saved:false/null is an actual unsaved observation, never revision+1.
        } catch (error) {
          if (error instanceof ReadingError && error.status === 412) blocked = true;
          if (current()) reportError(error);
          break;
        }
      }
    })().finally(() => {writing = undefined;});
    return writing;
  };
  const update = () => {
    const snapshot = controller.getSnapshot();
    if (!accepting || blocked || !current() || snapshot.status !== 'ready' || !snapshot.location
      || snapshot.sourceId !== session.manifest.sourceId || snapshot.sourceRevision !== session.manifest.revision) return;
    const location = readingLocationFromReader(snapshot.location), key = JSON.stringify(location);
    if (key === observed) return;
    observed = key; pending = location;
    if (timer === undefined) timer = setTimeout(() => {timer = undefined; void flush();}, 1000);
  };
  const off = controller.subscribe(update);
  const stop = (save = true) => {
    if (stopping) return stopping;
    accepting = false; off();
    if (timer !== undefined) {clearTimeout(timer); timer = undefined;}
    // Back/teardown is bounded even when a response body stalls.
    const deadline = setTimeout(() => life.abort(), 2000);
    stopping = (save ? flush() : Promise.resolve()).finally(() => {clearTimeout(deadline); life.abort(); pending = undefined;});
    return stopping;
  };
  update();
  return Object.freeze({flush, stop});
}
