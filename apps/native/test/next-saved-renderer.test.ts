import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {NativeSavedDestination} from '../src-next/screens/saved/model.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';

test('independent Saved descriptor uses the actual viewer lifetime with no Search or selection dependency', async () => {
  const {createNativeSavedRenderer} = await screenModule(new URL('../src-next/screens/saved/renderer.tsx', import.meta.url), {
    react: {default: React}, './SavedScreen': {SavedScreen: 'SavedScreen'}, './model': {NativeSavedDestination},
    '../destinations/lifetime': {nativeDestinationLifetime},
  });
  let current = true, calls = 0;
  const viewer = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'actual-epoch', viewerFence: 'home-only-fence'};
  const context = {api: {request: async () => {calls++; throw new Error('a descriptor render must not start a producer');}}, session: () => ({viewer}), authority: () => authority, current: () => current};
  const activation = {subscribe() {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})}, offline = {actual: 'injected-native-adapter'};
  const renderer = createNativeSavedRenderer(context, {activation, offline, requestId: async () => 'request-id', savedActions: {}, savedDownloadsCopy: {}, onThisPhone: 'pending-test-copy'});
  try {
    assert.equal(renderer.render({kind: 'shell', destination: {kind: 'search'}}, {}), null);
    const screen = renderer.render({kind: 'shell', destination: {kind: 'saved'}}, {});
    assert.equal(screen.type, 'SavedScreen'); assert.equal(screen.props.activation, activation); assert.equal(screen.props.device, offline);
    assert.equal(screen.props.selection, undefined, 'a genuine selection producer is optional, never replaced by a dummy');
    assert.equal(screen.props.destination.getSnapshot().phase, 'idle'); assert.equal(calls, 0);
    current = false; assert.equal(renderer.render({kind: 'shell', destination: {kind: 'saved'}}, {}), null);
    assert.equal(screen.props.destination.getSnapshot().myList.projection, null);
  } finally {renderer.dispose();}
});
