import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {NativeShellRouteRegistry} from '../src-next/shell/route-registry.ts';

test('a cold registry creates the one Watch module for the shared entry menu before any route', async () => {
  const {react} = hooks(), registry = new NativeShellRouteRegistry();
  let created = 0, disposed = 0;
  const menus: object[] = [];
  const {registerNativeWatch, nativeWatchEntryMenu} = await screenModule(new URL('../src-next/screens/watch/register.ts', import.meta.url), {
    react, '../../shell/route-registry': {nativeShellRoutes: registry},
    './module': {createNativeWatchModule: (context: any) => {
      const identity = ++created, entryMenu = {identity}; menus.push(entryMenu);
      return {entryMenu, render: (runtime: any) => ({watch: identity, runtime}), renderOverlays: () => ({overlay: identity}), dispose: () => {disposed++;}};
    }},
  });
  const remove = registerNativeWatch((context: any) => ({context}));
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const base = {current: () => true, authority: () => authority, session: () => ({}), navigation: {allows: () => true}};
  const context = Object.defineProperties({...base}, {
    api: {get: () => {throw new Error('the registry must not construct api on a cold overlay');}},
    playback: {get: () => {throw new Error('the registry must not start playback on a cold overlay');}},
  }) as any;
  const absent = registry.bind({...base, session: () => undefined} as any);
  assert.deepEqual(absent.renderOverlays({} as any), [], 'no session means no construction');
  assert.equal(created, 0);
  absent.dispose();
  const bound = registry.bind(context);
  assert.equal(created, 0);
  assert.equal(nativeWatchEntryMenu(context), undefined);
  assert.deepEqual(bound.renderOverlays({} as any), [{overlay: 1}], 'cold overlays create and draw the Watch overlay host');
  assert.equal(created, 1, 'exactly one authority-local module is created');
  assert.equal(nativeWatchEntryMenu(context), menus[0], 'the Saved/Library/Search menu exists before any title, channel or watch route');
  assert.deepEqual(bound.renderOverlays({} as any), [{overlay: 1}]);
  assert.equal((bound.renderWatch({} as any) as any).watch, 1, 'renderWatch borrows the same instance');
  assert.equal(created, 1);
  bound.dispose(); bound.dispose();
  assert.equal(disposed, 1, 'the cold instance is disposed exactly once');
  assert.equal(nativeWatchEntryMenu(context), undefined);
  assert.equal(bound.renderOverlays({} as any), null, 'a retired binding never constructs again');
  assert.equal(created, 1);
  remove();
});

test('actual bound registry shares one Watch/title clock, Back and disposal per bootstrap authority', async () => {
  const {react} = hooks(), registry = new NativeShellRouteRegistry();
  let created = 0, disposed = 0;
  const menus: object[] = [];
  const {registerNativeWatch, nativeWatchEntryMenu} = await screenModule(new URL('../src-next/screens/watch/register.ts', import.meta.url), {
    react, '../../shell/route-registry': {nativeShellRoutes: registry},
    './module': {createNativeWatchModule: (context: any, bindings: any) => {
      assert.equal(bindings.context, context); const identity = ++created;
      const entryMenu = {identity}; menus.push(entryMenu);
      return {entryMenu, channel: {render: (route: any) => ({channel: identity, route})}, render: (runtime: any) => ({watch: identity, runtime}), title: {render: (route: any, runtime: any) => ({title: identity, route, runtime})},
        renderOverlays: () => ({overlay: identity}), back: () => identity === 1, dispose: () => {disposed++;}};
    }},
  });
  const remove = registerNativeWatch((context: any) => ({context}));
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const context = {current: () => true, authority: () => authority, session: () => ({}), navigation: {allows: () => true}} as any;
  const otherContext = {...context};
  const first = registry.bind(context), second = registry.bind(otherContext);
  const route = {kind: 'title', entry: {id: 'actual', kind: 'movie', title: 'Actual'}} as any, runtime = {} as any;
  assert.equal(created, 0); assert.equal(nativeWatchEntryMenu(context), undefined); assert.equal(created, 0);
  assert.equal((first.render(route, runtime) as any).title, 1);
  assert.equal((first.renderWatch(runtime) as any).watch, 1);
  assert.equal((first.render({kind: 'channel', sourceId: 'real-source', channelId: 'real-channel'} as any, runtime) as any).channel, 1);
  assert.equal(created, 1, 'channel/title/watch borrow the same module');
  assert.equal(nativeWatchEntryMenu(context), menus[0]);
  assert.equal(first.back(), true); assert.equal(created, 1);
  assert.deepEqual(first.renderOverlays(runtime), [{overlay: 1}]); assert.equal(created, 1);
  assert.equal((second.renderWatch(runtime) as any).watch, 2); assert.equal(second.back(), false);
  assert.equal(nativeWatchEntryMenu(otherContext), menus[1]);
  first.dispose(); first.dispose(); assert.equal(disposed, 1);
  assert.equal(nativeWatchEntryMenu(context), undefined); assert.equal(nativeWatchEntryMenu(otherContext), menus[1]);
  remove(); assert.equal(disposed, 2); assert.equal(nativeWatchEntryMenu(otherContext), undefined);
  assert.equal(second.renderWatch(runtime), null); second.dispose(); assert.equal(disposed, 2);
});
