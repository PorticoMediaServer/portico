import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {parseBrowseCapabilities, type BrowseCapabilities} from '../../../packages/client-core/src/browse';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved';
import {savedViewSummary} from '../../../packages/client-core/src/presentation/saved-view-summary';
import {NativeHomeSavedViewSummaries} from '../src-next/screens/home/saved-view-summaries';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {serverTextLabel} from '../../../packages/client-core/src/presentation/server-text';
import type {HomeLayoutView} from '../../../packages/client-core/src/home';
import {NativeHomeCustomizeController} from '../src-next/screens/home/customize-controller';
import {createHomeRowDrag} from '../src-next/screens/home/customize-drag';
import {alpha} from '../src-next/kit/browse/presentation';
import {createTheme, nativeType} from '../src-next/kit/theme';

const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}], supportedViews: ['grid'], browsable: true} as const;
const capability: BrowseCapabilities = {
  library: {id: 'movies', name: 'Movies', kind: 'movie', defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}], quickFilters: [],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};
const view = (id: string): PersonalResource => ({id, serverId: 'server', viewerFence: 'viewer', kind: 'view', name: id, summary: 'STORED WORDS MUST NEVER BE DRAWN', visibility: 'private', revision: 1,
  role: 'owner', entryCount: 1, actions: ['pin'], status: 'ready', invalidComponents: [], pinned: false, pinRevision: 1, shares: [],
  definition: {libraryId: 'movies', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}});
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

test('Home reads one actual library/pivot vocabulary and renders the shared UI-language result without a Saved write', async () => {
  const requests: unknown[] = [], resources = [view('one'), view('two')], original = JSON.stringify(resources);
  const api: LibraryContentApi = {request: async <T>(path: string, method?: string, body?: unknown) => {requests.push({path, method, body}); return capability as T;}};
  const source = new NativeHomeSavedViewSummaries({api, serverId: 'server', current: () => true, libraryIds: () => ['movies']});
  assert.deepEqual(requests, [], 'constructing the route-local reader performs no I/O');
  assert.equal(source.line(resources[0], defaultI18n), undefined);
  await source.load(resources);
  assert.deepEqual(requests, [{path: '/v1/libraries/movies/browse-capabilities?pivot=movies', method: 'GET', body: undefined}]);
  const expected = savedViewSummary(resources[0].definition!, parseBrowseCapabilities(capability), defaultI18n)!.line;
  assert.equal(source.line(resources[0], defaultI18n), expected);
  assert.equal(source.line(resources[1], defaultI18n), expected);
  assert.notEqual(expected, resources[0].summary);
  await source.load(resources); assert.equal(requests.length, 1);
  const unsupported = {...resources[0], definition: {...resources[0].definition!, query: {not: {field: 'unknown', operator: 'equals' as const, value: true}}}};
  await source.load([unsupported]); assert.equal(source.line(unsupported, defaultI18n), undefined, 'withheld grammar does not fall back to wire summary');
  assert.equal(JSON.stringify(resources), original, 'the actual saved definitions and summary fields are unchanged');
  source.dispose();
});

test('replacing the Saved page aborts old reads and late responses cannot supply words to a new resource or authority', async () => {
  const first = deferred<unknown>(), second = deferred<unknown>(), signals: AbortSignal[] = [];
  let calls = 0, current = true, libraries = ['movies'];
  const api: LibraryContentApi = {request: async <T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {signals.push(signal!); return (++calls === 1 ? first.promise : second.promise) as Promise<T>;}};
  const source = new NativeHomeSavedViewSummaries({api, serverId: 'server', current: () => current, libraryIds: () => libraries});
  const old = view('old'), fresh = view('fresh');
  const oldRead = source.load([old]), freshRead = source.load([fresh]);
  assert.equal(signals[0].aborted, true);
  first.resolve(capability); await oldRead;
  assert.equal(source.line(old, defaultI18n), undefined); assert.equal(source.line(fresh, defaultI18n), undefined);
  second.resolve(capability); await freshRead; assert.ok(source.line(fresh, defaultI18n));
  libraries = []; assert.equal(source.line(fresh, defaultI18n), undefined, 'a library removed from current admission cannot expose an old vocabulary');
  libraries = ['movies']; current = false; assert.equal(source.line(fresh, defaultI18n), undefined);
  source.dispose(); assert.equal(signals[1].aborted, true);
});

