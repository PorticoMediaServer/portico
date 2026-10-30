import assert from 'node:assert/strict';
import test from 'node:test';
import React from 'react';
import {routePath} from '../../../packages/contracts/src/routes';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved';
import {parseBrowseCapabilities} from '../../../packages/client-core/src/browse';
import {savedViewSummary} from '../../../packages/client-core/src/presentation/saved-view-summary';
import {defaultI18n} from '../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import * as savedNavigation from '../src-next/screens/saved/navigation';
import * as savedSummaryOwner from '../src-next/screens/saved/view-summary-owner';
import * as savedRename from '../src-next/screens/saved/rename-view';
import * as customizeIntent from '../src-next/screens/home/customize-intent';
import * as librarySavedViewIntent from '../src-next/screens/library/saved-view-intent';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {screenModule} from './helpers/screen-harness.mjs';

const title: ContentEntry = {kind: 'movie', id: 'actual-film', libraryId: 'movies', title: 'Stored title'};
const queries = {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000};
const pivot = (id: string) => ({id, labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], sorts: [], filters: [], browsable: true});
const capability = (libraryId = 'movies', pivotId = 'movies') => ({
  library: {id: libraryId, name: 'Movies', kind: 'movie', defaultView: 'browse', pinned: false},
  pivots: [pivot(pivotId)], resolvedPivot: pivot(pivotId), fields: [],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
  quickFilters: [], queryLimits: queries,
});
const viewWire = (id: string) => ({id, serverId: 'server', viewerFence: 'directory-fence', kind: 'view', name: id, summary: `STORED WORDS ${id}`,
  visibility: 'private', revision: 1, role: 'owner', entryCount: 1, actions: ['pin'], status: 'ready', invalidComponents: [],
  pinned: false, pinRevision: 1, shares: [], definition: {libraryId: 'movies', pivot: 'movies', query: null,
    sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}});
const directory = (resources: readonly unknown[]) => ({serverId: 'server', viewerFence: 'directory-fence', revision: 1, resources, nextCursor: ''});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const lineOf = (resource: PersonalResource) => savedViewSummary(resource.definition!, parseBrowseCapabilities(capability(resource.definition!.libraryId, resource.definition!.pivot)), defaultI18n)!.line;

function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'Saved-own-fence'};
  let active = true;
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  const session: LocalSession = {viewer: {...authority, authority: 'local', role: 'member'}, accessToken: '', expiresAt: '2026-10-25T00:00:00Z',
    authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'fixture-family', tokenGeneration: '1'};
  const paths: string[] = [], signals: (AbortSignal | undefined)[] = [], capabilities: {gate: ReturnType<typeof deferred<unknown>>; signal?: AbortSignal}[] = [];
  let capabilityReads = 0, pendingDirectory: {gate: ReturnType<typeof deferred<unknown>>; signal?: AbortSignal} | undefined;
  const reply = async (url: URL, signal?: AbortSignal): Promise<unknown> => {
    if (url.pathname.endsWith('/saved/summary')) return {serverId: 'server', viewerFence: 'summary-fence', revision: {catalog: 4, viewer: 8},
      myList: {posters: 12, squares: 3}, favorites: {posters: 5, squares: 2}, playlists: 4, collections: 7, savedViews: 6};
    if (url.pathname === routePath('get_saved_pins_order')) return {serverId: 'server', viewerFence: 'pins-fence', revision: 3, order: []};
    if (url.pathname.endsWith('/browse-capabilities')) {
      capabilityReads++;
      if (capabilityReads === 1) return capability();
      const gate = deferred<unknown>(); capabilities.push({gate, signal}); return await gate.promise;
    }
    if (url.pathname.includes('/saved-resources')) {
      if (url.searchParams.get('shape') === 'posters') {
        const gate = deferred<unknown>(); pendingDirectory = {gate, signal};
        return await gate.promise;
      }
      return directory(url.searchParams.get('shape') === 'squares' ? [viewWire('fresh')] : [viewWire('actual')]);
    }
    const view = url.searchParams.get('view')!;
    return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'saved-fence'},
      revision: {catalog: 9, viewer: 13}, heading: {key: 'saved.title', fallback: ''}, navigation: [],
      query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
      sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 0, nextCursor: ''}]};
  };
  const unavailable = (): never => {throw new Error('unexpected_saved_port');};
  const context: NativeShellBootstrapContext = {
    current: () => active, session: () => session, authority: () => authority, navigation,
    api: new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
      const url = new URL(String(input)); paths.push(url.pathname + url.search); signals.push(init?.signal || undefined);
      return new Response(JSON.stringify(await reply(url, init?.signal || undefined)), {status: 200, headers: {'Content-Type': 'application/json'}});
    }),
    subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    libraries: () => [{id: 'movies', name: 'Movies', kind: 'movie'}],
    get home() {return unavailable();}, get events() {return unavailable();}, preferences: unavailable, subscribePreferences: unavailable,
    get appVersion() {return unavailable();}, get serverName() {return unavailable();}, get playback() {return unavailable();},
    get watch() {return unavailable();}, get account() {return unavailable();},
  };
  navigation.open({kind: 'shell', destination: {kind: 'saved'}}, authority);
  return {context, navigation, authority, paths, signals, capabilities, listeners, pendingDirectory: () => pendingDirectory,
    retire: () => {active = false; for (const listener of [...listeners]) listener();}};
}

