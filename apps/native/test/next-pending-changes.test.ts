import test from 'node:test';
import assert from 'node:assert/strict';
import {NativePendingChanges} from '../src-next/shell/pending-changes.ts';

function fixture() {
  let scope: any = {serverId: 'server', origin: 'http://server', authority: 'local', accountId: 'account', profileId: 'profile', familyId: 'family', deviceId: 'device'};
  const listeners = new Set<() => void>();
  let disposed = 0, consumed = 0;
  const model: any = {dispose: () => {disposed++;}, keepAsIs: () => {consumed++;}};
  const owner = new NativePendingChanges(model, () => scope, listener => {listeners.add(listener); return () => {listeners.delete(listener);};});
  return {owner, disposed: () => disposed, consumed: () => consumed, listeners,
    change: (patch: object | undefined) => {scope = patch ? {...scope, ...patch} : undefined; for (const listener of [...listeners]) listener();}};
}

test('closing pending changes aborts reads and preserves custody; reopening has a new lifetime', () => {
  const f = fixture(); let updates = 0;
  f.owner.subscribe(() => {updates++;});
  f.owner.open(); const original = f.owner.signal()!;
  assert.equal(f.owner.getSnapshot(), 1);
  f.owner.close();
  assert.equal(original.aborted, true);
  assert.equal(f.owner.getSnapshot(), 0);
  assert.equal(f.disposed(), 0); assert.equal(f.consumed(), 0);
  f.owner.open();
  assert.equal(f.owner.getSnapshot(), 2);
  assert.notEqual(f.owner.signal(), original); assert.equal(f.owner.signal()!.aborted, false);
  assert.equal(updates, 3);
  f.owner.dispose(); assert.equal(f.disposed(), 1);
});

for (const field of ['serverId', 'origin', 'authority', 'accountId', 'profileId', 'familyId', 'deviceId']) {
  test(`a changed ${field} permanently retires the original pending-changes presentation`, () => {
    const f = fixture(); f.owner.open(); const original = f.owner.signal()!;
    f.change({[field]: 'successor'});
    assert.equal(original.aborted, true); assert.equal(f.owner.getSnapshot(), 0);
    assert.equal(f.owner.current(), false); assert.equal(f.disposed(), 1); assert.equal(f.consumed(), 0);
    assert.equal(f.listeners.size, 0);
    f.owner.open(); assert.equal(f.owner.getSnapshot(), 0);
    f.owner.dispose(); assert.equal(f.disposed(), 1);
  });
}

test('sign out retires the presentation without consuming retained work', () => {
  const f = fixture(); f.owner.open(); f.change(undefined);
  assert.equal(f.owner.getSnapshot(), 0); assert.equal(f.consumed(), 0); assert.equal(f.disposed(), 1);
});
