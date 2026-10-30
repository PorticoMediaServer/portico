import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';
import {NativeShellNavigation} from '../src-next/shell/routes';
import * as registryPort from '../src-next/shell/route-registry.ts';
import {authorityKey} from '../src-next/shell/model.ts';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import {NativeSearchDestination} from '../src-next/screens/search/model.ts';
import {NativeSearchExplore, NativeSearchGenreList} from '../src-next/screens/search/explore.ts';
import {NativeLoadedDestinations} from '../src-next/screens/search/loaded-destinations.ts';
import {createNativeReadingSearch} from '../src-next/screens/search/reading-search.ts';
import {NativeBulkMedia} from '../src-next/screens/destinations/bulk-media.ts';
import * as navigationPorts from '../src-next/screens/search/navigation.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {mediaSelectionKey} from '../../../packages/client-core/src/media-selection';
import * as selectionCore from '../../../packages/client-core/src/selection-model';
import {selectedItemsLabel} from '../src-next/shell/selection-display';
import {hooks, screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
type Element = {type: any; props: Record<string, any>};

/** The TV entry must load the real Search registration: the rail's Search
 * destination and its genre routes are shell destinations, not phone-only. */
function find(node: any, type: any): Element | undefined {
  if (Array.isArray(node)) {for (const child of node) {const hit = find(child, type); if (hit) return hit;} return undefined;}
  if (!node || typeof node !== 'object' || !('props' in node)) return undefined;
  if (node.type === type) return node;
  return find(node.props.children, type);
}
/** Expand the module's retained hosts through the real activation ports. */
function expand(node: any): any {
  if (Array.isArray(node)) return node.map(expand);
  if (!node || typeof node !== 'object' || !('props' in node)) return node;
  if (typeof node.type === 'function') return expand(node.type(node.props));
  return {...node, props: {...node.props, children: expand(node.props.children)}};
}

function tvPreferences(remember: boolean) {
  return parsePreferenceSnapshot({deviceClass: 'television', registryRevision: 'actual-registry', registry: {revision: 'actual-registry', fields: [
    {key: 'search.rememberHistory', type: 'boolean', default: false, group: 'privacy', labelKey: 'preferences.search.rememberHistory', scopes: ['profile-server']},
  ]}, documents: [
    {scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {'search.rememberHistory': remember}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}},
  ], effective: {'search.rememberHistory': remember}, effectiveSource: {'search.rememberHistory': 'profile-server'}, clampedFields: []});
}
const art = (id: string) => ({kind: 'item', id, path: `/v1/metadata/item/${id}/art/backdrop?v=${'a'.repeat(64)}&w=1920`});
const searchPage = (url: URL) => ({
  scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 3, viewer: 7},
  query: {q: url.searchParams.get('q'), group: url.searchParams.get('group') || '', groups: ['movies'], limit: 40, sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
  capabilities: {groups: [{id: 'movies', title: '', available: true, reason: '', sorts: ['relevance']}], sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
  groups: [{id: 'movies', entityKind: 'movie', title: '', status: 'success', errorCode: '', totalCount: 1, totalCountExact: true, nextCursor: '',
    items: [{id: 'actual-film', kind: 'movie', libraryId: 'movies', title: 'Stored title', navigation: {view: 'item', entityId: 'actual-film'}}]}],
});
const history = {serverId: 'server', viewerFence: 'actual-history-fence', maxEntries: 8,
  entries: [{q: 'actual recent search', updatedAt: '2026-10-23T00:00:00Z', uses: 1}]};

async function setup() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-only'};
  const navigation = new NativeShellNavigation(authority, () => authority), authorityListeners = new Set<() => void>(), preferenceListeners = new Set<() => void>();
  const paths: string[] = [], errors: unknown[] = [], appStateListeners = new Set<(state: string) => void>();
  const AppState = {currentState: 'active', addEventListener: (_event: string, listener: (state: string) => void) => {appStateListeners.add(listener); return {remove: () => appStateListeners.delete(listener)};}};
  let current = true;
  const session: LocalSession = {viewer: {...authority, authority: 'local', role: 'member'}, accessToken: '', expiresAt: '2026-10-25T00:00:00Z',
    authorizationHorizon: '2026-10-26T00:00:00Z', sessionFamilyId: 'fixture-family', tokenGeneration: '1'};
  const unavailable = (): never => {throw new Error('unexpected_tv_search_port');};
  const context: NativeShellBootstrapContext = {
    current: () => current, session: () => session, authority: () => authority, navigation,
    api: new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
      const url = new URL(String(input)); paths.push(url.pathname + url.search);
      const data = url.pathname === '/v1/search' ? searchPage(url) : url.pathname.includes('/history') ? history
        : {queryId:'genre_query',revision: 'opaque', hasGenres: true, genres: [{value: 'drama', label: 'Drama', count: 20, covers: [], backdrop: art('drama')}]};
      assert.equal(init?.method ?? 'GET', 'GET');
      return new Response(JSON.stringify(data), {status: 200, headers: {'Content-Type': 'application/json'}});
    }),
    subscribeAuthority: listener => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    preferences: () => tvPreferences(true), subscribePreferences: listener => {preferenceListeners.add(listener); return () => {preferenceListeners.delete(listener);};},
    get home() {return unavailable();}, get events() {return unavailable();}, get appVersion() {return unavailable();}, get serverName() {return unavailable();},
    get playback() {return unavailable();}, get watch() {return unavailable();}, get account() {return unavailable();}, libraries: () => [],
  };
  const {createNativeSearchController} = await screenModule(new URL('../src-next/screens/search/controller.ts', import.meta.url), {
    react: {default: {}}, '../destinations/lifetime': {nativeDestinationLifetime}, './navigation': navigationPorts, './model': {NativeSearchDestination},
    '../native-screen-services': {nativeScreenServices: () => ({reportError: (error: unknown) => errors.push(error)})},
  });
  // Only the surface each explore was constructed for is recorded; everything
  // else is the real producer the module binds.
  class RecordingExplore extends NativeSearchExplore {
    readonly surface: string;
    constructor(options: ConstructorParameters<typeof NativeSearchExplore>[0]) {super(options); this.surface = options.surface;}
  }
  const engine = await screenModule(new URL('../src-next/screens/selection/native-selection.tsx', import.meta.url), {
    react: hooks().react, 'react-native': {View: 'View', Text: 'Text'},
    'react-native-safe-area-context': {useSafeAreaInsets: unavailable},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/selection-model': selectionCore,
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src/media-selection': {mediaSelectionKey},
    '../../kit/overlays': {Dialog: 'Dialog'}, '../../kit/controls': {Button: 'Button'},
    '../../shell/selection-display': {selectedItemsLabel}, '../../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'SelectionPhoneActionBar',
      BulkEditor: 'BulkEditor', DeleteMediaDialog: 'DeleteMediaDialog'},
    '../../kit/theme': {useTheme: unavailable}, '../destinations/lifetime': {nativeDestinationLifetime},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: unavailable, reportError: (error: unknown) => errors.push(error)})},
    // The selection owner uses these real controllers; the picker dialog itself is a drawn host and
    // its own focus/identity coverage is next-shell-tv-overlay-ids.
    '../search/loaded-destinations': {NativeLoadedDestinations},
    '../destinations/bulk-media': {NativeBulkMedia},
    '../destinations/DestinationPicker': {NativeDestinationPicker: 'NativeDestinationPicker'},
  });
  const {createNativeSearchSelection} = await screenModule(new URL('../src-next/screens/search/selection.ts', import.meta.url), {
    react: hooks().react, '../selection/native-selection': engine,
  });
  const registry = registryPort;
  // The reader's Search-all-books route is registered too; it is a TV-refused route,
  // so its real factory is registered and never opened here.
  const readingRegister = await screenModule(new URL('../src-next/screens/search/reading-register.tsx', import.meta.url), {...stackSlotFixture, 
    react: hooks().react, 'react-native': {Platform: {isTV: true}, View: 'View'},
    '../destinations/lifetime': {nativeDestinationLifetime},
    './reading-search': {createNativeReadingSearch}, './ReadingSearchScreen': {ReadingSearchScreen: 'ReadingSearchScreen'},
  });
  await screenModule(new URL('../src-next/screens/search/register.tsx', import.meta.url), {...stackSlotFixture, 
    react: hooks().react, 'react-native': {AppState, Platform: {isTV: true}, View: 'View'}, '../../shell/route-registry': registry,
    '../../shell/model': {authorityKey},
    '../destinations/lifetime': {nativeDestinationLifetime},
    '../destinations/entry-actions': {createNativeEntryActions: () => ({dispose() {}, menuState: {subscribe: () => () => {}, getSnapshot: () => 0}})},
    './selection': {createNativeSearchSelection},
    './controller': {createNativeSearchController}, './actions': {createNativeSearchActions: () => ({dispose() {}})},
    './navigation': navigationPorts, './explore': {NativeSearchExplore: RecordingExplore, NativeSearchGenreList},
    './SearchScreen': {SearchScreen: 'SearchScreen'}, './GenreBrowseScreen': {GenreBrowseScreen: 'GenreBrowseScreen'},
    './AllGenresScreen': {AllGenresScreen: 'AllGenresScreen'},
    './reading-register': readingRegister,
  });
  return {context, navigation, authority, paths, errors, authorityListeners, preferenceListeners, appStateListeners,
    retire: () => {current = false; for (const listener of [...authorityListeners]) listener();},
  };
}

