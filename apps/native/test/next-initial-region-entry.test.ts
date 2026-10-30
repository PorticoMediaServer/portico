import test from 'node:test';
import assert from 'node:assert/strict';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry.ts';

// Authored for hourly CI. Controlled native-host requests are source evidence,
// never a physical focus/renderer or original FX124 reproduction claim.
function fixture() {
  const registry = createFocusRegistry(), requests: string[] = [];
  registry.configure({id: 'spotlight', entry: 'play', order: ['play', 'details']});
  registry.configure({id: 'continue', order: ['actual-first', 'actual-second']});
  const attach = (regionId: string, childId: string) => registry.attach({regionId, childId}, {requestTVFocus() {requests.push(`${regionId}/${childId}`);}});
  const request = {active: true, primary: 'spotlight', fallback: ['continue'], allowFallback: true};
  return {registry, requests, attach, request, owner: createInitialRegionEntry(registry, 'actual-page-scope')};
}
test('no Spotlight has no imaginary Play stop; the first attached row becomes the one initial native target', () => {
  const f = fixture();
  f.registry.attachRegion('spotlight', {requestTVFocus() {throw new Error('empty guide is not a page-entry leaf');}});
  assert.equal(f.owner.request(f.request), 'waiting');
  f.attach('continue', 'actual-first');
  assert.equal(f.owner.request(f.request), 'requested');
  assert.deepEqual(f.requests, ['continue/actual-first']);
  f.attach('spotlight', 'play'); assert.equal(f.owner.request(f.request), 'finished');
  assert.deepEqual(f.requests, ['continue/actual-first'], 'later content never steals an admitted entry');
});
test('a present Spotlight waits for its actual control and can use its genuine first enabled choice', () => {
  const f = fixture();f.attach('continue', 'actual-first');
  assert.equal(f.owner.request({...f.request, allowFallback: false}), 'waiting');
  f.attach('spotlight', 'details');
  assert.equal(f.owner.request({...f.request, allowFallback: false}), 'requested');
  assert.deepEqual(f.requests, ['spotlight/details'], 'missing Play is not invented when Details is the real offered control');
});
test('a detached original auth snapshot remains return history but cannot block a new page entry', () => {
  const f = fixture();f.registry.configure({id: 'auth', entry: 'original-key'});f.attach('auth', 'original-key');
  f.registry.didFocus({regionId: 'auth', childId: 'original-key'});f.registry.attach({regionId: 'auth', childId: 'original-key'}, null);
  assert.equal(f.registry.capture()?.childId, 'original-key');assert.equal(f.registry.attachedFocus(), undefined);
  f.attach('continue', 'actual-first');assert.equal(f.owner.request(f.request), 'requested');
  assert.deepEqual(f.requests, ['continue/actual-first']);
});
test('an actual focused rail/native control owns the page and later arrivals never retake focus', () => {
  const f = fixture();f.registry.configure({id: 'rail', entry: 'home'});f.attach('rail', 'home');f.registry.didFocus({regionId: 'rail', childId: 'home'});
  f.attach('continue', 'actual-first');assert.equal(f.owner.request(f.request), 'owned');
  f.registry.didBlur({regionId: 'rail', childId: 'home'});assert.equal(f.owner.request(f.request), 'finished');assert.deepEqual(f.requests, []);
});
test('an original-return lease owns the gap through actual exit even when the old host detached', () => {
  const f = fixture();f.registry.configure({id: 'original', entry: 'opener'});f.attach('original', 'opener');f.registry.didFocus({regionId: 'original', childId: 'opener'});
  const release = f.registry.retain(f.registry.capture()!);f.registry.attach({regionId: 'original', childId: 'opener'}, null);
  f.attach('continue', 'actual-first');assert.equal(f.owner.request(f.request), 'waiting');assert.deepEqual(f.requests, []);
  release();assert.equal(f.owner.request(f.request), 'requested');
});
test('disabled ancestry does not qualify as attached focus or a new page-entry destination', () => {
  const f = fixture();f.registry.configure({id: 'parent', enabled: false});f.registry.configure({id: 'child', parent: 'parent', entry: 'key'});f.attach('child', 'key');
  f.registry.didFocus({regionId: 'child', childId: 'key'});
  assert.equal(f.registry.attachedFocus(), undefined);assert.equal(f.registry.entryAddress('child'), undefined);
  assert.equal(f.owner.request({...f.request, primary: 'child', allowFallback: false}), 'waiting');
});
test('inactive pages and explicit native entry completion never issue a new focus request', () => {
  const f = fixture();f.attach('continue', 'actual-first');assert.equal(f.owner.request({...f.request, active: false}), 'waiting');
  f.owner.finish();assert.equal(f.owner.request(f.request), 'finished');assert.deepEqual(f.requests, []);
});
