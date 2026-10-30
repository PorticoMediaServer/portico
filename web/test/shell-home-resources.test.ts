import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeDocument} from '@core/home.ts';
import type {HomeSnapshot} from '@core/home-service.ts';
import {HomeDocumentResources} from '../src/shell/home-resources.ts';

const document = (viewerFence: string): HomeDocument => ({viewerFence} as HomeDocument);
function source(initial?: HomeDocument, synchronous = false) {
  let snapshot: HomeSnapshot = {phase: 'idle', paging: new Set(), document: initial};
  const listeners = new Set<() => void>();
  return {listeners, getSnapshot: () => snapshot,
    subscribe(listener: () => void) {listeners.add(listener);if (synchronous) listener();return () => {listeners.delete(listener);};},
    publish(document?: HomeDocument, phase: HomeSnapshot['phase'] = 'ready') {snapshot = {phase, paging: new Set(), document};for (const listener of listeners) listener();},
  };
}
test('initial Home loading or failure does not invent a fence or construct media resources', () => {
  const home = source();let creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {creates++;return {value: {}, dispose() {}};}, changed() {}, failed: assert.fail});
  home.publish(undefined, 'loading');home.publish(undefined, 'error');
  assert.equal(resources.get(), undefined);assert.equal(creates, 0);
  resources.dispose();assert.equal(home.listeners.size, 0);
});
test('the first genuine fence creates once; ordinary same-viewer refresh retains resources, a new fence retires them', () => {
  const home = source(), first = document('actual-first');let creates = 0, disposed = 0;
  const value = {};
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: actual => {assert.equal(actual, first);creates++;return {value, dispose: () => {disposed++;}};}, changed() {}, failed: assert.fail});
  home.publish(first);assert.equal(resources.get(), value);
  home.publish({...first});assert.equal(resources.get(), value);assert.equal(creates, 1);
  home.publish(document('actual-replacement'));assert.equal(resources.get(), undefined);assert.equal(disposed, 1);
  home.publish(first);assert.equal(resources.get(), undefined);resources.dispose();assert.equal(disposed, 1);
});
test('an immediate subscription callback and an already-loaded document still create a single resource', () => {
  const home = source(document('actual-fence'), true);let creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => ({value: ++creates, dispose() {}}), changed() {}, failed: assert.fail});
  assert.equal(resources.get(), 1);assert.equal(creates, 1);resources.dispose();
});
test('retirement or a replacement notification during creation disposes the uncommitted resource', () => {
  for (const change of ['authority', 'fence'] as const) {
    const home = source();let current = true, disposed = 0, creates = 0;
    const resources = new HomeDocumentResources(home, {isCurrent: () => current, create: () => {
      creates++;if (change === 'authority') current = false;else home.publish(document('replacement'));
      return {value: {}, dispose: () => {disposed++;}};
    }, changed() {}, failed: assert.fail});
    home.publish(document('first'));
    assert.equal(creates, 1);assert.equal(resources.get(), undefined);assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
  }
});
test('a media setup failure reports once and leaves the actual Home state available to the page', () => {
  const home = source(), error = new Error('actual_media_setup_failure'), failures: unknown[] = [];
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {throw error;}, changed() {}, failed: error => {failures.push(error);}});
  const actual = document('actual-fence');assert.doesNotThrow(() => home.publish(actual));
  assert.equal(home.getSnapshot().document, actual);assert.equal(resources.get(), undefined);assert.deepEqual(failures, [error]);assert.equal(home.listeners.size, 0);
});
