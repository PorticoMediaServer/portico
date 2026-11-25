import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import * as homeLayoutEditor from '@core/home-layout-editor.ts';
import * as homeCore from '@core/home.ts';
import * as errorsCore from '@core/presentation/errors.ts';
import * as serverTextCore from '@core/presentation/server-text.ts';
import * as browseToolbarCore from '@core/presentation/browse-toolbar.ts';
import * as filterNamesCore from '@core/filter-value-names.ts';
import * as geometry from '../src/kit/layout/geometry.ts';
import * as savedRoute from '../src/screens/saved/route.ts';
import * as savedRuntime from '../src/screens/saved/runtime.ts';
import {createInputCadence} from '../src/screens/browse/input-cadence.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, textOf, tree} from './helpers/interaction-harness.mjs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Customize Home conflict "Keep editing" rebases local draft onto latest revision and allows Save', async () => {
  let revision = 1, saveCalls = 0, lastSaved: {order: readonly string[]; hidden: readonly string[]; expectedRevision: number} | undefined;
  const rows = [
    {id: 'continue', title: 'Continue', reorderable: false, hideable: false, required: true, hidden: false},
    {id: 'recent', title: 'Recently Added', reorderable: true, hideable: true, required: false, hidden: false},
    {id: 'popular', title: 'Popular', reorderable: true, hideable: true, required: false, hidden: false},
  ];
  const service = {
    loadLayout: async () => ({revision, rows, hiddenRowIds: [] as string[]}),
    saveLayout: async (rowOrder: readonly string[], hiddenRowIds: readonly string[], _req: () => string, expectedRevision?: number) => {
      saveCalls++;
      if (expectedRevision !== revision) throw Object.assign(new Error('conflict'), {code: 'home_layout_conflict'});
      lastSaved = {order: rowOrder, hidden: hiddenRowIds, expectedRevision: expectedRevision!};
      revision++;
      return {revision, rows, hiddenRowIds: [...hiddenRowIds]};
    },
    resetLayout: async () => ({revision: ++revision, rows, hiddenRowIds: [] as string[]}),
  };
  let req = 0;
  const controller = new homeLayoutEditor.HomeLayoutEditor(service as never, {requestId: () => `req-${++req}`, isCurrent: () => true});
  await controller.load();
  // User reorders Popular before Recently Added and hides Recently Added.
  controller.move('popular', 1);
  controller.show('recent', false);
  // Another device bumps revision before Save.
  revision = 2;
  assert.equal(await controller.save(), false);
  assert.equal(controller.getSnapshot().conflict, true);

  const h = createRenderer();
  let closed = 0;
  const mod = await componentModule(new URL('../src/screens/home/Customize.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '@core/home.ts': homeCore,
    '@core/home-layout-editor.ts': homeLayoutEditor,
    '@core/presentation/errors.ts': errorsCore,
    '@core/presentation/server-text.ts': serverTextCore,
    '../../kit/controls': {Button: 'Button', Switch: 'Switch'},
    '../../kit/overlays': {Dialog: (props: any) => h.react.createElement('Dialog', null, props.children, props.footer)},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states': {Skeleton: 'Skeleton'},
    '../../shell/viewport': {useShellWidth: () => 1280},
    './customize.module.css': {default: {}},
  });
  const copy = mod.homeCopy(false);
  const flat = () => tree(h.render(() => mod.HomeCustomizeDialog({
    controller, createSavedViews: () => ({}) as never, isCurrent: () => true, copy, onClose: () => { closed++; },
  })));
  const keepBtn = all(flat(), 'Button').find(node => textOf(node) === copy.keepEditing);
  assert.ok(keepBtn, 'Keep editing button renders on conflict');
  await keepBtn.props.onClick();
  for (let i = 0; i < 6; i++) { await Promise.resolve(); flat(); }
  assert.equal(controller.getSnapshot().conflict, false, 'Keep editing resolves conflict against latest revision');
  assert.equal(controller.getSnapshot().draft?.view.revision, 2);
  assert.deepEqual(controller.getSnapshot().draft?.order, ['continue', 'popular', 'recent']);
  assert.equal(controller.getSnapshot().draft?.hidden.has('recent'), true);

  const saveBtn = all(flat(), 'Button').find(node => textOf(node) === defaultI18n.t('action.save'));
  assert.ok(saveBtn);
  assert.equal(saveBtn.props.disabled, false, 'Save button is re-enabled after Keep editing');
  await saveBtn.props.onClick();
  for (let i = 0; i < 6; i++) { await Promise.resolve(); flat(); }
  assert.equal(saveCalls, 2);
  assert.deepEqual(lastSaved, {order: ['popular', 'recent'], hidden: ['recent'], expectedRevision: 2});
  assert.equal(closed, 1);
  controller.dispose();
});

