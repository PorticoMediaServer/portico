// Delete this version…: the server's preview runs the deletion as a dry run on its single writer, so the app asks it once
// per press of Delete, never live (desk A's condition on #3354/#3379).
import test from 'node:test';
import assert from 'node:assert/strict';
import {VersionDeletionOpening} from '../src/screens/detail/version-deletion.ts';

const preview = {versionId: 'ver_2', title: 'Roses', titles: 0, files: 1, bytes: 1, listEntries: 0, otherPeople: 0, alsoRemoves: []};
const settle = () => new Promise(resolve => setImmediate(resolve));

test('one press asks the preview once; reading, subscribing and re-rendering ask nothing more', async () => {
  let asked = 0;
  const opening = new VersionDeletionOpening(async () => {asked++; return preview;});
  assert.equal(opening.getSnapshot().phase, 'checking', 'the confirm shows its busy state while the preview comes');
  for (let n = 0; n < 5; n++) {opening.getSnapshot(); opening.subscribe(() => {})();}
  await settle();
  assert.deepEqual([asked, opening.getSnapshot().phase], [1, 'ready']);
  opening.retry(); await settle();
  assert.equal(asked, 1, 'Try again does nothing once the preview is in');
  opening.dispose();
});

test('Try again after a failure is a new press: one more request, no more', async () => {
  let asked = 0;
  const opening = new VersionDeletionOpening(async () => {if (++asked === 1) throw Object.assign(new Error('down'), {status: 503}); return preview;});
  await settle();
  assert.equal(opening.getSnapshot().phase, 'failed');
  opening.retry(); opening.retry(); await settle();
  assert.deepEqual([asked, opening.getSnapshot().phase], [2, 'ready']);
});

test('closing stops a preview on its way, and a closed opening never asks again', async () => {
  let signal: AbortSignal | undefined, asked = 0;
  const opening = new VersionDeletionOpening(seen => {asked++; signal = seen; return new Promise(() => {});});
  opening.dispose();
  assert.equal(signal?.aborted, true);
  opening.retry(); await settle();
  assert.equal(asked, 1);
});
