import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// useSyncExternalStore re-renders whenever getSnapshot returns a different object; TV Saved
// built a new one on every read and crashed with "Maximum update depth exceeded" (P2-74 E-tv-11).
test('the TV Saved activation returns one snapshot until navigation actually changes', async () => {
  class Service {subscribe() {return () => {};} getSnapshot() {return {};} dispose() {}}
  const {createNativeTVSavedDestination} = await screenModule(new URL('../src-next/screens/destinations/saved/model.ts', import.meta.url), {
    '../../../../../../packages/client-core/src/saved': {savedListSorts: [], SavedService: Service, fetchSavedSummary: async () => ({})},
    '../../../../../../packages/contracts/src/routes': {routePath: () => '/v1/saved'},
    '../../saved/view-model': {NativeSavedViewDestination: class {}},
    '../lifetime': {nativeDestinationLifetime: () => ({scope: {}, current: () => true})},
    '../../native-screen-services': {nativeScreenServices: () => ({requestId: () => 'request'})},
    react: {default: {}},
  });
  let route = {route: {kind: 'shell', destination: {kind: 'saved'}}, revision: 1}, intent = 'enter';
  const navigation = {subscribe: () => () => {}, getSnapshot: () => route, getIntent: () => intent};
  const {activation, dispose} = createNativeTVSavedDestination({api: {}, navigation});
  try {
    const first = activation.getSnapshot();
    assert.equal(first.visible, true);
    assert.strictEqual(activation.getSnapshot(), first, 'an unchanged route reads the same object');
    route = {...route, revision: 2};
    const second = activation.getSnapshot();
    assert.notStrictEqual(second, first); assert.equal(second.revision, 2);
    assert.strictEqual(activation.getSnapshot(), second);
    intent = 'restore';
    assert.equal(activation.getSnapshot().intent, 'restore');
  } finally {dispose();}
});