async function stateModule() {
  const common = {requestId: async () => 'request-id', reportError: () => {}, navigate: (entry: ContentEntry) => entry};
  const menu = {open: async () => {}, isOpen: () => false, subscribe: () => () => {}};
  const {createNativeEntryActions} = await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts', import.meta.url), {
    react: {default: React}, './lifetime': {nativeDestinationLifetime}, '../read/reading-books-open': {openNativeReadingBookEntry: async () => false}, '../native-screen-services': {nativeScreenServices: () => common},
    '../watch': {nativeWatchEntryMenu: () => menu},
  });
  return screenModule(new URL('../src-next/screens/saved/state.ts', import.meta.url), {
    './selection': {createNativeSavedViewSelection: () => ({dispose() {}})},
    react: {default: React}, '../destinations/lifetime': {nativeDestinationLifetime}, '../destinations/entry-actions': {createNativeEntryActions},
    '../native-screen-services': {nativeScreenServices: () => common}, './navigation': savedNavigation, './view-summary-owner': savedSummaryOwner, './rename-view': savedRename,
    '../home/customize-intent': customizeIntent, '../library/saved-view-intent': librarySavedViewIntent,
    '../downloads/device': {createNativeDownloadsDevice: () => ({device: {}, dispose: () => {}})},
  });
}

test('the real retained Saved state owns one summaries port for a views directory and retires it with the route', async () => {
  const f = fixture();
  savedNavigation.openNativeSavedView(f.context, {view: 'views'});
  const target = f.navigation.getSnapshot().route;
  if (target.kind !== 'saved-view') throw new Error('expected Saved route');
  const {createNativeSavedState} = await stateModule();
  const state = createNativeSavedState(f.context);
  try {
    assert.equal(f.paths.length, 0, 'opening Saved reads nothing');
    const view = state.view(target);
    assert.ok(view);
    assert.ok(view.summaries, 'a views directory owns a summaries port');
    assert.equal(f.paths.length, 0, 'creating the retained view stays inert');
    await view.load(); await tick();
    const resource = view.source.getSnapshot().personal!.resources[0]!;
    assert.equal(view.summaries!.line(resource, defaultI18n), lineOf(resource));
    assert.notEqual(view.summaries!.line(resource, defaultI18n), resource.summary, 'the stored summary is never drawn');
    assert.equal(view.summaries!.line({...resource}, defaultI18n), undefined, 'only the actual window object is accepted');
    assert.equal(f.paths.filter(path => path.startsWith('/v1/content')).length, 0, 'the state never enumerates the hub directory');
    assert.ok(f.paths.filter(path => path.includes('saved-resources')).every(path => new URL(path, 'https://test.invalid').searchParams.get('kind') === 'view'));

    const summaries = view.summaries;
    f.navigation.open({kind: 'title', entry: title}, f.authority);
    f.navigation.back(f.authority);
    assert.equal(f.navigation.getSnapshot().route, target);
    assert.equal(state.view(target), view, 'Title/Back keeps the one real view');
    assert.equal(view.summaries, summaries, 'the summaries port is retained unchanged');
    assert.equal(view.source.getSnapshot().personal!.resources[0], resource);
    assert.equal(view.summaries!.line(resource, defaultI18n), lineOf(resource));

    const replacing = view.source.select({view: 'views', shape: 'squares'});
    await replacing;
    const fresh = view.source.getSnapshot().personal!.resources[0]!;
    assert.notEqual(fresh, resource);
    assert.equal(view.summaries!.line(resource, defaultI18n), undefined, 'a replaced page withholds the old object');
    assert.equal(view.summaries!.line(fresh, defaultI18n), undefined, 'the new page needs its own vocabulary');
    f.capabilities[0]!.gate.resolve(capability());
    await tick();
    assert.equal(view.summaries!.line(fresh, defaultI18n), lineOf(fresh));
    assert.equal(view.summaries!.line(resource, defaultI18n), undefined);

    const dropping = view.source.select({view: 'views', shape: 'posters'});
    await tick();
    const pending = f.pendingDirectory();
    assert.ok(pending); assert.equal(pending.signal?.aborted, false);
    f.navigation.back(f.authority);
    assert.equal(state.view(target), undefined);
    assert.equal(pending.signal?.aborted, true, 'dropping the history route aborts the borrowed source read');
    assert.equal(view.source.getSnapshot().phase, 'idle');
    assert.equal(view.summaries!.line(fresh, defaultI18n), undefined);
    await view.summaries!.start();
    assert.equal(view.summaries!.line(fresh, defaultI18n), undefined, 'a held port stays permanently inert');
    pending.gate.resolve(directory([viewWire('third')]));
    await dropping;

    savedNavigation.openNativeSavedView(f.context, {view: 'views'});
    const secondTarget = f.navigation.getSnapshot().route;
    if (secondTarget.kind !== 'saved-view') throw new Error('expected second Saved route');
    const second = state.view(secondTarget)!;
    await second.load(); await tick();
    const secondResource = second.source.getSnapshot().personal!.resources[0]!;
    const pendingVocabulary = f.capabilities[1]!;
    assert.equal(pendingVocabulary.signal?.aborted, false);
    f.retire();
    assert.equal(state.view(secondTarget), undefined);
    assert.equal(pendingVocabulary.signal?.aborted, true, 'principal retirement aborts the owned reader work');
    pendingVocabulary.gate.resolve(capability());
    await tick();
    assert.equal(second.summaries!.line(secondResource, defaultI18n), undefined);
    assert.equal(second.source.getSnapshot().phase, 'idle');
    await second.summaries!.start();
    assert.equal(second.summaries!.line(secondResource, defaultI18n), undefined, 'the retired principal leaves the held port inert');
  } finally {state.dispose(); f.navigation.dispose();}
});
