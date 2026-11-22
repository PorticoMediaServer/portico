import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as selectionModel from '../../../packages/client-core/src/selection-model';
import * as browseWholeSelection from '../../../packages/client-core/src/browse-whole-selection.ts';

// Select all binds a library's windows to the server's exact whole-view summary, read once someone selects. A summary
// the model refuses (a title's own read no longer matches the server's count) is forgotten and handed back to the caller,
// and a failed read leaves the view one by one once a window has registered without a summary.
const movies = [0, 1, 2].map(index => ({id: `m${index}`, libraryId: 'films', kind: 'movie', title: `Movie ${index}`}));
const every = {play: true, queue: false, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: false, refresh: false, delete: false};

async function fixture(answers: (() => Promise<unknown>)[], options: Record<string, unknown> = {}) {
  const calls: string[] = [];
  const module = await screenModule(new URL('../src-next/screens/library/play-selection.ts', import.meta.url), {react: hooks().react,
    '../../../../../packages/client-core/src/selection-model': {...selectionModel,
      readCatalogueSelectionItem: async (_lifetime: unknown, entry: any) => ({id: entry.id, libraryId: entry.libraryId, kind: entry.kind, capabilities: every})},
    '../../../../../packages/client-core/src/catalogue-query-selection': {playCatalogueQuerySelection: async () => {}},
    '../../../../../packages/client-core/src/browse-whole-selection': browseWholeSelection,
    '../../shell/routes': {nativeDestinationActivation: () => ({getSnapshot: () => ({visible: true})})},
    '../destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true, scope: {}})},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'operation'})},
    '../../shell/freshness': {viewerQueriesFor: () => ({})},
  });
  const collection = {itemAt: (index: number) => movies[index], pageOf: () => 0, subscribePage: () => () => {}};
  const snapshot = {phase: 'ready', revision: 'r1', queryRevision: 1, collection, sort: [], predicates: [], tab: {id: 'movies'}, library: {id: 'films'}};
  const source = {getSnapshot: () => snapshot, subscribe: () => () => {}, contains: () => true,
    captureSavedView: () => ({libraryId: 'films', pivot: 'movies', query: null, sort: []}),
    requestWholeSelection: () => {calls.push('request'); return answers.shift()!();},
    forgetWholeSelection: () => calls.push('forget')};
  const context = {session: () => ({viewer: {serverId: 's', authority: 'local', accountId: 'a', profileId: 'p', role: 'owner'}}),
    playback: {runtime: {cataloguePlayback: () => undefined}, cataloguePlayback: () => undefined},
    navigation: {getSnapshot: () => ({revision: 1}), subscribe: () => () => {}}, subscribeAuthority: () => () => {}, api: {}};
  const producer = module.createNativeLibraryPlaySelection(context, source, {...options, onWholeRefused: () => calls.push('refused')});
  return {producer, calls};
}

const summary = (playCount: number) => ({libraryId: 'films', pivot: 'movies', filter: null, sort: [], catalogRevision: 'r1', total: 3,
  types: [{kind: 'movie', count: 3, libraries: [{libraryId: 'films', count: 3}],
    capabilities: {play: playCount, queue: 0, playlist: 3, myList: 3, watched: 3, collection: 3, favorite: 3, download: 0, edit: 0, refresh: 0, delete: 0}}]});

test('Select all binds every window to the one summary read when someone selects', async () => {
  const {producer, calls} = await fixture([async () => summary(3)]);
  assert.deepEqual(calls, [], 'nothing is read before a window is');
  await producer.prepareWindow('w0', 0, movies.slice(0, 2));
  await producer.prepareWindow('w1', 2, movies.slice(2));
  assert.deepEqual(calls, ['request'], 'one read for the whole view');
  assert.equal(producer.whole()?.total, 3);
  assert.equal(producer.selectWhole(producer.whole()!.id), true);
  assert.equal(producer.model.getSnapshot().target?.mode, 'query');
  producer.dispose();
});

test('a summary the model refuses is forgotten and handed back; the window is not registered', async () => {
  const {producer, calls} = await fixture([async () => summary(0)]);
  await assert.rejects(producer.prepareWindow('w0', 0, movies.slice(0, 2)));
  assert.deepEqual(calls, ['request', 'forget', 'refused']);
  producer.dispose();
});

