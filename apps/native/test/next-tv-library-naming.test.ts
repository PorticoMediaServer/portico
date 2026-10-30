import test from 'node:test';
import assert from 'node:assert/strict';
import type {BrowseCapabilities} from '../../../packages/client-core/src/browse';
import {defaultI18n} from '../../../packages/i18n/src';
import {savedViewSummary} from '../../../packages/client-core/src/presentation/saved-view-summary';
import {NativeShellNavigation} from '../src-next/shell/routes';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {nativeLibraryCanSave} from '../src-next/screens/library/model';
import {NativeLibrarySaveView} from '../src-next/screens/library/save-view';
import {createNativeTVNameInput} from '../src-next/tv/library/keyboard-state';
import {screenModule} from './helpers/screen-harness.mjs';

const library = {id: 'movies', name: 'Movies', kind: 'movie'};
const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: ['genre']} as const;
const capabilities: BrowseCapabilities = {library: {...library, defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'genre', labelKey: 'filter.genre', type: 'enum', operators: ['contains-any'], controlHint: 'facet-multi-select', complexity: 'advanced', cost: 'indexed', applicableKinds: ['movie']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000}};
const operationId = '12345678-1234-4123-8123-123456789abc';

async function fixture(value = 'Action') {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const navigation = new NativeShellNavigation(authority, () => authority), listeners = new Set<() => void>();
  navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: library.id}}, authority);
  let active = true, captured = 0;
  const posts: any[] = [], saved: string[] = [];
  const draft = {predicates: [{field: 'genre', operator: 'contains-any', value: [value]}], sort: {field: 'title', direction: 'asc'}};
  const definition = {libraryId: library.id, pivot: 'movies', query: {all: draft.predicates}, sort: [draft.sort], presentation: 'grid'};
  const source = {library, phase: 'ready', tab: {id: 'browse', pivot: 'movies'}, capabilities, defaultSort: draft.sort};
  const destination = {getSnapshot: () => source, captureSavedView: (actual: unknown) => {assert.equal(actual, draft); captured++; return definition;}};
  const context = {current: () => active, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}), libraries: () => [library], navigation,
    subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};},
    api: {request: async <T>(_path: string, method?: string, body?: unknown): Promise<T> => {
      if (method === 'POST') {posts.push(body); return {serverId: 'server', viewerFence: 'actual-viewer', receipt: {operationId, revision: 2, resourceId: 'new-view', deleted: false}} as T;}
      return {serverId: 'server', viewerFence: 'actual-viewer', revision: 1, resources: [], nextCursor: ''} as T;
    }}};
  const {createNativeTVLibraryNaming} = await screenModule(new URL('../src-next/tv/library/save-view.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../packages/client-core/src/presentation/saved-view-summary': {savedViewSummary},
    '../../screens/destinations/lifetime': {nativeDestinationLifetime}, '../../screens/native-screen-services': {nativeScreenServices: () => ({requestId: async () => operationId})},
    '../../screens/library/model': {nativeLibraryCanSave}, '../../screens/library/save-view': {NativeLibrarySaveView}, './keyboard-state': {createNativeTVNameInput},
  });
  const open = () => createNativeTVLibraryNaming(context, destination, draft, defaultI18n, (id: string) => saved.push(id));
  return {open, navigation, authority, posts, saved, draft, source, get captured() {return captured;}, retire: () => {active = false; for (const fn of [...listeners]) fn();}};
}

test('TV naming uses the canonical conditions only, then saves the one real captured definition', async () => {
  const f = await fixture(), naming = f.open();
  try {
    assert.equal(naming.controller.getSnapshot().name, 'Genre Action');
    assert.equal(f.captured, 1); assert.equal(f.posts.length, 0, 'opening performs no mutation');
    assert.equal(naming.input.openKeyboard(), true); naming.input.press('a');
    assert.equal(naming.controller.getSnapshot().name, 'Genre Actiona');
    assert.equal(naming.input.back(), 'keyboard'); assert.equal(naming.controller.getSnapshot().open, true);
    f.draft.predicates = []; // The mounted toolbar can change; this opening keeps its captured definition.
    const pending = naming.controller.save();
    assert.equal(naming.close(), false, 'an accepted pending save keeps its real receipt owner');
    await pending;
    assert.equal(f.posts.length, 1); assert.equal(f.posts[0].name, 'Genre Actiona');
    assert.notEqual(f.posts[0].summary, 'Genre Action. Title A to Z.');
    assert.deepEqual(f.posts[0].definition.query, {all: [{field: 'genre', operator: 'contains-any', value: ['Action']}]});
    assert.deepEqual(f.saved, ['new-view']); assert.equal(naming.controller.getSnapshot().open, false);
  } finally {naming.dispose(); f.navigation.dispose();}
});

test('over-limit suggestions start empty without inventing names', async () => {
  const f = await fixture('x'.repeat(201)), naming = f.open();
  try {assert.equal(naming.controller.getSnapshot().name, ''); assert.equal(f.posts.length, 0);}
  finally {naming.dispose(); f.navigation.dispose();}
});

test('navigation or authority retirement permanently closes the borrowed TV input and mutation', async () => {
  const f = await fixture(), naming = f.open();
  naming.input.openKeyboard();
  f.navigation.open({kind: 'shell', destination: {kind: 'home'}}, f.authority);
  assert.equal(naming.controller.getSnapshot().open, false); assert.equal(naming.input.press('a'), false);
  f.navigation.back(f.authority); await naming.controller.save(); assert.equal(f.posts.length, 0);
  const next = f.open(); f.retire(); assert.equal(next.input.openKeyboard(), false); await next.controller.save();
  assert.equal(f.posts.length, 0); next.dispose(); naming.dispose(); f.navigation.dispose();
});
