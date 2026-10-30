import test from 'node:test';
import assert from 'node:assert/strict';
import {readEventState, recoverEventState} from '../src-next/platform/event-resync.ts';
import {EventsClient} from '../../../packages/client-core/src/playback-v1/events.ts';

const tick = async () => {for (let i = 0; i < 80; i++) await Promise.resolve();};
const gate = () => {let resolve!: () => void; const promise = new Promise<void>(r => {resolve = r;}); return {promise, resolve};};

test('native notification/dashboard fan-out shares actual four-read admission without a nested permit', async () => {
  const events = new EventsClient({http: {send: async () => {throw new Error('No feed requests');}}});
  const held = gate(), controller = new AbortController(); let active = 0, maximum = 0, starts = 0;
  const offs = Array.from({length: 6}, () => recoverEventState(events, async () => {
    await Promise.all(Array.from({length: 2}, () => readEventState(events, controller.signal, async () => {
      starts++; active++; maximum = Math.max(maximum, active); await held.promise; active--;
    })));
  }));
  try {
    events.deliver({id: 'native-resync', type: 'stream.resync'}); await tick();
    assert.equal(active, 4); held.resolve(); await tick();
    assert.equal(starts, 12); assert.equal(maximum, 4);
  } finally {held.resolve(); controller.abort(); offs.forEach(off => off()); events.resyncReads.dispose();}
});

test('native actual GET waiting for admission is withdrawn when its original lifetime retires', async () => {
  const events = new EventsClient({http: {send: async () => {throw new Error('No feed requests');}}});
  const held = gate(), controller = new AbortController();
  const occupied = Array.from({length: 4}, () => readEventState(events, new AbortController().signal, () => held.promise));
  await tick(); let called = false;
  const queued = readEventState(events, controller.signal, async () => {called = true;});
  controller.abort(); await assert.rejects(queued); held.resolve(); await Promise.all(occupied); await tick();
  assert.equal(called, false); events.resyncReads.dispose();
});

test('native compatibility port coalesces a held read and disposal prevents any later wake', async () => {
  const listeners = new Set<() => void>(), held = gate(); let reads = 0;
  const events = {onResync: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const off = recoverEventState(events, async () => {reads++; await held.promise;});
  for (let i = 0; i < 20; i++) for (const listener of listeners) listener();
  await tick(); assert.equal(reads, 1); held.resolve(); await tick(); assert.equal(reads, 2);
  off(); assert.equal(listeners.size, 0);
});
