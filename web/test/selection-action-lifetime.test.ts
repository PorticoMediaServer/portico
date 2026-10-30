import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionActionHostBridge, createSelectionActionLifetime} from '../src/screens/browse/selection-action-lifetime.ts';

function fixture() {
  let operationCurrent = true, document: object = {}, phase = 'ready', scope = 'route/query/viewer/family/authority', current = true;
  const initialDocument = document, signal = new AbortController(), listeners = new Set<() => void>();
  const lifetime = createSelectionActionLifetime({expectedTotal: 1000, signal: signal.signal, scopeKey: () => scope, isCurrent: () => current,
    selectionCurrent: () => operationCurrent && document === initialDocument,
    attempted: () => ['submitting', 'uncertain', 'running', 'cancelling', 'result'].includes(phase),
    subscribe: fn => {listeners.add(fn); return () => {listeners.delete(fn);};}});
  return {lifetime, signal, publish: () => {for (const fn of [...listeners]) fn();},
    ownMutation: () => {operationCurrent = false;document = {};}, phase: (next: string) => {phase = next;},
    changeScope: () => {scope = 'another-query-or-viewer';}, retire: () => {current = false;}};
}
test('query action lifetime requires the exact model operation and Home document before submission', () => {
  const f = fixture(); assert.equal(f.lifetime.source.isCurrent(), true);
  assert.equal(f.lifetime.source.expectedTotal, 1000);
  f.phase('confirming'); f.ownMutation(); assert.equal(f.lifetime.source.isCurrent(), false);
});
test('query action lifetime keeps progress/cancel after possible submission and own content publication', () => {
  const f = fixture(); f.phase('submitting'); assert.equal(f.lifetime.source.isCurrent(), true);
  f.ownMutation(); f.phase('running'); assert.equal(f.lifetime.source.isCurrent(), true);
  f.phase('uncertain'); assert.equal(f.lifetime.source.isCurrent(), true);
  f.phase('cancelling'); assert.equal(f.lifetime.source.isCurrent(), true);
  // A later status failure cannot turn the original accepted body into a new selection attempt.
  f.phase('error'); assert.equal(f.lifetime.source.isCurrent(), true);
  assert.equal(f.lifetime.attempted(), true);
  f.lifetime.retire(); f.phase('ready'); assert.equal(f.lifetime.source.isCurrent(), false);
});
test('query action lifetime never tolerates another query, viewer, route or authority after submission', () => {
  for (const end of [(f: ReturnType<typeof fixture>) => f.changeScope(), (f: ReturnType<typeof fixture>) => f.retire(),
    (f: ReturnType<typeof fixture>) => f.signal.abort()]) {
    const f = fixture(); f.phase('submitting'); assert.equal(f.lifetime.source.isCurrent(), true); end(f);
    assert.equal(f.lifetime.source.isCurrent(), false);
  }
});
test('query action bridge replacement retires a preview but preserves a submitted operation in its stable scope', () => {
  const bridge = createSelectionActionHostBridge<object>(), operation = {target: {mode: 'query'}} as never;
  let off = 0, notifications = 0, submitted = false;
  const first = {subscribe: (_fn: () => void) => () => {off++;}, captureOperation: () => operation, isOperationCurrent: () => true};
  bridge.attach(first);
  const life = createSelectionActionLifetime({expectedTotal: 1000, signal: new AbortController().signal, scopeKey: () => 'same-real-query',
    isCurrent: () => true, selectionCurrent: () => bridge.isOperationCurrent(operation), attempted: () => submitted, subscribe: bridge.subscribe});
  const unsubscribe = life.source.subscribe(() => {notifications++;});
  assert.equal(life.source.isCurrent(), true); submitted = true; assert.equal(life.source.isCurrent(), true);
  bridge.attach(undefined); assert.equal(life.source.isCurrent(), true);
  assert.equal(off, 1); assert.equal(notifications, 1); assert.equal(bridge.captureOperation(), null);
  unsubscribe(); bridge.dispose();
});
