import test from 'node:test';
import assert from 'node:assert/strict';
import {uploadPickedSubtitle} from '../src-next/platform/playback/subtitle-file-upload.ts';

const metadata = {language: 'en', rights: 'Viewer supplied rights', scope: 'personal' as const, offsetUs: '-500000'};
const file = {format: 'idx' as const, fileUri: 'file:///private/staging/selected.idx', companionUri: 'file:///private/staging/selected.sub', title: 'Selected subtitle'};
function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};}
const life = (controller = new AbortController(), isCurrent = () => true) => ({signal: controller.signal, isCurrent});

test('picked file references and caller metadata go to the existing service with a live combined lifetime', async () => {
  let saved = 0;
  const result = await uploadPickedSubtitle({discard: async () => true, pick: async () => file}, async (input, lifetime) => {
    saved++; assert.deepEqual(input, {...metadata, format: file.format, title: file.title, file: {uri: file.fileUri, name: 'selected.idx', type: 'application/octet-stream'}, fileName: 'selected.idx', companion: {uri: file.companionUri, name: 'selected.sub', type: 'application/octet-stream'}, companionName: 'selected.sub'});
    assert.equal(lifetime.signal.aborted, false); assert.equal(lifetime.isCurrent(), true);
  }, life(), life(), metadata);
  assert.equal(result, true); assert.equal(saved, 1);
});

test('source or sheet abort cancels an open picker and a late native result never saves', async () => {
  for (const which of ['source', 'sheet'] as const) {
    const source = new AbortController(), sheet = new AbortController(), picked = deferred<typeof file>();
    let signal: AbortSignal | undefined, saves = 0;
    const pending = uploadPickedSubtitle({discard: async () => true, pick: async value => {signal = value; return picked.promise;}}, async () => {saves++;}, life(source), life(sheet), metadata);
    (which === 'source' ? source : sheet).abort();
    assert.equal(await pending, false); assert.equal(signal?.aborted, true);
    picked.resolve(file); await Promise.resolve(); assert.equal(saves, 0);
  }
});

test('retired members and native cancellation cannot create an upload', async () => {
  let current = true, saves = 0;
  const picked = deferred<typeof file>();
  const pending = uploadPickedSubtitle({discard: async () => true, pick: () => picked.promise}, async () => {saves++;}, life(), life(undefined, () => current), metadata);
  current = false; picked.resolve(file);
  assert.equal(await pending, false); assert.equal(saves, 0);
  assert.equal(await uploadPickedSubtitle({discard: async () => true, pick: async () => undefined}, async () => {saves++;}, life(), life(), metadata), false);
  assert.equal(saves, 0);
});

test('the same lifetime follows an awaited service save and cancellation cannot acknowledge a late receipt', async () => {
  const sheet = new AbortController(), saving = deferred<void>();
  let savingLife: ReturnType<typeof life> | undefined;
  const pending = uploadPickedSubtitle({discard: async () => true, pick: async () => file}, async (_input, lifetime) => {savingLife = lifetime; await saving.promise;}, life(), life(sheet), metadata);
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(savingLife); sheet.abort();
  assert.equal(savingLife.signal.aborted, true); assert.equal(savingLife.isCurrent(), false);
  saving.resolve(); assert.equal(await pending, false);
});

test('staged files are discarded exactly after successful, failed and cancelled uploads settle', async () => {
  for (const outcome of ['success', 'failure', 'cancel'] as const) {
    const discarded: unknown[] = [], sheet = new AbortController(), saving = deferred<void>();
    const pending = uploadPickedSubtitle({pick: async () => file, discard: async uris => {discarded.push(uris);throw Error('cleanup ignored');}}, async () => {
      await saving.promise;
      if (outcome === 'failure') throw Error('upload failed');
    }, life(), life(sheet), metadata);
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(discarded, []);
    if (outcome === 'cancel') sheet.abort();
    saving.resolve();
    if (outcome === 'failure') await assert.rejects(pending);
    else assert.equal(await pending, outcome === 'success');
    assert.deepEqual(discarded, [[file.fileUri, file.companionUri]]);
  }
});

test('a late picker result after cancellation is discarded without saving', async () => {
  const picked = deferred<typeof file>(), sheet = new AbortController(), discarded: unknown[] = [];
  const pending = uploadPickedSubtitle({pick: () => picked.promise, discard: async uris => {discarded.push(uris);}}, async () => {assert.fail('cancelled picker cannot save');}, life(), life(sheet), metadata);
  sheet.abort(); assert.equal(await pending, false);
  picked.resolve(file); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(discarded, [[file.fileUri, file.companionUri]]);
});