test('the TV entry registers the real Search module against the TV surface, with no private substitute', async () => {
  // The platform entry, not App.tsx's handset-only block, must load this module.
  const entry = await readFile(new URL('../src-next/tv/register.ts', import.meta.url), 'utf8');
  assert.match(entry, /import '\.\.\/screens\/search\/register';/, 'the TV entry imports the real Search registration');
  const s = await setup(), states = hooks(), runtime = {} as never;
  let bound: ReturnType<typeof registryPort.nativeShellRoutes.bind> | undefined;
  try {
    assert.equal(s.navigation.open({kind: 'shell', destination: {kind: 'search'}}, s.authority), true);
    const root = s.navigation.getSnapshot().route;
    bound = registryPort.nativeShellRoutes.bind(s.context);
    assert.equal(bound.render(root, runtime), null);
    const tree = states.render(() => expand(bound!.renderRetained(runtime)));
    const screen = find(tree, 'SearchScreen') as Element | undefined;
    assert.ok(screen, 'the bound registry renders the real Search host');
    assert.equal(screen.props.explore.surface, 'tv', 'genre prefixes follow the TV surface');
    assert.deepEqual(screen.props.tvCopy, {preferToTalk: 'next.search.preferToTalk', voiceHint: 'next.search.remoteMicrophoneHelp'});
    assert.equal(s.paths.length, 0, 'registration and binding issue no eager read');

    const destination = screen.props.destination, activation = screen.props.activation;
    assert.equal(activation.getSnapshot().visible, true);
    destination.setText('arrived query');
    await destination.commit(); await tick();
    assert.ok(s.paths.some(path => path.startsWith('/v1/search?')), 'the arrived query performs a real Search read');
    const arrived = destination.getSnapshot().search;
    assert.equal(arrived.phase, 'ready');
    assert.equal(destination.getSnapshot().groups[0].items[0].id, 'actual-film');
    assert.equal(s.navigation.open({kind: 'title', entry: {id: 'actual-film', kind: 'movie', libraryId: 'movies', title: 'Stored title'}}, s.authority), true);
    assert.equal(activation.getSnapshot().visible, false);
    const read = s.paths.length;
    destination.setText('hidden'); await destination.commit(); await tick();
    assert.equal(s.paths.length, read, 'a retained but hidden page stops issuing reads');
    assert.equal(s.navigation.back(s.authority), true);
    assert.equal(activation.getSnapshot().visible, true);
    assert.equal(activation.getSnapshot().intent, 'restore');
    assert.equal(destination.getSnapshot().text, 'arrived query');
    assert.equal(destination.getSnapshot().search.data, arrived.data);
    assert.equal(s.paths.length, read, 'Back restores the arrived page without another request');

    assert.equal(navigationPorts.openNativeSearchGenres(s.context), true);
    const genres = states.render(() => expand(bound!.renderRetained(runtime)));
    const all = find(genres, 'AllGenresScreen') as Element | undefined;
    assert.ok(all, 'search-genres registers at the TV entry');
    assert.notEqual(all.props.explore, screen.props.explore, 'All genres owns its independent directory');
    assert.ok(all.props.explore instanceof NativeSearchGenreList);
    assert.equal(s.navigation.open({kind: 'search-genre', value: 'drama', label: 'Drama'}, s.authority), true);
    const genre = states.render(() => expand(bound!.renderRetained(runtime)));
    const browse = find(genre, 'GenreBrowseScreen') as Element | undefined;
    assert.ok(browse, 'search-genre registers at the TV entry');
    assert.equal(browse.props.value, 'drama');

    s.retire();
    assert.equal(activation.getSnapshot().visible, false);
    s.paths.length = 0;
    destination.setText('retired'); await destination.commit(); await tick();
    assert.equal(s.paths.length, 0, 'a retired authority cannot read through held references');
    assert.deepEqual(s.errors, []);
    assert.equal(s.preferenceListeners.size, 0);
  } finally {
    bound?.dispose(); s.navigation.dispose();
  }
  assert.equal(s.authorityListeners.size, 0); assert.equal(s.appStateListeners.size, 0);
});
