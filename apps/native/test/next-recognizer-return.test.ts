import test from 'node:test';
import assert from 'node:assert/strict';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {retainRecognizerReturn} from '../src-next/kit/focus/recognizer-return.ts';

// Authored UNRUN for hourly CI. Controlled hosts do not qualify native recognition/focus.
function fixture() {
  const registry = createFocusRegistry(), calls: string[] = [];
  registry.configure({id: 'keyboard.row.2', order: ['m', 'n']});
  const original = {regionId: 'keyboard.row.2', childId: 'n'};
  registry.attach(original, {requestTVFocus() {calls.push('n');}});
  registry.didFocus(original);
  return {registry, calls, original};
}
test('actual system-input completion returns once to its original attached key and releases the lease', () => {
  const f = fixture(), finish = retainRecognizerReturn(f.registry, f.original);
  assert.equal(f.registry.hasRetainedFocus(), true);
  finish(true); finish(true);
  assert.deepEqual(f.calls, ['n']); assert.equal(f.registry.hasRetainedFocus(), false);
});
test('retired/refused recognizer releases its original lease without touching a successor', () => {
  const f = fixture(), finish = retainRecognizerReturn(f.registry, f.original);
  finish(false); finish(true);
  assert.deepEqual(f.calls, []); assert.equal(f.registry.hasRetainedFocus(), false);
});
test('a missing/removed original key never invents a first-key target', () => {
  const f = fixture(), finish = retainRecognizerReturn(f.registry, f.original);
  f.registry.attach(f.original, null); finish(true);
  assert.deepEqual(f.calls, []); assert.equal(f.registry.hasRetainedFocus(), false);
  retainRecognizerReturn(f.registry, undefined)(true);
  assert.equal(f.registry.hasRetainedFocus(), false);
});
