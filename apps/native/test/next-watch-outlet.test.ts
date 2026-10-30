import assert from 'node:assert/strict';
import test from 'node:test';
import {playbackFence} from '../../../packages/player-core/src';
import {NativeWatchOutlet, type NativeExpandedVideoSlot, type NativeWatchCurrent} from '../src-next/shell/watch-outlet';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
const window = {width: 1920, height: 1080};
const slot = (layoutRevision = 0): NativeExpandedVideoSlot => ({authority, fence, window, layoutRevision, frameInWindow: {x: 200, y: 120, width: 1200, height: 675}});

test('only the latest real host can publish or clear the persistent picture slot', () => {
  const outlet = new NativeWatchOutlet(() => ({authority, fence, window}));
  const first = outlet.register(authority);
  assert.equal(first.publish(slot()), true);
  const second = outlet.register(authority);
  assert.equal(outlet.getSnapshot(), undefined);
  assert.equal(first.publish(slot(1)), false);
  assert.equal(second.publish(slot()), true);
  first.clear(); first.dispose();
  assert.ok(outlet.getSnapshot(), 'retired host cannot clear the replacement');
  second.dispose();
  assert.equal(outlet.getSnapshot(), undefined);
  assert.equal(second.publish(slot(1)), false);
});

test('authority, output fence, dimensions and layout revisions fence late layout callbacks', () => {
  let current: NativeWatchCurrent | undefined = {authority, fence, window};
  const outlet = new NativeWatchOutlet(() => current), host = outlet.register(authority);
  assert.equal(host.publish(slot(2)), true);
  assert.equal(host.publish(slot(1)), false);
  assert.equal(host.publish(slot(2)), false);
  current = {...current!, window: {width: 1080, height: 1920}};
  assert.equal(outlet.getSnapshot(), undefined);
  assert.equal(host.publish(slot(3)), false);
  outlet.invalidate();
  current = {authority, fence: {...fence, generation: 2}, window};
  assert.equal(host.publish(slot(4)), false);
  current = {authority: {...authority, viewerFence: 'new'}, fence, window};
  assert.equal(host.publish(slot(5)), false);
  const stale = outlet.register(authority);
  assert.equal(stale.publish(slot(6)), false);
  current = undefined;
  assert.equal(host.publish(slot(7)), false);
});

test('invalid geometry never replaces the last admitted measurement and input mutation cannot change it', () => {
  const outlet = new NativeWatchOutlet(() => ({authority, fence, window})), host = outlet.register(authority);
  const input = slot();
  assert.equal(host.publish(input), true);
  const accepted = outlet.getSnapshot();
  for (const frame of [{x: NaN, y: 0, width: 10, height: 10}, {x: 0, y: 0, width: 0, height: 10}, {x: 2000, y: 0, width: 10, height: 10}]) {
    assert.equal(host.publish({...slot(1), frameInWindow: frame}), false);
    assert.strictEqual(outlet.getSnapshot(), accepted);
  }
  assert.notStrictEqual(accepted!.window, input.window);
  assert.notStrictEqual(accepted!.frameInWindow, input.frameInWindow);
  assert.ok(Object.isFrozen(accepted!.frameInWindow));
  host.clear();
  assert.equal(host.publish(slot()), false, 'clear does not make old callbacks admissible');
  assert.equal(host.publish(slot(1)), true);
  outlet.dispose();
  assert.equal(outlet.getSnapshot(), undefined);
  assert.equal(host.publish(slot(2)), false);
});
