import test from 'node:test';
import assert from 'node:assert/strict';
import {WebLocalQueue, captureLocalQueue} from '../src/platform/playback/local-queue.ts';
import {PlayerSlot} from '../src/platform/playback/web-playback.ts';
import {ControlledLocalMedia, localPreparation, localScope} from './local-media-fixture.ts';

const settled = async () => {for (let count = 0; count < 8; count++) await new Promise(setImmediate);};
test('local queues require the complete captured family and preserve producer order without URL/session substitutes', () => {
  const source = localPreparation(), captured = captureLocalQueue(source, localScope);
  assert.deepEqual(captured.items.map(item => item.id), source.items.map(item => item.id));assert.notEqual(captured.items, source.items);
  assert.throws(() => captureLocalQueue(source, {...localScope, sessionFamilyId: 'foreign'}));
  assert.throws(() => captureLocalQueue({...source, items: [source.items[0]!, source.items[0]!]}, localScope));
  assert.throws(() => captureLocalQueue({...source, items: []}, localScope));
  const row = source.items[0]!;
  const extra = captureLocalQueue({...source, items: [{...row, title: {...row.title, privatePath: '/native/private'} as typeof row.title}]}, localScope);
  assert.equal(Object.hasOwn(extra.items[0]!.title, 'privatePath'), false, 'native implementation fields never enter the shared title projection');
  assert.equal(extra.items[0]!.title.durationSeconds, 100, 'the captured app model is already in seconds');
  assert.throws(() => captureLocalQueue({...source, items: [{...row, title: {...row.title, kind: 'album'}}]}, localScope));
  assert.throws(() => captureLocalQueue({...source, items: [{...row, durationMs: 999}]}, localScope));
});

test('only acknowledged local loads publish a real fence; ended advances all ordered members and releases exactly once', async () => {
  const port = new ControlledLocalMedia(), slot = new PlayerSlot();slot.mount(port);
  let releases = 0;const errors: unknown[] = [];
  const queue = new WebLocalQueue({scope: localScope, run: 1, port: slot, preparation: localPreparation(), isCurrent: () => true,
    release: async () => {releases++;}, reportError: error => {errors.push(error);}});
  await queue.start({signal: new AbortController().signal, isCurrent: () => true});
  const first = queue.getSnapshot().fence!;assert.equal(first.kind, 'local');assert.equal(first.sessionId, 'local:download_1:1');assert.equal(first.playId, 'journal_1');
  assert.equal(first.artifact.bytes, 1);assert.deepEqual(port.urls, []);
  port.emit({state: 'ended', positionMs: 100000});port.emit({state: 'ended', positionMs: 100000});await settled();
  assert.equal(queue.getSnapshot().index, 1);assert.deepEqual(port.loads.map(source => source.entryId), ['download_1', 'download_2']);
  assert.throws(() => queue.pause(first), /stale_playback_command/);
  const second = queue.getSnapshot().fence!;queue.pause(second);assert.equal(queue.getSnapshot().observation!.state, 'paused');
  queue.seek(second, 17);assert.equal(queue.getSnapshot().observation!.positionMs, 17000);queue.resume(second);
  await queue.previous(second);assert.equal(queue.getSnapshot().index, 1, 'past three seconds restarts this member');assert.equal(queue.getSnapshot().observation!.positionMs, 0);
  await queue.previous(second);assert.equal(queue.getSnapshot().index, 0);
  await queue.next(queue.getSnapshot().fence!);await queue.next(queue.getSnapshot().fence!);
  port.emit({state: 'ended', positionMs: 100000});await settled();assert.equal(queue.getSnapshot().phase, 'ended');
  assert.equal(queue.getSnapshot().index, 2);assert.deepEqual(errors, []);
  queue.dispose();queue.dispose();await settled();assert.equal(releases, 1);slot.dispose();
});

test('an obsolete acknowledged local load cannot unload or overwrite a newer source, even with repeated public generation', async () => {
  const port = new ControlledLocalMedia(), slot = new PlayerSlot();slot.mount(port);
  let release!: () => void;
  port.gate = async source => {if (source.entryId === 'download_1') await new Promise<void>(resolve => {release = resolve;});};
  const preparation = localPreparation();
  const source = (index: number) => ({kind: 'local' as const, scope: localScope, preparationId: preparation.preparationId,
    localSourceId: preparation.items[index]!.localSourceId, entryId: preparation.items[index]!.id, itemId: preparation.items[index]!.itemId,
    generation: 1, startPositionMs: 0, autoplay: true});
  const old = slot.loadLocal(source(0), {signal: new AbortController().signal, isCurrent: () => true});
  await settled();const rejected = assert.rejects(old, /retired/);
  const receipt = await slot.loadLocal(source(1), {signal: new AbortController().signal, isCurrent: () => true});
  const unloads = port.unloads;release();await rejected;
  assert.equal(receipt.entryId, 'download_2');assert.equal(slot.loaded, 1);assert.equal(port.unloads, unloads);assert.equal(slot.observe().state, 'playing');slot.dispose();
});
