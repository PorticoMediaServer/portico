import assert from 'node:assert/strict';
import test from 'node:test';
import React from 'react';
import {routePath} from '../../../packages/contracts/src/routes';
import {HttpLocalApi} from '../../../packages/client-core/src';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import * as savedNavigation from '../src-next/screens/saved/navigation';
import * as savedSummaryOwner from '../src-next/screens/saved/view-summary-owner';
import * as savedRename from '../src-next/screens/saved/rename-view';
import * as customizeIntent from '../src-next/screens/home/customize-intent';
import * as librarySavedViewIntent from '../src-next/screens/library/saved-view-intent';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {screenModule} from './helpers/screen-harness.mjs';

const title: ContentEntry = {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Film'};
const session = {viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner'},
  accessToken: '', expiresAt: '2026-10-25T00:00:00Z', authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'family', tokenGeneration: '1'};
const browse = () => ({pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null}, entries: [],
  pageInfo: {start: 0, total: 0, revision: 'browse-1', nextCursor: '', hasMore: false}, positionIndex: []});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

type Mutation = Readonly<{path: string; method?: string; body?: any}>;
function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'resource-fence'};
  let active = true, revision = 1;
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  const paths: string[] = [], mutations: Mutation[] = [], plans: ('success' | 'timeout' | 'conflict' | {gate: ReturnType<typeof deferred<unknown>>})[] = [];
  let named = 'Stored view';
  const viewerView = (actions: string[] = ['update', 'pin']) => ({id: 'actual-view', serverId: 'server', viewerFence: 'resource-fence', kind: 'view',
    name: named, summary: 'stored summary', visibility: 'private', revision, role: 'owner', entryCount: 0, actions, status: 'ready',
    invalidComponents: [], pinned: false, pinRevision: 1, shares: [], definition: {libraryId: 'movies', pivot: 'movies', query: null,
      sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}});
  const collection = {id: 'actual-collection', serverId: 'server', viewerFence: 'resource-fence', kind: 'collection', name: 'Collection',
    summary: '', visibility: 'private', revision: 1, role: 'owner', entryCount: 0, actions: ['entries'], status: 'ready',
    invalidComponents: [], pinned: false, pinRevision: 1, shares: []};
  const directoryRow = {...viewerView(), viewerFence: 'directory-fence'};
  const resourceFor = (resourceId: string): any => resourceId === 'actual-collection' ? collection
    : resourceId === 'readonly-view' ? {...viewerView(['pin']), id: 'readonly-view'} : viewerView();
  const reply = async (url: URL, method?: string, body?: any): Promise<unknown> => {
    if (method === 'PATCH') {
      const plan = plans.shift() ?? 'success';
      if (plan === 'timeout') throw Object.assign(new Error('timed out'), {code: 'timeout'});
      if (plan === 'conflict') throw Object.assign(new Error('conflict'), {code: 'resource_conflict'});
      if (typeof plan === 'object') await plan.gate.promise;
      const resourceId = decodeURIComponent(url.pathname.split('/').at(-1)!);
      named = body.name; revision++;
      return {serverId: 'server', viewerFence: 'resource-fence', receipt: {operationId: body.operationId, resourceId, deleted: false, revision}};
    }
    if (url.pathname.endsWith('/saved/summary')) return {serverId: 'server', viewerFence: 'summary-fence', revision: {catalog: 1, viewer: 1},
      myList: {posters: 0, squares: 0}, favorites: {posters: 0, squares: 0}, playlists: 0, collections: 0, savedViews: 6};
    if (url.pathname === routePath('get_saved_pins_order')) return {serverId: 'server', viewerFence: 'pins-fence', revision: 1, order: []};
    if (url.pathname.includes('/saved-resources/') && url.pathname.endsWith('/content')) {
      const resource = resourceFor(decodeURIComponent(url.pathname.split('/').at(-2)!));
      return resource.kind === 'view' ? {resource, browse: browse(), nextCursor: ''} : {resource, entries: [], nextCursor: '', query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [], totalCount: 0, start: 0};
    }
    if (url.pathname.includes('/saved-resources')) return {serverId: 'server', viewerFence: 'directory-fence', revision: 1, resources: [directoryRow], nextCursor: ''};
    const view = url.searchParams.get('view') ?? 'watchlist';
    return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'saved-fence'},
      revision: {catalog: 1, viewer: 1}, heading: {key: 'saved.title', fallback: ''}, navigation: [],
      query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
      sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 0, nextCursor: ''}]};
  };
  const unavailable = (): never => {throw new Error('unexpected_saved_port');};
  const context: NativeShellBootstrapContext = {
    current: () => active, session: () => session, authority: () => authority, navigation,
    api: new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
      const url = new URL(String(input)), method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      paths.push(url.pathname + url.search);
      if (method === 'PATCH') mutations.push({path: url.pathname, method, body});
      return new Response(JSON.stringify(await reply(url, method, body)), {status: 200, headers: {'Content-Type': 'application/json'}});
    }),
    subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    libraries: () => [{id: 'movies', name: 'Movies', kind: 'movie'}],
    get home() {return unavailable();}, get events() {return unavailable();}, preferences: unavailable, subscribePreferences: unavailable,
    get appVersion() {return unavailable();}, get serverName() {return unavailable();}, get playback() {return unavailable();},
    get watch() {return unavailable();}, get account() {return unavailable();},
  };
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  return {context, navigation, authority, paths, mutations, plans,
    retire: () => {active = false; for (const listener of [...listeners]) listener();}};
}

