import {dockFence} from './helpers/dock-fence.ts';
/** Authored UNRUN UI-resource vectors. No native capture, transfer or decoder is
 * executed; no original-frame, native-file or playback acceptance is claimed. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {DockVideoExitImages} from '../src-next/kit/dock/video-exit-paint.ts';
const fence = dockFence();
const bounds = {x: 24, y: 700, width: 80, height: 45};
function rig() {
  let view = {width: 390, height: 844}, original = true, paint = true, releases = 0, claims = 0;
  const life = new AbortController(), listeners = new Set<() => void>(), image = {uri: 'file:///captured-original', width: 1920, height: 1080};
  const grant = {fence, image, signal: life.signal, paintCurrent: () => paint && !life.signal.aborted,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    release: async () => {releases++;}};
  let finish!: (value: typeof grant | undefined) => void;
  const captured = {fence, image, retainForExit: () => {claims++; return new Promise<typeof grant | undefined>(resolve => {finish = resolve;});}};
  const errors: unknown[] = [], store = new DockVideoExitImages(() => view, error => errors.push(error));
  return {store, grant, captured, errors, get claims() {return claims;}, get releases() {return releases;},
    offer: (decoded = true) => store.offer(captured, decoded, bounds, () => original), finish: () => finish(grant),
    replace: () => {original = false;}, resize: () => {view = {width: 844, height: 390};},
    retire: () => {paint = false; life.abort(); for (const notify of [...listeners]) notify();}};
}

test('only an already-decoded original can begin one pre-retirement claim', async () => {
  const r = rig();
  await r.offer(false); assert.equal(r.claims, 0);
  const pending = r.offer(); assert.equal(r.claims, 1);
  await r.offer(); assert.equal(r.claims, 1);
  r.finish(); await pending;
  assert.equal(r.store.take({...fence, generation: 2}, bounds), undefined);
  assert.equal(r.store.take(fence, {...bounds, x: 25}), undefined);
  const original = r.store.take(fence, bounds); assert.ok(original);
  assert.equal(r.store.take(fence, bounds), undefined);
  await original.release(); await original.release();
  assert.equal(r.releases, 1);
});

test('a grant completed after replacement is released and never offered', async () => {
  const r = rig(), pending = r.offer(); r.replace(); r.finish(); await pending;
  await Promise.resolve();
  assert.equal(r.store.take(fence, bounds), undefined);
  assert.equal(r.releases, 1);
});

test('a completed grant survives only playback replacement, not original paint retirement', async () => {
  const r = rig(), pending = r.offer(); r.finish(); await pending;
  r.replace();
  const original = r.store.take(fence, bounds); assert.ok(original);
  assert.equal(original.paintCurrent(), true);
  r.retire(); await Promise.resolve(); await Promise.resolve();
  assert.equal(original.paintCurrent(), false);
  assert.equal(r.releases, 1);
});

test('resized geometry and disposal reject original pixels without retargeting their rectangle', async () => {
  const r = rig(), pending = r.offer(); r.finish(); await pending; r.resize();
  assert.equal(r.store.take(fence, bounds), undefined);
  r.store.dispose(); await Promise.resolve(); await Promise.resolve();
  assert.equal(r.releases, 1);
});

test('effect replay clears old claims but a fresh captured frame can serve the still-live original scope', async () => {
  const r = rig(), pending = r.offer();
  r.store.clear(); r.finish(); await pending; await Promise.resolve();
  assert.equal(r.store.take(fence, bounds), undefined);
  assert.equal(r.releases, 1);
  let freshClaims = 0, freshReleases = 0;
  const image = {...r.grant.image, uri: 'file:///fresh-original'};
  const grant = {...r.grant, image, release: async () => {freshReleases++;}};
  const frame = {fence, image, retainForExit: async () => {freshClaims++; return grant;}};
  await r.store.offer(frame, true, bounds, () => true);
  const retained = r.store.take(fence, bounds);
  assert.ok(retained); assert.equal(freshClaims, 1);
  r.store.dispose(); await Promise.resolve(); await Promise.resolve();
  assert.equal(retained.paintCurrent(), false); assert.equal(freshReleases, 1);
  await r.store.offer({...frame}, true, bounds, () => true);
  assert.equal(freshClaims, 1, 'a genuinely disposed owner never reactivates');
});

test('retiring an unused old file preserves a pending claim for the actual current source', async () => {
  const r = rig(), first = r.offer(); r.finish(); await first;
  const nextFence = {...fence, generation: 2}, image = {...r.grant.image, uri: 'file:///second-original'};
  let resolve!: (value: typeof grant) => void;
  const grant = {...r.grant, fence: nextFence, image, signal: new AbortController().signal, release: async () => {}};
  const frame = {fence: nextFence, image, retainForExit: () => new Promise<typeof grant>(done => {resolve = done;})};
  const pending = r.store.offer(frame, true, bounds, () => true);
  r.store.retireUnconsumed(nextFence); await Promise.resolve(); await Promise.resolve();
  assert.equal(r.releases, 1);
  resolve(grant); await pending;
  assert.ok(r.store.take(nextFence, bounds));
  r.store.dispose();
});


test('the exit canvas keeps the original measured conversion and refuses host relayout', async () => {
  const r = rig(); let hostCurrent = true;
  const local = {x: 20, y: 690, width: 80, height: 45};
  const pending = r.store.offer(r.captured, true, bounds, () => true, {bounds: local, current: () => hostCurrent});
  r.finish(); await pending;
  const paint = r.store.take(fence, bounds); assert.ok(paint);
  assert.deepEqual(paint.canvasBounds, local);
  assert.notEqual(paint.canvasBounds, local);
  hostCurrent = false;
  assert.equal(paint.paintCurrent(), false, 'neither a successor host nor its rectangle can be substituted');
  await paint.release(); assert.equal(r.releases, 1);
});
