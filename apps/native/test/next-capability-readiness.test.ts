import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeCapabilityReadiness} from '../src-next/platform/playback/capability-readiness.ts';

const document = (id: string) => ({id}) as never;
const settle = async () => {for (let i = 0; i < 8; i++) await Promise.resolve();};

test('FMT-02: an acknowledged or unchanged document satisfies readiness', async () => {
  for (const result of ['accepted', 'current'] as const) {
    const published: unknown[] = [];
    const readiness = new NativeCapabilityReadiness({publish: async value => {published.push(value); return result;}}, () => false);
    void readiness.publish(async () => document('a'));
    await readiness.ready();
    assert.deepEqual(published, [document('a')]);
  }
});

test('FMT-02: one failure is retried for the same document; a second failure fails the start closed', async () => {
  const results = ['failed', 'accepted'], published: unknown[] = [];
  const readiness = new NativeCapabilityReadiness({publish: async value => {published.push(value); return results.shift() as never;}}, () => false);
  void readiness.publish(async () => document('a'));
  await readiness.ready();
  assert.deepEqual(published, [document('a'), document('a')]);
  const failing = new NativeCapabilityReadiness({publish: async () => 'failed'}, () => false);
  void failing.publish(async () => document('b'));
  await assert.rejects(failing.ready());
  const throwing = new NativeCapabilityReadiness({publish: async () => 'accepted'}, () => false);
  void throwing.publish(async () => {throw new Error('facts unavailable');});
  await assert.rejects(throwing.ready());
});

test('FMT-02: a superseded publication never stands in for its successor', async () => {
  let release!: () => void; const held = new Promise<void>(done => {release = done;});
  const published: string[] = [];
  const readiness = new NativeCapabilityReadiness({publish: async value => {
    published.push((value as unknown as {id: string}).id);
    if ((value as unknown as {id: string}).id === 'old') {await held; return 'accepted';}
    return 'failed';
  }}, () => false);
  void readiness.publish(async () => document('old'));
  await settle();
  void readiness.publish(async () => document('new'));
  const ready = readiness.ready();
  release();
  await assert.rejects(ready, 'the old acknowledgement cannot satisfy the newer failed document');
  assert.deepEqual(published, ['old', 'new', 'new']);
});

test('FMT-02: nothing is published after disposal', async () => {
  let closed = false; const published: unknown[] = [];
  const readiness = new NativeCapabilityReadiness({publish: async value => {published.push(value); return 'accepted';}}, () => closed);
  closed = true;
  void readiness.publish(async () => document('a'));
  await readiness.ready();
  assert.deepEqual(published, []);
});
