import assert from 'node:assert/strict';
import test from 'node:test';
import type {SearchGroup} from '../../../packages/client-core/src/search';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
const movie = {id: 'movie', kind: 'movie', title: 'Stored Movie', libraryId: 'movies', navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}};
const person = {id: 'person-without-library', kind: 'person', title: 'Actual Person', navigation: {view: 'person', entityId: 'person-without-library'}};
const channel = {id: 'actual-channel', kind: 'channel', title: 'Actual Channel', navigation: {view: 'channel', entityId: 'actual-channel',
  channelId: 'actual-channel', sourceId: 'actual-guide-source', provenance: 'live-source'}};
const groups = [{id: 'movies', entityKind: 'movie', items: [movie]}, {id: 'people', entityKind: 'person', items: [person]}, {id: 'live-tv', entityKind: 'channel', items: [channel]}];

/** The actual focus module's exports, loaded from their own sources after the
 * standard-home React boundary is registered. The shallow card render below sits
 * under no FocusItem, so it reads the genuine default value but never runs the
 * focus hook's lifetime; this fixture does not claim a mounted focus host. */
async function focusNamespace(react: any) {
  const [{createTheme}, registry, hostRef] = await Promise.all([
    import('../src-next/kit/theme.ts'), import('../src-next/kit/focus/registry.ts'), import('../src-next/kit/focus/useHostRef.ts')]);
  const theme = createTheme({width: 390, isTV: false});
  return screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {react: {...react, createContext: (value: any) => ({value}), forwardRef: (render: any) => render},
    'react-native': {findNodeHandle: () => undefined, TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => theme},
    './registry': registry, './useHostRef': hostRef});
}

async function fixture(mount = false) {
  let beforeQueue: ((signal: AbortSignal) => Promise<void>) | undefined;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string; method: string; signal: AbortSignal}, f: {pin: {serverId: string}}) => {
    const url = new URL(request.path, 'https://fixture.invalid');
    if (url.pathname === '/v1/queues' && request.method === 'POST') {await beforeQueue?.(request.signal); return;}
    if (url.pathname !== '/v1/search') return;
    return {status: 200, body: {
      scope: {serverId: f.pin.serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 12, viewer: 7},
      query: {q: url.searchParams.get('q'), group: url.searchParams.get('group') || '', groups: groups.map(group => group.id), limit: 40,
        sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
      capabilities: {groups: groups.map(group => ({id: group.id, title: '', available: true, reason: '', sorts: ['relevance']})),
        sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
      groups: groups.map(group => ({...group, title: '', status: 'success', errorCode: '', totalCount: group.items.length, totalCountExact: true, nextCursor: ''})),
    }};
  }});
  let runtime: RestoredNativeShellRuntime | undefined, source: ReturnType<typeof import('../src-next/screens/search/controller').createNativeSearchController> | undefined;
  let actions: ReturnType<typeof import('../src-next/screens/search/actions').createNativeSearchActions> | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {createNativeSearchActions} = await import('../src-next/screens/search/actions');
    const {createNativeSearchController} = await import('../src-next/screens/search/controller');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    context.navigation.open({kind: 'shell', destination: {kind: 'search'}}, context.authority());
    const target = context.navigation.getSnapshot().route;
    source = createNativeSearchController(context, target);
    actions = createNativeSearchActions(context, source.source, source.activation);
    if (mount) await app.mount({runtime});
    source.source.setText('stored'); await source.source.commit();
    const item = (group: SearchGroup) => source!.source.getSnapshot().groups.find(value => value.id === group)!.items[0]!;
    return {app, context, runtime, source, actions, target, item, queue: (value: typeof beforeQueue) => {beforeQueue = value;},
      close: async () => {await app.unmount(); actions?.dispose(); source?.dispose(); runtime?.dispose(); app.disposeLoader();}};
  } catch (error) {await app.unmount(); actions?.dispose(); source?.dispose(); runtime?.dispose(); app.disposeLoader(); throw error;}
}

