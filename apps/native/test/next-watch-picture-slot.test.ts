// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {sameFence, playbackFence} from '../../../packages/player-core/src/index.ts';
import {sameAuthority} from '../src-next/shell/model.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = (generation: number) => playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation});
let window = {width: 800, height: 1000};
const {NativeWatchPicture} = await screenModule(new URL('../src-next/screens/watch/picture-slot.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {View: 'View', useWindowDimensions: () => window},
  '../../../../../packages/player-core/src': {sameFence}, '../../shell/model': {sameAuthority},
});

test('picture measurement retains registration through posture/window change and rejects delayed old source/window results', async () => {
  window = {width: 800, height: 1000};
  let currentFence = fence(1), registrations = 0, disposed = 0, tree: any;
  const callbacks: ((x: number, y: number, width: number, height: number) => void)[] = [], published: any[] = [];
  const view = {measureInWindow: (callback: typeof callbacks[number]) => {callbacks.push(callback);}};
  const outlet = {register() {registrations++; return {publish: (slot: unknown) => {published.push(slot); return true;}, clear() {}, dispose() {disposed++;}};}};
  const current = () => ({authority, fence: currentFence, window});
  const host = createFabricHookHost(() => {tree = NativeWatchPicture({authority, fence: currentFence, outlet, current}); tree.props.ref.current = view;});
  await host.mount();
  try {
    const old = callbacks.at(-1)!;
    currentFence = fence(2); await host.rerender();
    old(0, 20, 800, 450); assert.equal(published.length, 0);
    callbacks.at(-1)!(16, 50, 445, 250);
    assert.equal(published.length, 1); assert.equal(published[0].fence.generation, 2);
    assert.deepEqual(published[0].frameInWindow, {x: 16, y: 50, width: 445, height: 250});
    tree.props.onLayout(); const beforeFold = callbacks.at(-1)!;
    window = {width: 1100, height: 800}; beforeFold(0, 50, 800, 450);
    assert.equal(published.length, 1);
    await host.rerender(); callbacks.at(-1)!(0, 20, 1100, 619);
    assert.equal(published.length, 2); assert.equal(registrations, 1);
    assert.ok(published[1].layoutRevision > published[0].layoutRevision);
    const afterUnmount = callbacks.at(-1)!; await host.unmount(); afterUnmount(0, 20, 1100, 619);
    assert.equal(published.length, 2); assert.equal(disposed, 1);
  } finally {await host.unmount();}
});

test('a nonfinite or nonpositive measured picture never publishes native bounds', async () => {
  let tree: any, callback!: (x: number, y: number, width: number, height: number) => void;
  const published: unknown[] = [], currentFence = fence(1);
  const outlet = {register: () => ({publish: (value: unknown) => {published.push(value); return true;}, clear() {}, dispose() {}})};
  const current = () => ({authority, fence: currentFence, window});
  const host = createFabricHookHost(() => {tree = NativeWatchPicture({authority, fence: currentFence, outlet, current}); tree.props.ref.current = {measureInWindow: (value: typeof callback) => {callback = value;}};});
  await host.mount();
  try {callback(0, 0, 0, 10); tree.props.onLayout(); callback(NaN, 0, 10, 10); assert.equal(published.length, 0);} finally {await host.unmount();}
});
