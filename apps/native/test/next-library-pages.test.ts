import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

type Node = {type: unknown; props: Record<string, any>};
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: object, ...children: unknown[]): Node => ({type, props: {...props, children}})}};
const modules = {react, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
  '../../../kit/artwork': {Artwork: 'Artwork', AmbientArt: 'AmbientArt'},
  '../../../kit/browse': {LibraryMenu: 'Menu', LibrarySwitcher: 'Switcher', BrowseToolbar: 'Toolbar', FilteredEmpty: 'FilteredEmpty'},
  '../../../kit/browse/LibraryCard': {LibraryCard: 'Card'}, '../../../kit/browse/HomeRowCard': {HomeRowCard: 'Card'},
  '../../../kit/browse/LibraryListRow': {LibraryListRow: 'ListRow'}, '../../../kit/browse/LibraryShelf': {LibraryShelf: 'Shelf', useLibraryMixAnchor: () => ({})},
  '../../../kit/browse/LibraryFilterFields': {useLibraryFrameFilters: () => ({open: false})}, '../../../kit/browse/LibraryTiles': {LibraryTiles: 'Tiles'},
  '../../../kit/frames/BrowseGrid': {BrowseGrid: 'Grid'}, '../../../kit/frames/useBrowseScroll': {useBrowseScroll: () => ({})},
  '../../../kit/controls': {Button: 'Button', Chip: 'Chip'}, '../../../kit/layout': {AlphabetRail: 'Alphabet'},
  '../../../kit/overlays': {Banner: 'Banner'}, '../../../kit/states': {StateView: 'State'},
};

test('Library keeps its header while loading and pins Filters, Sort and both views when the catalogue arrives', async () => {
  const actions: string[] = [];
  const state = {library: {id: 'films', name: 'Films'}, tab: {shows: 'grid'}, phase: 'loading', discover: {}};
  const view = {state, active: {visible: true}, rows: [], t: defaultI18n.t, openFilters: () => actions.push('filters')};
  const grid = {view: {value: 'list'}, data: {status: 'ready'}, setSort: () => actions.push('sort'), onValueChange: () => actions.push('grid')};
  const {LibraryPage} = await screenModule(new URL('../src-next/pages/touch/library/LibraryPage.tsx', import.meta.url), {
    ...modules, '../../shared/library/useLibrary': {useLibrary: () => view, useLibraryGrid: () => grid},
  });
  const props = {runtime: {}, destination: {}, actions: {}};
  const frame = (LibraryPage(props) as Node).props.children[0] as Node;
  assert.equal(frame.props.header.title, 'Films');
  assert.equal(frame.props.state.kind, 'loading');
  Object.assign(state, {phase: 'ready', collection: {}});
  const catalogue = (LibraryPage(props) as Node).props.children[0] as Node;
  const ready = (catalogue.type as (props: Node['props']) => Node)(catalogue.props);
  const toolbar = ready.props.toolbar.props.children[1];
  toolbar.props.onFilters();
  toolbar.props.onSortChange();
  toolbar.props.view.onValueChange('grid');
  assert.deepEqual(actions, ['filters', 'sort', 'grid']);
  assert.equal(ready.props.grid.props.shape, 'list');
});

test('Libraries offers Edit pins and opens the selected library using the supplied actions', async () => {
  const actions: string[] = [];
  const {LibrariesPage} = await screenModule(new URL('../src-next/pages/touch/library/LibrariesPage.tsx', import.meta.url), {
    ...modules, '../../shared/library/useLibrary': {useLibraryOverview: () => ({t: defaultI18n.t, libraries: [{id: 'films', onNavigate: () => actions.push('films')}]})},
  });
  const frame = LibrariesPage({runtime: {}, editPins: () => actions.push('pins')}) as Node;
  frame.props.header.trailing.props.onPress();
  frame.props.children[0].props.libraries[0].onNavigate();
  assert.deepEqual(actions, ['pins', 'films']);
});

test('See all keeps Back and the group playback actions over the results', async () => {
  const actions: string[] = [];
  const {SeeAllPage} = await screenModule(new URL('../src-next/pages/touch/library/SeeAllPage.tsx', import.meta.url), {
    ...modules, '../../shared/library/useSeeAll': {useSeeAll: () => ({t: defaultI18n.t, title: 'Recently added', ready: true, playable: true, view: 'grid', kinds: []})},
  });
  const frame = SeeAllPage({controller: {collection: {}}, runtime: {}, onBack: () => actions.push('back'), onPlay: (shuffle: boolean) => actions.push(shuffle ? 'shuffle' : 'play')}) as Node;
  assert.equal(frame.props.header.title, 'Recently added');
  frame.props.header.actions.props.children[1].props.onPress();
  frame.props.header.back.onPress();
  assert.deepEqual(actions, ['shuffle', 'back']);
  assert.equal(frame.props.grid.props.shape, 'poster');
});

test('TV Library connects Filters to its grid and jumps from the letter rail beside it, with no Jump to chip', async () => {
  const actions: string[] = [];
  const t = defaultI18n.t, state = {library: {id: 'films', name: 'Films'}, tab: {shows: 'grid'}, collection: {}};
  const view = {state, t, active: {visible: true}, openFilters: () => actions.push('filters'), gridId: 'grid', toolbarId: 'toolbar'};
  const grid = {data: {status: 'ready', positionIndex: {A: 0, M: 40}, total: 80}, range: {first: 0, last: 20},
    region: {id: 'grid', edges: {up: 'toolbar', right: 'grid.alphabet'}}, alphabet: {id: 'grid.alphabet', edges: {left: 'grid'}},
    onJump: (index: number) => actions.push(`jump ${index}`)};
  const {LibraryPage} = await screenModule(new URL('../src-next/pages/tv/library/LibraryPage.tsx', import.meta.url), {
    ...modules, '../../../kit/focus': {FocusItem: 'FocusItem'}, '../../../kit/music/MusicMixesRow': {MusicMixesRow: 'MusicMixesRow'},
    '../../shared/library/useLibrary': {useTVLibrary: () => view, useTVLibraryGrid: () => grid},
  });
  const page = LibraryPage({runtime: {}, destination: {}, actions: {}}) as Node;
  const frame = (page.type as (props: Node['props']) => Node)(page.props);
  const toolbar = frame.props.toolbar.props.children[1];
  toolbar.props.onFilters();
  assert.equal(toolbar.props.onJump, undefined);
  assert.equal(frame.props.alphabet.props.focus.edges.left, 'grid');
  frame.props.alphabet.props.onJump(40);
  assert.deepEqual(actions, ['filters', 'jump 40']);
  assert.equal(frame.props.grid.props.children[0].props.region.edges.right, 'grid.alphabet');
});

test('TV See all starts on Play and provides an on-screen Back action', async () => {
  let back = false;
  const {SeeAllPage} = await screenModule(new URL('../src-next/pages/tv/library/SeeAllPage.tsx', import.meta.url), {
    ...modules, '../../../kit/focus': {FocusItem: 'FocusItem'},
    '../../shared/library/useSeeAll': {useSeeAll: () => ({t: defaultI18n.t, title: 'Recently added', ready: true, playable: true,
      focus: {header: {id: 'row', entry: 'play'}, grid: {id: 'grid'}}})},
  });
  const frame = SeeAllPage({controller: {collection: {}}, runtime: {}, onBack: () => {back = true;}}) as Node;
  assert.equal(frame.props.startFocus, 'play');
  frame.props.header.back.onPress();
  assert.ok(back);
  assert.equal(frame.props.grid.props.region.id, 'grid');
});
