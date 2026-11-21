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


test('TV Saved sends title A to Z to both lists and honors an explicit direction', async () => {
  const routes: unknown[] = [];
  class Service {
    subscribe() {return () => {};}
    getSnapshot() {return {};}
    async select(route: unknown) {routes.push(route);}
    dispose() {}
  }
  const {NativeTVSavedController} = await screenModule(new URL('../src-next/screens/destinations/saved/model.ts', import.meta.url), {
    '../../../../../../packages/client-core/src/saved': {savedListSorts: [], SavedService: Service},
    '../../saved/view-model': {NativeSavedViewDestination: Service}, '../lifetime': {}, '../../native-screen-services': {}, react: {default: {}},
  });
  const controller = new NativeTVSavedController({api: {}, scope: {}, current: () => true});
  try {
    await controller.select('favorites', {sort: 'title'});
    await controller.select('watchlist', {sort: 'title', direction: 'desc'});
    assert.deepEqual(routes, [
      {view: 'favorites', shape: 'posters', sort: 'title', direction: 'asc', filter: 'all'},
      {view: 'favorites', shape: 'squares', sort: 'title', direction: 'asc', filter: 'all'},
      {view: 'watchlist', shape: 'posters', sort: 'title', direction: 'desc', filter: 'all'},
      {view: 'watchlist', shape: 'squares', sort: 'title', direction: 'desc', filter: 'all'},
    ]);
  } finally {controller.dispose();}
});

test('TV Saved reads the shown tab again in place when its page comes back, so a deleted playlist does not linger', async () => {
  const calls: string[] = [];
  class Service {
    route: any;
    subscribe() {return () => {};}
    getSnapshot() {return {};}
    async select(route: any) {this.route = route; calls.push(`select:${route.view}${route.shape ? `:${route.shape}` : ''}`);}
    async refresh() {calls.push(`refresh:${this.route?.view}${this.route?.shape ? `:${this.route.shape}` : ''}`);}
    dispose() {}
  }
  const {NativeTVSavedController} = await screenModule(new URL('../src-next/screens/destinations/saved/model.ts', import.meta.url), {
    '../../../../../../packages/client-core/src/saved': {savedListSorts: ['title'], SavedService: Service},
    '../../saved/view-model': {NativeSavedViewDestination: Service}, '../lifetime': {}, '../../native-screen-services': {}, react: {default: {}},
  });
  const controller = new NativeTVSavedController({api: {}, scope: {}, current: () => true});
  try {
    await controller.select('playlists', {filter: 'mine'});
    await controller.reload();
    assert.deepEqual(calls, ['select:playlists', 'refresh:playlists'], 'the same playlists route is read again, not reset');
    calls.length = 0;
    await controller.select('favorites');
    await controller.reload();
    assert.deepEqual(calls.filter(call => call.startsWith('refresh')), ['refresh:favorites:posters', 'refresh:favorites:squares']);
  } finally {controller.dispose();}
});