test('Filters only refetches the facet whose query changed and publishes facet error text', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const h = createRenderer();
  const facetCalls: {facet: string; query: string}[] = [];
  let failStudio = false;
  const fields = [
    {id: 'genre', labelKey: 'filter.field.genre', type: 'string', operators: ['contains-any'], controlHint: 'facet-multi-select', facetSource: {field: 'genre'}},
    {id: 'studio', labelKey: 'filter.field.studio', type: 'string', operators: ['in'], controlHint: 'facet-multi-select', facetSource: {field: 'studio'}},
  ];
  const state = {phase: 'ready', predicates: [], queryRevision: 1, capabilities: {library: {kind: 'movie'}, fields}};
  const controller = {
    source: {libraryId: 'lib', pivot: 'movies'},
    subscribe: () => () => {},
    getSnapshot: () => state,
    acceptsQuery: () => true,
    parseNode: (v: unknown) => v,
    setQuery() {},
    facets: async (facet: string, query: string) => {
      facetCalls.push({facet, query});
      if (facet === 'studio' && failStudio) throw new Error('network_down');
      return {values: [{value: `${facet}-1`, label: `${facet} 1`}], total: 1};
    },
  };
  const mod = await componentModule(new URL('../src/screens/browse/Filters.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    '@core/filter-value-names.ts': filterNamesCore,
    '@core/presentation/errors.ts': errorsCore,
    '@core/presentation/browse-toolbar.ts': browseToolbarCore,
    './input-cadence': {createInputCadence},
    '../../kit/browse': {FilterBuilder: 'FilterBuilder'},
  });
  const model = {groups: [{id: 'title', fields}]};
  const labels = {field: (f: any) => f.id, sort: (s: any) => s.id, quick: (q: any) => q.id};
  const render = () => tree(h.render(() => mod.useLibraryFilters({
    controller, model, labels, compact: false, open: true, canSaveView: false, onClose() {}, reportError() {},
  }).children));
  const settle = async () => { for (let i = 0; i < 6; i++) { await Promise.resolve(); render(); } };

  render(); await settle();
  // Open both genre and studio facets.
  one(render(), 'FilterBuilder').props.onFieldOpenChange('genre', true);
  one(render(), 'FilterBuilder').props.onFieldOpenChange('studio', true);
  render();
  context.mock.timers.tick(250); await settle();
  assert.deepEqual(facetCalls, [{facet: 'genre', query: ''}, {facet: 'studio', query: ''}]);

  // Typing in genre only refetches genre, not studio.
  one(render(), 'FilterBuilder').props.onSearchChange('genre', 'sci');
  render();
  context.mock.timers.tick(250); await settle();
  assert.deepEqual(facetCalls, [{facet: 'genre', query: ''}, {facet: 'studio', query: ''}, {facet: 'genre', query: 'sci'}]);

  // A failing facet populates state.error with non-empty error text.
  failStudio = true;
  one(render(), 'FilterBuilder').props.onRetry('studio');
  render();
  context.mock.timers.tick(250); await settle();
  const studioField = one(render(), 'FilterBuilder').props.fields.studio;
  assert.equal(studioField.status, 'error');
  assert.ok(typeof studioField.error === 'string' && studioField.error.length > 0, 'facet error text is populated');
});

