import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ReadingError} from '@portico/client-core';
import {bindReadingProgress} from '../../src/screens/read/progress.ts';

function harness({progress, saveProgress}: {
  progress?: (signal?: AbortSignal) => Promise<unknown>;
  saveProgress: (request: {expectedRevision: number; location: unknown}, signal?: AbortSignal) => Promise<unknown>;
}) {
  const listeners = new Set<() => void>(), errors: unknown[] = [];
  let snapshot = {status: 'ready', sourceId: 'src', sourceRevision: 'rev', location: {format: 'pdf', page: 0, progression: 0}};
  const controller = {subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}, getSnapshot: () => snapshot};
  const move = (page: number) => {snapshot = {...snapshot, location: {format: 'pdf', page, progression: page / 10}}; for (const listener of listeners) listener();};
  const session = {manifest: {sourceId: 'src', revision: 'rev'}, signal: new AbortController().signal, saveProgress, ...(progress ? {progress} : {})};
  const writer = bindReadingProgress({controller: controller as never, session: session as never, authority: {isCurrent: () => true} as never,
    initial: {progress: {stale: false, revision: 1, location: {format: 'pdf', pageIndex: 0, progression: 0}}} as never,
    reportError: error => errors.push(error)});
  return {writer, move, errors};
}

test('HTTP 412 refreshes the server progress revision so subsequent saves succeed without blocking', async () => {
  const saves: {expectedRevision: number; pageIndex: number}[] = [];
  let reads = 0;
  const f = harness({
    progress: async () => {reads++; return {progress: {revision: 5, stale: false, location: {format: 'pdf', pageIndex: 2, progression: 0.2}}};},
    saveProgress: async request => {
      const location = request.location as {pageIndex: number};
      saves.push({expectedRevision: request.expectedRevision, pageIndex: location.pageIndex});
      if (request.expectedRevision < 5) throw new ReadingError('conflict', 412);
      return {saved: true, progress: {revision: request.expectedRevision + 1}};
    },
  });
  f.move(3); await f.writer.flush();
  assert.equal(reads, 1);
  assert.equal(f.writer.isBlocked(), false);
  assert.deepEqual(f.errors, []);
  assert.deepEqual(saves, [{expectedRevision: 1, pageIndex: 3}], 'conflict does not blindly overwrite the newer server position');
  f.move(4); await f.writer.flush();
  assert.deepEqual(saves, [{expectedRevision: 1, pageIndex: 3}, {expectedRevision: 5, pageIndex: 4}]);
  assert.equal(f.writer.isBlocked(), false);
  assert.deepEqual(f.errors, []);
  await f.writer.stop();
});

test('repeated HTTP 412 conflicts stop after the retry guard and surface the error', async () => {
  let reads = 0;
  const f = harness({
    progress: async () => {reads++; return {progress: {revision: reads + 1, stale: false, location: {format: 'pdf', pageIndex: 0, progression: 0}}};},
    saveProgress: async () => {throw new ReadingError('conflict', 412);},
  });
  for (let page = 1; page <= 4; page++) {f.move(page); await f.writer.flush();}
  assert.equal(reads, 3);
  assert.equal(f.writer.isBlocked(), true);
  assert.equal(f.errors.length, 1);
  assert.equal((f.errors[0] as ReadingError).status, 412);
  await f.writer.stop();
});

test('failed 412 recovery or missing progress reader blocks and reports cleanly', async () => {
  const recoveryError = new ReadingError('unavailable', 503);
  const failed = harness({
    progress: async () => {throw recoveryError;},
    saveProgress: async () => {throw new ReadingError('conflict', 412);},
  });
  failed.move(2); await failed.writer.flush();
  assert.equal(failed.writer.isBlocked(), true);
  assert.deepEqual(failed.errors, [recoveryError]);
  await failed.writer.stop();

  const noReader = harness({saveProgress: async () => {throw new ReadingError('conflict', 412);}});
  noReader.move(2); await noReader.writer.flush();
  assert.equal(noReader.writer.isBlocked(), true);
  assert.equal(noReader.errors.length, 1);
  await noReader.writer.stop();
});