test('unadmitted libraries and other server resources start no reads, and a mismatched capability response supplies no words', async () => {
  let calls = 0;
  const source = new NativeHomeSavedViewSummaries({serverId: 'server', current: () => true, libraryIds: () => ['movies'], api: {
    request: async <T>() => {calls++; return {...capability, library: {...capability.library, id: 'another-library'}} as T;},
  }});
  const other = {...view('other'), serverId: 'another-server'}, removed = {...view('removed'), definition: {...view('removed').definition!, libraryId: 'removed'}};
  await source.load([other, removed]); assert.equal(calls, 0);
  const admitted = view('admitted'); await source.load([admitted]); assert.equal(calls, 1);
  assert.equal(source.line(admitted, defaultI18n), undefined);
  source.dispose();
});

for (const isTV of [false, true]) test(`actual ${isTV ? 'TV' : 'phone'} Home chooser draws only computed lines and preserves the selected real resource`, async () => {
  const theme = createTheme({width: isTV ? 1920 : 390, isTV});
  const h = hooks();
  const {HomeSavedViewChoices, HomeCustomize} = await screenModule(new URL('../src-next/screens/home/HomeCustomize.tsx', import.meta.url), {
    '../../platform/tv-key-adapter': {createTVKeyAdapter}, '../../platform/tv-key-source': {tvKeySource: 'native'},
    react: {...h.react, useLayoutEffect: h.react.useEffect},
    'react-native': {ScrollView: 'Scroll', Text: 'Text', View: 'View'}, 'react-native-gesture-handler': {Gesture: {Native: () => ({})}, GestureDetector: 'GestureDetector'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel}, '../../kit/controls': {Focusable: 'Row', Button: 'Button', Switch: 'Switch'}, '../../kit/focus': {useFocusRegistry: () => ({})}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {alpha},
    '../../kit/overlays': {BottomSheet: 'Sheet', SidePanel: 'Panel', Dialog: 'Dialog'}, '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {nativeType, useTheme: () => theme}, './customize-controller': {}, './customize-drag': {createHomeRowDrag},
  });
  const resources = [view('drawn'), view('withheld')], selected: PersonalResource[] = [];
  const tree = HomeSavedViewChoices({views: resources, i18n: defaultI18n, summary: (resource: PersonalResource) => resource === resources[0] ? 'COMPUTED' : undefined, onChoose: (resource: PersonalResource) => selected.push(resource)});
  const text = nodes(tree).filter(node => node.type === 'Text').flatMap(node => node.props.children);
  assert.deepEqual(text, ['drawn', 'COMPUTED', 'withheld']);
  nodes(tree).find(node => node.type === 'Row').props.onPress(); assert.equal(selected[0], resources[0]);
  const layout: HomeLayoutView = {revision: 1, rowOrder: ['continue'], hiddenRowIds: [], rows: [
    {id: 'continue', title: 'Continue', kind: 'continue', artworkShape: 'landscape', required: true, hideable: false, reorderable: true, hidden: false},
  ]};
  let writes = 0;
  const controller = new NativeHomeCustomizeController({current: () => true, requestId: async () => 'request', savedViews: {serverId: 'server', isCurrent: resource => resources.includes(resource)},
    home: {loadLayout: async () => layout, saveLayout: async () => {writes++; return layout;}, resetLayout: async () => {writes++;}}});
  await controller.load(); assert.equal(controller.addSavedView(resources[0]), true);
  const t = defaultI18n.t, copy = {
    title: t('home.customize.action'), instruction: t('next.home.layoutTVInstruction'), alwaysOn: t('home.customize.alwaysOn'), addView: t('home.customize.addView'), reset: t('action.reset'), save: t('action.save'),
    conflictTitle: t('next.home.layoutChangedTitle'), conflictBody: t('next.home.layoutChangedBodyCompact'), reload: t('next.home.reloadLayout'), keepEditing: t('next.home.keepEditing'),
    move: t('next.home.layoutMoveAction'), drop: t('next.home.layoutDropAction'), putBack: t('next.home.layoutPutBackAction'),
  };
  const body = h.render(() => HomeCustomize({controller, copy, i18n: defaultI18n, onClose() {}, onAddView() {}, testID: 'customize', summary: () => 'COMPUTED'}));
  const rowWords = nodes(body).filter(node => node.type === 'Text').flatMap(node => node.props.children);
  assert.ok(rowWords.includes('COMPUTED'), 'the newly chosen transient row uses the same computed-line port');
  assert.equal(rowWords.includes(resources[0].summary), false);
  assert.equal(controller.getSnapshot().rows.at(-1)!.savedView, resources[0]);
  assert.equal(writes, 0, 'rendering the chosen view does not persist a resource or layout');
  controller.dispose();
});