test('AlphabetRail safely handles throwing or absent pointer capture', async () => {
  const h = createRenderer();
  const jumped: string[] = [];
  const mod = await componentModule(new URL('../src/kit/layout/AlphabetRail.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    './geometry': geometry,
    './layout.module.css': {default: {}},
  });
  const anchors = [{key: 'A', index: 0}, {key: 'B', index: 10}];
  const nav = one(tree(h.render(() => mod.AlphabetRail({
    anchors, visibleRange: {first: 0, last: 5}, onJump: (a: any) => jumped.push(a.key), platform: 'phone',
  }))), 'nav');
  const throwingTarget = {
    setPointerCapture() { throw new DOMException('No active pointer', 'NotFoundError'); },
    hasPointerCapture: () => false,
    releasePointerCapture() { throw new Error('must not call releasePointerCapture when not captured'); },
  };
  assert.doesNotThrow(() => {
    nav.props.onPointerDown({button: 0, pointerId: 1, clientY: 10, currentTarget: throwingTarget, preventDefault() {}});
    nav.props.onPointerUp({pointerId: 1, currentTarget: throwingTarget});
  });
});

test('SavedScreen omits nested <main>, reacts to phone create_playlist updates, and shows items count', async () => {
  const savedSource = read('../src/screens/saved/Saved.tsx');
  const resourceSource = read('../src/screens/saved/Resource.tsx');
  const savedRouteSource = read('../src/screens/saved/Saved.route.tsx');
  assert.doesNotMatch(savedSource, /<main\b/, 'Saved.tsx never renders a nested <main>');
  assert.doesNotMatch(resourceSource, /<main\b/, 'Resource.tsx never renders a nested <main>');
  assert.doesNotMatch(savedRouteSource, /window\.location\.assign/, 'Saved.route.tsx uses <a download>, not window.location.assign');
  assert.match(savedRouteSource, /anchor\.download =/);
  assert.match(savedSource, /t\('web\.saved\.items', \{count: resource\.entryCount\}\)/);

  // Verify phone "+ New playlist" reacts when runtime.saved publishes create_playlist.
  const h = createRenderer();
  const listeners = new Set<() => void>();
  let savedSnap: any = {phase: 'loading', route: {view: 'playlists'}, projection: null};
  const emptyStore = (value: any) => ({subscribe: () => () => {}, getSnapshot: () => value});
  const runtime = {
    scope: {serverId: 's', viewerId: 'v'},
    operations: {current: true, run: async (fn: any) => fn()},
    select: async () => {},
    saved: {subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); }, getSnapshot: () => savedSnap, revalidate: async () => {}},
    posters: emptyStore({phase: 'idle', route: {view: 'watchlist'}}),
    squares: emptyStore({phase: 'idle', route: {view: 'watchlist'}}),
    personal: emptyStore({phase: 'idle', route: {view: 'collections'}, resources: [], entries: []}),
    summary: {...emptyStore({value: undefined}), refresh: async () => {}},
    downloads: emptyStore({items: []}),
    installReadyDownloads() {},
  };
  const mod = await componentModule(new URL('../src/screens/saved/Saved.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n},
    './route.ts': savedRoute,
    './runtime.ts': savedRuntime,
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Switch: 'Switch'},
    '../../kit/browse': {FiltersButton: 'FiltersButton', SortMenu: 'SortMenu', ViewToggle: 'ViewToggle'},
    '../../kit/frames': {BrowseFrame: (props: any) => h.react.createElement('BrowseFrame', props, props.header?.trailing, props.children)},
    '../../kit/overlays': {MenuRow: 'MenuRow', Popover: 'Popover'},
    '../../kit/cards': {PlaylistMosaic: 'PlaylistMosaic', PosterCard: 'PosterCard', NewPlaylistCard: 'NewPlaylistCard'},
    '../../kit/artwork': {CoverMosaic: 'CoverMosaic'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/layout': {WindowedGrid: 'WindowedGrid', shellBottomClearance: () => 0},
    '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'},
    '../../platform/freshness': {useQuietRefresh() {}},
    '../../shell/viewport': {useShellWidth: () => 390},
    '../home/model': {savedCardModel: () => ({title: ''})},
    '../browse': {BrowseEntryCard: 'BrowseEntryCard'},
    './Downloads': {LocalDownloads: 'LocalDownloads', ServerDownloads: 'ServerDownloads'},
    './Resource': {SavedHistory: 'SavedHistory', SavedResourceScreen: 'SavedResourceScreen'},
    './ResourceFields': {ResourceFields: 'ResourceFields'},
    './ResourceRow': {ResourceRow: 'ResourceRow'},
    './SavedDialog': {SavedDialog: 'SavedDialog'},
    './SavedPhoneDirectory': {SavedPhoneDirectory: 'SavedPhoneDirectory'},
    './use-view-operations.ts': {useSavedViewOperations: () => runtime.operations},
    './saved.module.css': {default: {}},
  });
  const render = () => tree(h.render(() => mod.SavedScreen({
    runtime, route: {view: 'playlists'}, online: true, downloadsOffered: false,
    artwork: {foreground: () => undefined, ambient: () => undefined},
    preparationArtwork: () => undefined,
    presentation: {view: 'grid', cardWidth: 112, backdrops: false, reducedMotion: false, setView() {}},
    effects: {openEntry() {}, playEntry: async () => {}, selectRoute() {}, reportError() {}, openItem() {}, saveFile: async () => {}},
  })));
  assert.equal(all(render(), 'Button').some(b => b.props['aria-label'] === defaultI18n.t('web.saved.newPlaylist')), false);
  savedSnap = {phase: 'ready', route: {view: 'playlists'}, projection: {actions: ['create_playlist'], sorts: [], filters: [], sections: []}};
  for (const fn of listeners) fn();
  assert.equal(all(render(), 'Button').some(b => b.props['aria-label'] === defaultI18n.t('web.saved.newPlaylist')), true);
});

