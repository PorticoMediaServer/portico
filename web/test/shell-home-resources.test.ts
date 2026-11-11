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
test('the first Home document creates once; a refresh and a moved viewer fence keep the same resource, and only retirement ends it', () => {
  const home = source(), first = document('actual-first');let creates = 0, disposed = 0, current = true;
  const value = {};
  const resources = new HomeDocumentResources(home, {isCurrent: () => current, create: actual => {assert.equal(actual, first);creates++;return {value, dispose: () => {disposed++;}};}, changed() {}, failed: assert.fail});
  home.publish(first);assert.equal(resources.get(), value);
  home.publish({...first});assert.equal(resources.get(), value);assert.equal(creates, 1);
  // The viewer's access was decided again: the newer Home answer is followed by the resource itself.
  home.publish(document('actual-replacement'));assert.equal(resources.get(), value);assert.equal(disposed, 0);
  home.publish(undefined, 'loading');assert.equal(resources.get(), value);
  home.publish(first);assert.equal(resources.get(), value);assert.equal(creates, 1);assert.equal(disposed, 0);
  current = false;home.publish(first);assert.equal(resources.get(), undefined);assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
  current = true;home.publish(document('after-retirement'));assert.equal(resources.get(), undefined);assert.equal(creates, 1);
  resources.dispose();assert.equal(disposed, 1);
});
test('an immediate subscription callback and an already-loaded document still create a single resource', () => {
  const home = source(document('actual-fence'), true);let creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => ({value: ++creates, dispose() {}}), changed() {}, failed: assert.fail});
  assert.equal(resources.get(), 1);assert.equal(creates, 1);resources.dispose();
});
test('retirement during creation disposes the uncommitted resource', () => {
  const home = source();let current = true, disposed = 0, creates = 0;
  const resources = new HomeDocumentResources(home, {isCurrent: () => current, create: () => {
    creates++;current = false;
    return {value: {}, dispose: () => {disposed++;}};
  }, changed() {}, failed: assert.fail});
  home.publish(document('first'));
  assert.equal(creates, 1);assert.equal(resources.get(), undefined);assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
});
test('a moved viewer fence published during creation commits the one resource, which follows that newer document itself', () => {
  const home = source();let disposed = 0, creates = 0;
  const value = {};
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {
    creates++;home.publish(document('replacement'));
    return {value, dispose: () => {disposed++;}};
  }, changed() {}, failed: assert.fail});
  home.publish(document('first'));
  assert.equal(creates, 1);assert.equal(resources.get(), value);assert.equal(disposed, 0);assert.equal(home.listeners.size, 1);
  resources.dispose();assert.equal(disposed, 1);assert.equal(home.listeners.size, 0);
});
test('a media setup failure reports once and leaves the actual Home state available to the page', () => {
  const home = source(), error = new Error('actual_media_setup_failure'), failures: unknown[] = [];
  const resources = new HomeDocumentResources(home, {isCurrent: () => true, create: () => {throw error;}, changed() {}, failed: error => {failures.push(error);}});
  const actual = document('actual-fence');assert.doesNotThrow(() => home.publish(actual));
  assert.equal(home.getSnapshot().document, actual);assert.equal(resources.get(), undefined);assert.deepEqual(failures, [error]);assert.equal(home.listeners.size, 0);
});