test('Search actions preserve exact current media/Person/channel identities without a fabricated library or source', async () => {
  const f = await fixture();
  try {
    const person = f.item('people'), channel = f.item('live-tv'), movie = f.item('movies');
    const before = f.context.navigation.getSnapshot();
    f.actions.open({...person}, 'people'); f.actions.open(person, 'movies'); assert.equal(f.context.navigation.getSnapshot(), before);
    assert.equal(f.actions.canMenu(person, 'people'), false); assert.equal(f.actions.canMenu(channel, 'live-tv'), false); assert.equal(f.actions.canMenu(movie, 'movies'), true);
    const host = hooks();
    const {homeCardText} = await import('../src-next/screens/home/model');
    const {nativeSearchEntryKey} = await import('../src-next/screens/search/model');
    const {SearchGridCard} = await screenModule(new URL('../src-next/screens/search/SearchGridCard.tsx', import.meta.url), {
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame', ArtFrame: 'ArtFrame'},
    '../../kit/browse': {SongRow: 'SongRow'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => ({isTV: false})},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({caption: [], detail: []})},
      react: host.react, 'react-native': {View: 'View'},
      '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile'},
      '../../kit/focus': await focusNamespace(host.react),
      '../home/model': {homeCardText}, './model': {nativeSearchEntryKey},
    });
    const personCard = host.render(() => SearchGridCard({entry: person, group: 'people', runtime: f.runtime, actions: f.actions, width: 120}));
    const movieCard = host.render(() => SearchGridCard({entry: movie, group: 'movies', runtime: f.runtime, actions: f.actions, width: 120}));
    assert.equal(personCard.props.onMore, undefined, 'a non-media entity receives no fake media-menu command');
    assert.equal(typeof movieCard.props.onMore, 'function');

    f.actions.open(person, 'people'); assert.deepEqual(f.context.navigation.getSnapshot().route, {kind: 'person', personId: person.navigation!.entityId});
    assert.equal('libraryId' in person, false); f.context.navigation.back(f.context.authority());
    f.actions.open(channel, 'live-tv'); assert.deepEqual(f.context.navigation.getSnapshot().route, {kind: 'channel', sourceId: 'actual-guide-source', channelId: 'actual-channel'});
    f.context.navigation.back(f.context.authority());
    f.actions.open(movie, 'movies'); const title = f.context.navigation.getSnapshot().route;
    assert.equal(title.kind, 'title'); if (title.kind === 'title') assert.equal(title.entry, movie);
    f.context.navigation.back(f.context.authority());
    f.actions.dispose(); const retired = f.context.navigation.getSnapshot(); f.actions.open(person, 'people'); assert.equal(f.context.navigation.getSnapshot(), retired);
    assert.equal(f.source.source.getSnapshot().search.phase, 'ready', 'actions never dispose the borrowed Search source');
  } finally {await f.close();}
});

test('same arrived Search page after pause cannot revive pending Play; a new genuine gesture uses the same runtime', async () => {
  const f = await fixture(true), begun = deferred(), release = deferred();
  let signal: AbortSignal | undefined;
  try {
    const movie = f.item('movies'), before = f.source.source.getSnapshot().search;
    f.queue(async value => {signal = value; begun.resolve(); await release.promise;});
    f.actions.play(movie, 'movies'); await begun.promise;
    f.source.source.pause(); f.source.source.resume();
    const after = f.source.source.getSnapshot().search;
    assert.equal(f.item('movies'), movie); assert.equal(after.data?.revision.catalog, before.data?.revision.catalog);
    assert.notEqual(after.generation, before.generation); assert.equal(signal!.aborted, true, 'original generation retires despite exact resident object and unchanged nav');
    f.queue(undefined); release.resolve(); await f.app.settle();
    assert.equal(f.context.playback.runtime.getSnapshot().fence, undefined, 'late old queue cannot adopt');
    f.actions.play(movie, 'movies'); await f.app.settle();
    assert.ok(f.context.playback.runtime.getSnapshot().fence, 'new current gesture admits actual native playback without an old output fence');
    const accepted = f.context.playback.runtime.getSnapshot().fence;
    f.actions.dispose(); await f.app.settle();
    assert.equal(f.context.playback.runtime.getSnapshot().fence, accepted, 'settled accepted playback is not stopped when its caller retires');
  } finally {release.resolve(); await f.close();}
});