test('targeted Wave 4C source invariants hold across MetadataEditor, Selection editors, Entry, HomeRow, Library, Discovery, Resource, Artist, Collection', () => {
  const metadata = read('../src/screens/metadata/MetadataEditor.tsx');
  assert.match(metadata, /if \(saved\) setDraft\(\{\}\);/, 'MetadataEditor clears draft on save');
  assert.match(metadata, /'undo', 'lock_all', 'unlock_all', 'identify'/, 'MetadataEditor clears draft on server-mutating actions');
  assert.match(metadata, /<Button onClick=\{close\}>\{t\('action\.cancel'\)\}<\/Button>/, 'Cancel uses close()');
  assert.doesNotMatch(metadata, /<label className=\{styles\.field\}/, 'Field does not wrap multiple interactive controls in <label>');
  assert.match(metadata, /presentError\(caught, 'detail'/);

  const sel = read('../src/screens/browse/SelectionEditorSurface.tsx');
  const qsel = read('../src/screens/browse/QuerySelectionEditorSurface.tsx');
  assert.doesNotMatch(sel, /!busy && !error && !receipts\.length/);
  assert.doesNotMatch(qsel, /!busy && !error\)/);

  const entry = read('../src/screens/detail/audiobooks/Entry.tsx');
  assert.doesNotMatch(entry, /leafMore|hasLeafActions/, 'no duplicate more button on chapterless audiobook files');

  const homeRow = read('../src/screens/browse/HomeRow.route.tsx');
  assert.match(homeRow, /homeRowCache = new WeakMap/);
  assert.match(homeRow, /controllerQuery\.sorts\[0\]\?\.field === bestMatch\[0\]\?\.field/);

  const library = read('../src/screens/browse/Library.tsx');
  assert.match(library, /resultTotal=\{resultTotal\}/);
  assert.match(library, /(?:defaultI18n|i18n)\.has\(value\.labelKey\) \? (?:i18n\.)?t\(value\.labelKey\) : value\.labelKey/);

  const discovery = read('../src/screens/browse/Discovery.tsx');
  assert.match(discovery, /if \(action\.selection\?\.active\) \{action\.selection\.onToggle/);

  const resource = read('../src/screens/saved/Resource.tsx');
  assert.match(resource, /className=\{styles\.historyRow\} data-shape=\{square \? 'square' : 'wide'\} onClick=\{open\}/);

  const artist = read('../src/screens/detail/Artist.tsx');
  assert.doesNotMatch(artist, /title\.albumType/);
  assert.match(artist, /filter\.field\.entityKind/);

  const collection = read('../src/screens/detail/Collection.tsx');
  assert.match(collection, /const hasMore = snapshot\.appended\.some/);
  assert.match(collection, /const canReorder = !hasMore && films\.length > 1/);
  assert.match(collection, /total: count/);
});
