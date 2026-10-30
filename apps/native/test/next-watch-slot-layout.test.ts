import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence} from '../../../packages/player-core/src';
import {watchSlotInHost, watchSlotIsCurrent} from '../src-next/shell/watch-slot-layout.ts';
import type {NativeExpandedVideoSlot} from '../src-next/shell/watch-outlet.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
const window = {width: 840, height: 880};
const slot: NativeExpandedVideoSlot = {authority, fence, layoutRevision: 0, window, frameInWindow: {x: 0, y: 32, width: 840, height: 472.5}};

test('expanded slot layout accepts the current measured picture and preserves partial viewport overlap', () => {
  assert.equal(watchSlotIsCurrent(slot, authority, fence, window), true);
  const partial = {...slot, frameInWindow: {...slot.frameInWindow, x: -16}};
  assert.equal(watchSlotIsCurrent(partial, authority, fence, window), true);
  assert.deepEqual(watchSlotInHost(partial, {x: 4, y: -45}), {x: -20, y: 77, width: 840, height: 472.5});
  assert.deepEqual(slot.frameInWindow, {x: 0, y: 32, width: 840, height: 472.5});
});

test('expanded slot layout rejects stale authority, exact playback generation and window', () => {
  assert.equal(watchSlotIsCurrent(undefined, authority, fence, window), false);
  assert.equal(watchSlotIsCurrent(slot, authority, undefined, window), false);
  for (const key of ['serverId', 'accountId', 'profileId', 'epoch', 'viewerFence']) {
    assert.equal(watchSlotIsCurrent({...slot, authority: {...authority, [key]: 'other'}}, authority, fence, window), false, key);
  }
  for (const patch of [{generation: 2}, {sessionId: 'replacement'}, {sourceId: 'other'}, {entryId: 'other'}]) {
    assert.equal(watchSlotIsCurrent({...slot, fence: {...fence, ...patch}}, authority, fence, window), false);
  }
  assert.equal(watchSlotIsCurrent(slot, authority, fence, {width: 880, height: 840}), false);
});

test('expanded slot layout refuses invalid bounds, revisions and non-intersecting pictures', () => {
  for (const patch of [{x: NaN}, {y: Infinity}, {width: 0}, {height: -1}, {x: 840}, {y: 880}, {x: -840}, {y: -472.5}, {x: Number.MAX_VALUE, width: Number.MAX_VALUE}]) {
    assert.equal(watchSlotIsCurrent({...slot, frameInWindow: {...slot.frameInWindow, ...patch}}, authority, fence, window), false, JSON.stringify(patch));
  }
  for (const revision of [-1, .5, Infinity]) assert.equal(watchSlotIsCurrent({...slot, layoutRevision: revision}, authority, fence, window), false);
  for (const width of [0, -1, NaN, Infinity]) assert.equal(watchSlotIsCurrent(slot, authority, fence, {width, height: 880}), false);
  assert.equal(watchSlotInHost(slot, {x: NaN, y: 0}), undefined);
});