let ids = 0;
async function stateModule() {
  const common = {requestId: async () => `00000000-0000-4000-8000-${String(++ids).padStart(12, '0')}`,
    reportError: () => {}, navigate: (entry: ContentEntry) => entry};
  const menu = {open: async () => {}, isOpen: () => false, subscribe: () => () => {}};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {default: React}, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async () => false}, '../native-screen-services': {nativeScreenServices: () => common},
    '../watch': {nativeWatchEntryMenu: () => menu},
  });
  return screenModule(new URL('../src-next/screens/saved/state.ts', import.meta.url), {
    './selection': {createNativeSavedViewSelection: () => ({dispose() {}})},
    react: {default: React}, '../destinations/lifetime': {nativeDestinationLifetime}, '../destinations/entry-actions': {createNativeEntryActions},
    '../native-screen-services': {nativeScreenServices: () => common}, './navigation': savedNavigation,
    './view-summary-owner': savedSummaryOwner, './rename-view': savedRename,
    '../home/customize-intent': customizeIntent, '../library/saved-view-intent': librarySavedViewIntent,
    '../downloads/device': {createNativeDownloadsDevice: () => ({device: {}, dispose: () => {}})},
  });
}

test('the retained resource view opens one exact rename over its existing service and keeps it through Title/Back', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    assert.equal(f.paths.length, 0, 'opening Saved reads nothing');
    const view = state.view(target)!;
    assert.equal(f.paths.length, 0, 'the view is inert');
    await view.load();
    const personal = view.source.getSnapshot().personal!;
    assert.equal(view.source.getSnapshot().phase, 'ready', `error=${JSON.stringify(personal.error)} mutationError=${JSON.stringify(personal.mutationError)}`);
    const resource = personal.resource!;
    assert.ok(resource, `resource missing; error=${JSON.stringify(personal.error)}`);
    assert.equal(resource.kind, 'view');
    assert.equal(view.rename({...resource}), undefined, 'a copied object is not the arrived resource');
    assert.equal(view.rename({...resource, id: 'another'}), undefined);
    const controller = view.rename(resource);
    assert.ok(controller);
    assert.equal(view.rename(resource), controller, 'the same opening returns the one controller');
    assert.equal(view.rename({...resource}), undefined, 'a clone cannot fork the open controller');

    f.navigation.open({kind: 'title', entry: title}, f.authority);
    f.navigation.back(f.authority);
    assert.equal(f.navigation.getSnapshot().route, target);
    assert.equal(state.view(target), view);
    assert.equal(view.rename(resource), controller, 'Title/Back keeps the controller identity');

    assert.equal(controller.setName('Renamed view'), true);
    assert.equal(await controller.save(), true);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(controller.getSnapshot().name, '');
    assert.equal(view.source.getSnapshot().personal!.resource!.name, 'Renamed view', 'the canonical service refreshed the resource');
    assert.equal(f.mutations.length, 1);
    assert.equal(f.mutations[0]!.method, 'PATCH');
    assert.equal(f.mutations[0]!.body.name, 'Renamed view');
    const refreshed = view.source.getSnapshot().personal!.resource!;
    assert.equal(view.rename(resource), undefined, 'the previous object is stale after the service refresh');
    const reopened = view.rename(refreshed);
    assert.ok(reopened); assert.notEqual(reopened, controller);
    assert.equal(f.mutations.length, 1, 'reopening after a confirmed close issues no command');
    reopened.dispose();
    controller.dispose();
    assert.equal(view.source.getSnapshot().phase, 'ready', 'controller disposal leaves the borrowed source ready');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('a directory row and a non-offered resource expose no rename port', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  const state = createNativeSavedState(f.context);
  try {
    savedNavigation.openNativeSavedView(f.context, {view: 'views'});
    const viewsTarget = f.navigation.getSnapshot().route;
    if (viewsTarget.kind !== 'saved-view') throw new Error('expected views route');
    const views = state.view(viewsTarget)!;
    await views.load();
    const row = views.source.getSnapshot().personal!.resources[0]!;
    assert.equal(views.rename(row), undefined, 'a directory row is not a loaded mutation service');
    f.navigation.back(f.authority);

    savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-collection'});
    const collectionTarget = f.navigation.getSnapshot().route;
    if (collectionTarget.kind !== 'saved-view') throw new Error('expected collection route');
    const collectionView = state.view(collectionTarget)!;
    await collectionView.load();
    const collection = collectionView.source.getSnapshot().personal!.resource!;
    assert.equal(collection.kind, 'collection');
    assert.equal(collectionView.rename(collection), undefined, 'another kind never offers rename');

    savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'readonly-view'});
    const readonlyTarget = f.navigation.getSnapshot().route;
    if (readonlyTarget.kind !== 'saved-view') throw new Error('expected readonly route');
    const readonly = state.view(readonlyTarget)!;
    await readonly.load();
    const held = readonly.source.getSnapshot().personal!.resource!;
    assert.equal(held.kind, 'view');
    assert.equal(held.actions.includes('update'), false);
    assert.equal(readonly.rename(held), undefined, 'an unoffered update never opens rename');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('an uncertain rename keeps its original canonical command through retry without a second mutation', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  f.plans.push('timeout', 'success');
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    const view = state.view(target)!;
    await view.load();
    const resource = view.source.getSnapshot().personal!.resource!;
    const controller = view.rename(resource)!;
    assert.equal(controller.setName('Retry name'), true);
    assert.equal(await controller.save(), false);
    let snapshot = controller.getSnapshot();
    assert.equal(snapshot.name, 'Retry name', 'the typed name survives the uncertain result');
    assert.equal(snapshot.retryPending, true);
    assert.equal(snapshot.error?.code, 'timeout');
    assert.equal(f.mutations.length, 1);
    assert.equal(view.rename(resource), controller, 'the held controller is not replaced');
    assert.equal(await controller.save(), false, 'a second save cannot fork the uncertain command');
    assert.equal(f.mutations.length, 1);
    assert.equal(await controller.retry(), true);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(f.mutations.length, 2);
    assert.equal(f.mutations[1]!.body.operationId, f.mutations[0]!.body.operationId, 'retry reuses the same operation id');
    assert.equal(f.mutations[1]!.body.name, 'Retry name', 'retry reuses the same body name');
    assert.equal(view.source.getSnapshot().personal!.resource!.name, 'Retry name');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('a replaced source generation retires the held rename and a late acknowledgment cannot publish', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  const gate = deferred<unknown>();
  f.plans.push({gate});
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    const view = state.view(target)!;
    await view.load();
    const resource = view.source.getSnapshot().personal!.resource!;
    const controller = view.rename(resource)!;
    assert.equal(controller.setName('Late name'), true);
    const saving = controller.save(); await tick();
    assert.equal(f.mutations.length, 1);
    await view.source.select({view: 'views'});
    assert.equal(controller.getSnapshot().retired, true, 'the source replacement retires the held rename');
    const retired = controller.getSnapshot();
    gate.resolve({});
    await saving; await tick();
    assert.equal(controller.getSnapshot(), retired, 'the late acknowledgment changes nothing');
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(view.source.getSnapshot().phase, 'ready');
    assert.equal(view.rename(resource), undefined, 'the replaced source offers no rename for the old resource');
    controller.dispose();
    assert.equal(view.source.getSnapshot().phase, 'ready', 'the borrowed source remains parent-owned');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('a same-id source generation retires the old service identity', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    const view = state.view(target)!;
    await view.load();
    const resource = view.source.getSnapshot().personal!.resource!;
    const controller = view.rename(resource)!;
    await view.source.select({view: 'resource', resourceId: 'actual-view', sort: 'title'});
    assert.equal(controller.getSnapshot().retired, true, 'the same id from a new service generation retires the old controller');
    const replacement = view.source.getSnapshot().personal!.resource!;
    assert.notEqual(replacement, resource);
    assert.equal(view.rename(resource), undefined, 'the old object belongs to the retired service');
    const next = view.rename(replacement);
    assert.ok(next); assert.notEqual(next, controller, 'a new generation needs its own controller');
    assert.equal(f.mutations.length, 0, 'opening over a new generation issues no request');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('closing the presentation leaves the canonical uncertain command intact', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  f.plans.push('timeout');
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    const view = state.view(target)!;
    await view.load();
    const resource = view.source.getSnapshot().personal!.resource!;
    const owner = view.source.viewRename(resource);
    assert.ok(owner, 'the owner port is captured before the command starts');
    const controller = view.rename(resource)!;
    assert.equal(controller.setName('Kept elsewhere'), true);
    assert.equal(await controller.save(), false);
    assert.equal(controller.getSnapshot().retryPending, true);
    assert.equal(controller.cancel(), true);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(view.source.getSnapshot().personal!.retryPending, true, 'the canonical service still holds the uncertain command');
    assert.equal(view.rename(resource), undefined, 'a fresh opening refuses while the owner command is retryPending');
    assert.equal(f.mutations.length, 1, 'the blocked reopening issues no command');
    assert.equal(await owner.port.retry(), true, 'the captured owner port resolves its original command');
    assert.equal(f.mutations.length, 2);
    assert.equal(f.mutations[1]!.body.operationId, f.mutations[0]!.body.operationId);
    assert.equal(f.mutations[1]!.body.name, 'Kept elsewhere');
    const refreshed = view.source.getSnapshot().personal!.resource!;
    assert.notEqual(refreshed, resource);
    const next = view.rename(refreshed);
    assert.ok(next); assert.notEqual(next, controller, 'the settled canonical service allows a new presentation');
    assert.equal(view.rename(refreshed), next);
    assert.equal(f.mutations.length, 2, 'the new opening issues no command of its own');
    assert.equal(controller.setName('revive'), false, 'the old closed presentation never revives');
    next.dispose();
    assert.equal(view.source.getSnapshot().phase, 'ready', 'the borrowed service remains parent-owned');
  } finally {state.dispose(); f.navigation.dispose();}
});

test('an in-flight Save disposed locally refuses a fresh opening until the canonical service settles', async () => {
  const f = fixture(), {createNativeSavedState} = await stateModule();
  const gate = deferred<unknown>();
  f.plans.push({gate});
  savedNavigation.openNativeSavedView(f.context, {view: 'resource', resourceId: 'actual-view'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const state = createNativeSavedState(f.context);
  try {
    const view = state.view(target)!;
    await view.load();
    const resource = view.source.getSnapshot().personal!.resource!;
    const controller = view.rename(resource)!;
    assert.equal(controller.setName('Settled later'), true);
    const saving = controller.save(); await tick();
    assert.equal(f.mutations.length, 1);
    controller.dispose();
    assert.equal(view.rename(resource), undefined, 'a pending canonical command blocks a fresh opening');
    assert.equal(f.mutations.length, 1, 'the blocked reopening issues no command');
    gate.resolve({});
    await saving; await tick();
    const refreshed = view.source.getSnapshot().personal!.resource!;
    assert.equal(refreshed.name, 'Settled later');
    const next = view.rename(refreshed);
    assert.ok(next); assert.notEqual(next, controller, 'settlement allows a new presentation');
    assert.equal(f.mutations.length, 1, 'the new opening issues no command of its own');
    assert.equal(controller.setName('revive'), false, 'the old disposed presentation never revives');
    assert.equal(view.source.getSnapshot().phase, 'ready');
    next.dispose();
    assert.equal(view.source.getSnapshot().phase, 'ready', 'the borrowed service remains parent-owned');
  } finally {state.dispose(); f.navigation.dispose();}
});
