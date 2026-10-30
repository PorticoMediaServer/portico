import test from 'node:test';
import assert from 'node:assert/strict';
import {createWebScreenRegistry, defineWebAction, defineWebScreen, invokeWebAction, webScreenIdForRoute, webScreenPaths} from '../src/route-registry.ts';
const Screen = () => null;
test('every existing destination has a stable screen id, with no prefix/opaque-id inference', () => {
  assert.equal(webScreenPaths.device, '/device');
  for (const [id, path] of Object.entries(webScreenPaths)) assert.equal(webScreenIdForRoute(path), id);
  assert.equal(webScreenIdForRoute('/person/$personId'), 'person');
  assert.equal(webScreenIdForRoute('/read/$itemId'), 'read');
  assert.equal(webScreenIdForRoute('/person/some-id'), undefined);
  assert.equal(webScreenIdForRoute('/read/some-id'), undefined);
  assert.equal(webScreenIdForRoute('/library/some-id'), undefined);
  assert.equal(webScreenIdForRoute('/settings/extra'), undefined);
});

test('ruling35 feedback registers one dialog action without a page or navigation target', () => {
  const Host = () => null;
  const definition = defineWebAction({id: 'feedback', host: Host, subscribe: () => () => {}, getSnapshot: () => false, invoke() {assert.fail('not a mount effect');}});
  const registry = createWebScreenRegistry({'feedback.action.tsx': {default: definition}});
  assert.equal(registry.getAction('feedback')?.host, Host);assert.deepEqual(registry.actions(), [definition]);
  assert.equal(Object.hasOwn(webScreenPaths, 'feedback'), false);assert.equal(webScreenIdForRoute('/feedback'), undefined);
  assert.equal(registry.missing().some(id => String(id) === 'feedback'), false);assert.deepEqual(registry.retainedHosts(), []);
});
test('feedback gestures reread actual permission and captured currentness instead of stale menu state', () => {
  let canSubmit = false, current = true, opened = 0;
  const action = defineWebAction({id: 'feedback', host: Screen, subscribe: () => () => {}, getSnapshot: () => canSubmit, invoke: () => {opened++;}});
  assert.equal(invokeWebAction(undefined, () => current), false);assert.equal(invokeWebAction(action, () => current), false);
  canSubmit = true;assert.equal(invokeWebAction(action, () => current), true);assert.equal(opened, 1);
  current = false;assert.equal(invokeWebAction(action, () => current), false);current = true;canSubmit = false;
  assert.equal(invokeWebAction(action, () => current), false);assert.equal(opened, 1);
});
test('multiple feedback renderers cannot overwrite the action owner', () => {
  const action = defineWebAction({id: 'feedback', host: Screen, subscribe: () => () => {}, getSnapshot: () => false, invoke() {}});
  assert.throws(() => createWebScreenRegistry({'first.action.tsx': {default: action}, 'second.action.tsx': {default: action}}), /duplicate-web-action:feedback/);
});
test('screen modules register independently; missing components remain visible to diagnostics', () => {
  const registry = createWebScreenRegistry({'./screens/settings/settings.route.tsx': {default: defineWebScreen({id: 'settings', component: Screen})}, './screens/accounts/entry.route.tsx': {default: [defineWebScreen({id: 'signIn', component: Screen}), defineWebScreen({id: 'profiles', component: Screen})]}});
  assert.equal(registry.get('settings')?.component, Screen);
  assert.equal(registry.get('player'), undefined);
  assert.equal(registry.chrome('settings'), 'shell');
  assert.equal(registry.chrome('signIn'), 'standalone');
  assert.equal(registry.chrome('player'), 'player');
  assert.equal(registry.chrome('read'), 'standalone');
  assert.equal(registry.chrome('device'), 'standalone');
  assert.equal(registry.chrome('person'), 'shell');
  assert.ok(registry.missing().includes('player'));
  assert.ok(!registry.missing().includes('profiles'));
});
test('a second owner cannot silently overwrite a route or install a second media host', () => {
  assert.throws(() => createWebScreenRegistry({'a': {default: {id: 'home', component: Screen}}, 'b': {default: {id: 'home', component: Screen}}}), /duplicate-web-screen:home/);
  assert.throws(() => defineWebScreen({id: 'home', component: Screen, playbackHost: Screen}), /playback-host/);
  const registry = createWebScreenRegistry({'player': {default: {id: 'player', component: Screen, playbackHost: Screen}}});
  assert.equal(registry.get('player')?.playbackHost, Screen);
});

test('a screen registers its own retained host without installing another player host', () => {
  const ReadHost = () => null;
  const registry = createWebScreenRegistry({'read': {default: defineWebScreen({id: 'read', component: Screen, chrome: 'standalone', retainedHost: ReadHost})},
    'player': {default: defineWebScreen({id: 'player', component: Screen, playbackHost: Screen})}});
  assert.deepEqual(registry.retainedHosts(), [{id: 'read', component: ReadHost}]);
  assert.equal(registry.get('player')?.playbackHost, Screen);
  assert.deepEqual(createWebScreenRegistry({}).retainedHosts(), []);
  assert.throws(() => defineWebScreen({id: 'player', component: Screen, retainedHost: Screen}), /already-has-playback-host/);
});

test('Read and Person preserve actual opaque ids through dedicated destinations', () => {
  assert.equal(webScreenPaths.read, '/read/$itemId');
  assert.equal(webScreenPaths.person, '/person/$personId');
  assert.equal(webScreenIdForRoute('/read/$itemId'), 'read');
  assert.equal(webScreenIdForRoute('/person/$personId'), 'person');
});


test('a screen owns its query schema; fallback validators keep unregistered routes unchanged', () => {
  const fallback = (raw: Record<string, unknown>) => typeof raw.resourceId === 'string' ? {resourceId: raw.resourceId} : {};
  const absent = createWebScreenRegistry({});
  assert.deepEqual(absent.validateSearch('saved', {resourceId: 'actual', unknown: 'discarded'}, fallback), {resourceId: 'actual'});
  assert.deepEqual(absent.validateSearch('live', {channelId: 'not-forwarded'}), {});
  let received: unknown;
  const registry = createWebScreenRegistry({'saved': {default: defineWebScreen({id: 'saved', component: Screen, validateSearch: raw => {received = raw;return {view: 'history', sort: 'recent', resourceId: raw.resourceId};}})}});
  const raw = {view: 'history', resourceId: 'actual', credential: 'discarded'};
  assert.deepEqual(registry.validateSearch('saved', raw, fallback), {view: 'history', sort: 'recent', resourceId: 'actual'});
  assert.equal(received, raw);
});
test('a rejecting or malformed screen parser never falls back to unvalidated navigation state', () => {
  let fallbacks = 0;
  const registry = createWebScreenRegistry({'live': {default: {id: 'live', component: Screen, validateSearch: () => {throw new Error('invalid-channel');}}}});
  assert.throws(() => registry.validateSearch('live', {channelId: 'invalid'}, () => {fallbacks++;return {sourceId: 'inferred'};}), /invalid-channel/);
  assert.equal(fallbacks, 0);
  for (const result of [null, [], 'raw']) {
    const bad = createWebScreenRegistry({'saved': {default: {id: 'saved', component: Screen, validateSearch: (() => result) as never}}});
    assert.throws(() => bad.validateSearch('saved', {}), /invalid-web-search-result/);
  }
});