test('a failed read leaves titles one by one, and once a window registers no later window asks again', async () => {
  const {producer, calls} = await fixture([async () => {throw new Error('offline');}, async () => summary(3)]);
  await producer.prepareWindow('w0', 0, movies.slice(0, 2));
  await producer.prepareWindow('w1', 2, movies.slice(2));
  assert.deepEqual(calls, ['request'], 'a later summary would mix a query into windows that carry none');
  assert.equal(producer.whole(), undefined);
  producer.dispose();
});

test('a view whose summary was refused never asks again', async () => {
  const {producer, calls} = await fixture([], {wholeOffered: false});
  await producer.prepareWindow('w0', 0, movies.slice(0, 2));
  assert.deepEqual(calls, []);
  assert.equal(producer.whole(), undefined);
  producer.dispose();
});

test('library selection rebuilt after a refused summary keeps the grid windows and applies the waiting pick', async () => {
  const tick = () => new Promise<void>(resolve => setImmediate(resolve));
  const host = hooks(), created: any[] = [], toggled: string[] = [];
  const snapshot = {phase: 'ready', library: {id: 'films'}, tab: {id: 'movies'}, revision: 'r1', queryRevision: 0, collection: {}};
  const source = {getSnapshot: () => snapshot, subscribe: () => () => {}, hold: () => () => {}};
  const producerFor = (options: any) => {
    const refused = created.length === 0; created.push(options);
    return {model: {getSnapshot: () => ({active: toggled.length > 0, selectedKeys: []}), subscribe: () => () => {}, clear() {}}, whole: () => undefined, dispose() {},
      prepareWindow: async (_id: string, _first: number, entries: any[]) => {
        if (refused) {options.onWholeRefused(); throw new Error('refused');}
        return {keys: new Map(entries.map(entry => [entry, `key-${entry.id}`])), toggle: (entry: any) => {toggled.push(entry.id);}, dispose() {}};
      }};
  };
  const {createNativeLibrarySelection} = await screenModule(new URL('../src-next/shell/library-selection.tsx', import.meta.url), {
    '../platform/offline-downloads': {}, react: host.react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../../../packages/client-core/src/presentation/errors': {},
    '../../../../packages/client-core/src/catalogue-query-selection': {}, '../../../../packages/client-core/src/selection-model': {selectionFourthAction: () => undefined},
    '../../../../packages/client-core/src/presentation/selection-notice': {selectionDoneNotice: () => ''}, '../../../../packages/client-core/src/media-selection': {},
    './selection-display': {selectedItemsLabel: () => ''}, '../kit/selection': {SelectionBar: 'SelectionBar', SelectionPhoneActionBar: 'SelectionPhoneActionBar'},
    '../kit/theme': {useTheme: () => ({isTV: false})}, '../kit/overlays/OverlayHost': {OverlayPortal: ({children}: any) => children}, '../kit/overlays': {}, '../kit/controls': {},
    '../screens/destinations/lifetime': {nativeDestinationLifetime: () => ({current: () => true})}, '../screens/native-screen-services': {nativeScreenServices: () => ({reportError() {}})},
    '../kit/selection/LoadedDestinationPicker': {}, '../screens/search/loaded-destinations': {}, '../screens/destinations/bulk-media': {},
    '../screens/library/model': {nativeLibraryEntryKey: (entry: any) => entry.id}, '../screens/library/play-selection': {createNativeLibraryPlaySelection: (_context: unknown, _source: unknown, options: any) => producerFor(options)},
    '../screens/library/selection-unavailable-actions': {nativeSelectionUnavailableActions: (actions: unknown) => actions},
  });
  const port = createNativeLibrarySelection({subscribeAuthority: () => () => {}, playback: {subscribe: () => () => {}, getSnapshot: () => ({dock: {}})}} as any,
    source as any, {getSnapshot: () => ({visible: true}), subscribe: () => () => {}} as any);
  try {
    const unregister = port.register(source, movies.slice(0, 2), 0);
    port.getSnapshot().onEnter('m1');
    for (let index = 0; index < 5; index++) await tick();
    assert.equal(created.length, 2, 'rebuilt once');
    assert.equal(created[0].wholeOffered, true); assert.equal(created[1].wholeOffered, false, 'the rebuilt view is one by one');
    assert.deepEqual(toggled, ['m1'], 'the waiting pick is applied to the window carried across the rebuild');
    unregister();
  } finally {port.dispose();}
});
