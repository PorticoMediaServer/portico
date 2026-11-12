import assert from 'node:assert/strict';
import {test} from 'node:test';
import {bindReadingProgress} from '../../src/screens/read/progress.ts';

// 10-reading rule 14 with desk B's ruling: while a book is taken near a place saved in another
// file, nothing is saved, so that place stays until the person moves on from where they landed.
test('a book taken near a place from another file saves nothing until the person moves on from the landing', async () => {
  const listeners = new Set<() => void>(), saved: unknown[] = [];
  let snapshot = {status: 'ready', sourceId: 'src', sourceRevision: 'rev', location: {format: 'pdf', page: 0, progression: 0}};
  const controller = {subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}, getSnapshot: () => snapshot};
  const move = (page: number) => {snapshot = {...snapshot, location: {format: 'pdf', page, progression: page / 10}}; for (const listener of listeners) listener();};
  const session = {manifest: {sourceId: 'src', revision: 'rev'}, signal: new AbortController().signal,
    saveProgress: async (request: {location: unknown}) => {saved.push(request.location); return {saved: true, progress: {revision: saved.length}};}};
  const progress = bindReadingProgress({controller: controller as never, session: session as never, authority: {isCurrent: () => true} as never,
    initial: {progress: {stale: true, revision: 1, location: {format: 'pdf', pageIndex: 6, progression: 0.6}}} as never, reportError: () => {}, held: true});
  move(6); await progress.flush();
  assert.deepEqual(saved, [], 'neither the start nor the landing is saved');
  progress.landed(); await progress.flush();
  assert.deepEqual(saved, []);
  move(7); await progress.flush();
  assert.deepEqual(saved, [{format: 'pdf', pageIndex: 7, progression: 0.7}]);
  await progress.stop();
});
