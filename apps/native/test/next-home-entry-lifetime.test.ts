import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeHomeEntryLifetime} from '../src-next/screens/home/entry-lifetime';

test('actual row/nav retirement aborts creation and Back cannot revive the original gesture', () => {
  const parent = new AbortController(), listeners = new Set<() => void>();
  let rowCurrent = true;
  const operation = nativeHomeEntryLifetime(() => rowCurrent, [listener => {listeners.add(listener); return () => {listeners.delete(listener);};}], parent.signal);
  assert.equal(operation.lifetime.isCurrent(), true);
  rowCurrent = false; for (const listener of [...listeners]) listener();
  assert.equal(operation.lifetime.signal.aborted, true);
  rowCurrent = true; assert.equal(operation.lifetime.isCurrent(), false);
  operation.dispose(); assert.equal(listeners.size, 0);
});

test('the entry predicate also fails closed before a membership subscription publishes', () => {
  let memberCurrent = true;
  const operation = nativeHomeEntryLifetime(() => memberCurrent, [], new AbortController().signal);
  memberCurrent = false;
  assert.equal(operation.lifetime.isCurrent(), false);
  operation.dispose();
});

test('disposing creation observers does not abort an accepted output; owner disposal does', () => {
  const owner = new AbortController();
  const operation = nativeHomeEntryLifetime(() => true, [], owner.signal);
  operation.dispose();
  assert.equal(operation.lifetime.signal.aborted, false);
  const pending = nativeHomeEntryLifetime(() => true, [], owner.signal);
  owner.abort(); assert.equal(pending.lifetime.signal.aborted, true); assert.equal(pending.lifetime.isCurrent(), false);
  pending.dispose();
});
