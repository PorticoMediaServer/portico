import test from 'node:test';
import assert from 'node:assert/strict';
import {DVRModel} from '../src/dvr.ts';
import {NextDVRClient} from '../src/next-dvr.ts';
import type {DvrRecording} from '../../contracts/src/next/api.ts';

/** The recordings screens' model over the client they use on the rebuilt server (`NextDVRClient`). */
const booking = (title: string): DvrRecording => ({id: 'rec_1', channel: 'ch_1', title, startsAt: '2026-11-12T20:00:00Z', endsAt: '2026-11-12T21:00:00Z',
  padBeforeMs: 0, padAfterMs: 0, priority: 0, state: 'scheduled', inGuide: true, keep: false, fromRule: false} as DvrRecording);
const page = (title: string) => ({items: [booking(title)]});
const shown = (model: DVRModel) => model.getSnapshot().page?.recordings.map(one => one.programme.title);

test('a late answer and a disposed viewer cannot revive what the recordings screen shows', async () => {
  const pending: Array<(v: unknown) => void> = [];
  const api = {request: <T,>() => new Promise<T>(resolve => pending.push(v => resolve(v as T)))};
  const model = new DVRModel(new NextDVRClient(api));
  const first = model.load({view: 'upcoming'}), second = model.load({view: 'history'});
  pending[1](page('Newer')); await second; pending[0](page('Older')); await first;
  assert.deepEqual(shown(model), ['Newer']); assert.equal(model.getSnapshot().query.view, 'history');
  const third = model.load({view: 'recorded'}); model.dispose(); pending[2](page('Late')); await third;
  assert.equal(model.getSnapshot().page, null); assert.equal(model.getSnapshot().detail, null);
});

test('a temporary failure keeps the recordings shown and never shows its message; losing access clears them', async () => {
  let error: unknown;
  const api = {request: async <T,>() => {if (error) throw error; return page('Kept') as T;}};
  const model = new DVRModel(new NextDVRClient(api));
  await model.load(); error = new Error('/private/path/provider-password'); await model.load();
  assert.deepEqual(shown(model), ['Kept']); assert.ok(!model.getSnapshot().error.includes('private'));
  error = {status: 403}; await model.load(); assert.equal(model.getSnapshot().page, null); model.dispose();
});

test('a sign-in lost on an editor read through the shared client clears the recordings shown', async () => {
  let denied = false;
  const api = {request: async <T,>() => {if (denied) throw {status: 401}; return page('Mine') as T;}};
  const model = new DVRModel(new NextDVRClient(api));
  await model.load(); const recording = model.getSnapshot().page!.recordings[0]!; denied = true;
  await assert.rejects(model.client.deletePreview(recording));
  assert.equal(model.getSnapshot().page, null); assert.equal(model.getSnapshot().accessDenied, true);
  denied = false; await model.load(); assert.equal(model.getSnapshot().accessDenied, false); assert.deepEqual(shown(model), ['Mine']); model.dispose();
});

test('a replayed effect reactivates the model without reviving the old opening\'s reads', async () => {
  const pending: Array<(v: unknown) => void> = [];
  const api = {request: <T,>() => new Promise<T>(resolve => pending.push(v => resolve(v as T)))};
  const model = new DVRModel(new NextDVRClient(api));
  const old = model.activate(), first = model.load(); model.deactivate(old);
  assert.equal(model.getSnapshot().page, null);
  const current = model.activate(), second = model.load({view: 'recorded'});
  pending[1](page('Current')); await second; pending[0](page('Old')); await first; model.deactivate(old);
  assert.deepEqual(shown(model), ['Current']); assert.equal(model.getSnapshot().query.view, 'recorded');
  model.deactivate(current); assert.equal(model.getSnapshot().page, null);
});

test('an old action cannot clear a newer opening\'s busy state', async () => {
  const api = {request: async <T,>() => page('Any') as T};
  const model = new DVRModel(new NextDVRClient(api));
  const oldLease = model.activate();
  let finishOld!: () => void, finishNew!: () => void;
  const oldAction = model.act(() => new Promise<void>(resolve => {finishOld = resolve;}));
  model.deactivate(oldLease); model.activate();
  const newAction = model.act(() => new Promise<void>(resolve => {finishNew = resolve;}));
  finishOld(); assert.equal(await oldAction, false);
  assert.equal(model.getSnapshot().mutating, true);
  let ran = false; assert.equal(await model.act(async () => {ran = true;}), false); assert.equal(ran, false);
  finishNew(); assert.equal(await newAction, true); assert.equal(model.getSnapshot().mutating, false);
  model.dispose();
});
