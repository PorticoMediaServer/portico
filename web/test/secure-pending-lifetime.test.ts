import assert from 'node:assert/strict';
import test from 'node:test';
import {SecurePendingStorage} from '../src/platform/secure-pending.ts';

// ACC-LEAF-25: the final-commit lifetime of SecurePendingStorage, authored UNRUN.
// The underlying port deliberately holds its commit after the update callback, so
// the wrapper's forwarded guard is exercised at the real seam. This tests guard
// forwarding and lifetime, not an encryption implementation.

type Record = {revision: number};
const secure = () => ({protocol: 'https:', hostname: 'media.example', secureContext: true, encryption: true, indexedDB: true, locks: true});

function heldPort(initial: Record) {
  let record: Record | undefined = initial;
  let release!: () => void;
  let ready = false;
  const gate = new Promise<void>(resolve => {release = resolve;});
  const guards: number[] = [];
  return {
    read: async () => record,
    change: async (update: (current: Record | undefined) => Record | undefined, beforeCommit?: () => void) => {
      const next = update(record);
      ready = true;
      await gate;
      guards.push(1);
      beforeCommit?.();
      record = next;
    },
    release: () => release(),
    get ready() {return ready;},
    get record() {return record;},
    get guardCalls() {return guards.length;},
  };
}

// Not thenable: the caller must be able to release the held commit before it
// awaits the change itself, otherwise the await would deadlock on the gate.
async function start(port: ReturnType<typeof heldPort>, wrapper: SecurePendingStorage<Record>, update: (current?: Record) => Record | undefined, beforeCommit?: () => void) {
  const pending = wrapper.change(update, beforeCommit);
  await Promise.resolve();
  assert.equal(port.ready, true);
  return {pending};
}

test('a retired lifetime at the final guard leaves the prior record', async () => {
  const environment = secure();
  const port = heldPort({revision: 1});
  const wrapper = new SecurePendingStorage(() => port, () => environment);
  const life = {live: true};
  const {pending} = await start(port, wrapper, current => ({revision: (current?.revision ?? 0) + 1}), () => {
    if (!life.live) throw new Error('The original continuation lifetime ended.');
  });
  life.live = false;
  port.release();
  await assert.rejects(pending, /lifetime ended/);
  assert.deepEqual(port.record, {revision: 1});
});

test('a live guard commits once and is called once', async () => {
  const environment = secure();
  const port = heldPort({revision: 1});
  const wrapper = new SecurePendingStorage(() => port, () => environment);
  let calls = 0;
  const {pending} = await start(port, wrapper, current => ({revision: (current?.revision ?? 0) + 1}), () => {calls++;});
  port.release();
  await pending;
  assert.deepEqual(port.record, {revision: 2});
  assert.equal(calls, 1);
  assert.equal(port.guardCalls, 1);
});

test('losing the secure prerequisite after the update but before the final commit refuses', async () => {
  const environment = secure();
  const port = heldPort({revision: 1});
  const wrapper = new SecurePendingStorage(() => port, () => environment);
  const {pending} = await start(port, wrapper, current => ({revision: (current?.revision ?? 0) + 1}));
  environment.secureContext = false;
  port.release();
  await assert.rejects(pending, /secure browser context/);
  assert.deepEqual(port.record, {revision: 1});
});
