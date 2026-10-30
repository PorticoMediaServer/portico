/** Prepared offline for checkpoint execution: actual SearchScreen callback wiring. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {runScreenCommand} from '../src/screens/detail/context.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, tree, textOf} from './helpers/interaction-harness.mjs';
const t = (key: any, values?: any) => defaultI18n.t(key, values);
async function mount(context: any, options: {text?: string; remember?: boolean; failClear?: () => boolean; snapshot?: any; summary?: any[]; group?: string; linkedQuery?: string; liveAvailable?: boolean; onNow?: any; openLive?: boolean; genres?: any; missingArt?: boolean} = {}) {
  context.mock.timers.enable({apis: ['setTimeout', 'setInterval']});
  const h = createRenderer(), calls: {name: string; args: any[]}[] = [];
  let current = true;
  const record = (name: string) => (...args: any[]) => { calls.push({name, args}); return Promise.resolve(); };
  const initial = options.text ?? 'harbor';
  const store = (value: any) => ({subscribe: () => () => {}, getSnapshot: () => value});
  const service = {...store(options.snapshot ?? {phase: 'ready', query: {q: initial}, groups: [], pagination: {cursor: ''}}),
    cancel: record('cancel'), retry: record('retry'), refresh: record('refresh'), next: record('next')};
  const binding = {service, results: {...service, page: () => ({loading: false, error: null})}, session: () => ({text: initial, group: options.group}), summary: () => options.summary ?? [], select: record('select'),
    recent: async () => { calls.push({name: 'recent', args: []}); return {entries: [{q: 'Stored Query'}]}; },
    clearRecent: async () => { calls.push({name: 'clearRecent', args: []}); if (options.failClear?.()) throw new Error('unavailable'); },
    genres: {...store(options.genres ?? {phase: 'ready', genres: []}), load: record('genres'), browseTarget: (genre: any) => ({view: 'genre', id: genre.value})},
    onNow: {...store(options.onNow ?? {phase: 'ready', cards: [], sourceNames: []}), load: record('onNow')},
    context: {isCurrent: () => current, identity: {liveAvailable: options.liveAvailable === true}, effects: {reportError: record('error'), openBrowse: record('openBrowse')}, artwork: {foreground: (path: string) => options.missingArt ? undefined : path},
      preferences: store({effective: {'search.rememberHistory': options.remember !== false, 'appearance.cardSizePercent': 100}})}};
  const identity = await componentModule(new URL('../src/screens/detail/context.ts', import.meta.url), {react: h.react});
  const app = await componentModule(new URL('../src/screens/search/Search.tsx', import.meta.url), {
    react: h.react, '../detail/context': {...identity, runScreenCommand}, '../detail/text': {t, locale: () => 'en-US'},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', TextField: 'TextField'},
    '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile', GenreTile: 'GenreTile', LiveCard: 'LiveCard'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/layout': {WindowedGrid: 'WindowedGrid'},
    '../../kit/states': {Skeleton: 'Skeleton'}, '../../kit/overlays': {Banner: 'Banner'},
    '../../kit/states/SearchEmpty': {SearchEmpty: 'SearchEmpty'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../platform/freshness': {useFreshDocument() {}}, '../../shell/viewport': {useShellWidth: () => 1440},
    '../detail/Loading': {useLoadingClock: () => 0}, '../detail/ScreenProblem': {ScreenProblem: 'ScreenProblem'},
    './RecentSearches': {RecentSearches: 'RecentSearches'}, './TopResult': {TopResult: 'TopResult'}, './AllResultsRow': {AllResultsRow: 'AllResultsRow'},
    './window': {appendedWindow: () => ({collection: {}, set(items: any[]) { calls.push({name: 'window', args: [items]}); }, dispose() {}})},
    './search.module.css': {default: {}},
  });
  const consume = record('consumeLinkedQuery'), openLive = record('openLive');
  const flat = () => tree(h.render(() => app.SearchScreen({binding, linkedQuery: options.linkedQuery, onConsumeLinkedQuery: consume, onOpenLive: options.openLive ? openLive : undefined})));
  const settle = async () => { for (let i = 0; i < 6; i++) { await Promise.resolve(); flat(); } };
  flat(); await settle();
  return {flat, settle, calls, focused: h.focused, retire: () => { current = false; }};
}
const selects = (f: any) => f.calls.filter((call: any) => call.name === 'select');

test('typing cancels old results immediately and debounces the trimmed query without recording', async context => {
  const f = await mount(context);
  one(f.flat(), 'TextField').props.onChange({currentTarget: {value: '  Different Query  '}}); f.flat();
  assert.equal(f.calls.filter(call => call.name === 'cancel').length, 1);
  context.mock.timers.tick(259); await f.settle(); assert.equal(selects(f).length, 0);
  context.mock.timers.tick(1); await f.settle();
  assert.deepEqual(selects(f).map((call: any) => call.args), [['Different Query', {}]]);
});

test('explicit submit records only when history is enabled', async context => {
  const f = await mount(context); let prevented = 0;
  one(f.flat(), 'form').props.onSubmit({preventDefault() { prevented++; }});
  assert.equal(prevented, 1);
  assert.deepEqual(selects(f).at(-1)?.args, ['harbor', {record: true}]);
});

test('disabled history neither loads recent queries nor asks submit to record them', async context => {
  const f = await mount(context, {remember: false});
  one(f.flat(), 'form').props.onSubmit({preventDefault() {}});
  assert.equal(f.calls.some(call => call.name === 'recent'), false);
  assert.deepEqual(selects(f).at(-1)?.args, ['harbor', {}]);
});

test('ArrowDown focuses the first result while other keys preserve their default action', async context => {
  const f = await mount(context); let prevented = 0;
  const field = one(f.flat(), 'TextField');
  field.props.onKeyDown({key: 'ArrowDown', preventDefault() { prevented++; }});
  field.props.onKeyDown({key: 'ArrowUp', preventDefault() { prevented++; }});
  assert.equal(prevented, 1);
  assert.ok(f.focused.includes('first-result'));
});

test('failed Clear keeps real history and reports failure; explicit retry clears both', async context => {
  let failed = true;
  const f = await mount(context, {text: '', failClear: () => failed});
  one(f.flat(), 'RecentSearches').props.onClear(); await f.settle();
  assert.deepEqual(one(f.flat(), 'RecentSearches').props.queries, ['Stored Query']);
  assert.equal(textOf(all(f.flat(), 'p').find(node => node.props.role === 'alert')), t('web.search.clearFailed'));
  failed = false; one(f.flat(), 'RecentSearches').props.onClear(); await f.settle();
  assert.deepEqual(one(f.flat(), 'RecentSearches').props.queries, []);
  assert.equal(all(f.flat(), 'p').some(node => node.props.role === 'alert'), false);
  assert.equal(f.calls.filter(call => call.name === 'clearRecent').length, 2);
});

test('a retired screen does not submit or apply a recent-query selection', async context => {
  const f = await mount(context, {text: ''}); f.retire();
  one(f.flat(), 'RecentSearches').props.onSelect('Stored Query');
  one(f.flat(), 'form').props.onSubmit({preventDefault() {}});
  assert.equal(one(f.flat(), 'TextField').props.value, '');
  assert.equal(selects(f).length, 0);
});


test('choosing a type chip submits that group after debounce; typing resets to All', async context => {
  const f = await mount(context);
  const movies = all(f.flat(), 'Chip').find(node => textOf(node) === t('search.chip.movies')); assert.ok(movies);
  movies.props.onClick(); f.flat();
  context.mock.timers.tick(260); await f.settle();
  assert.deepEqual(selects(f).at(-1)?.args, ['harbor', {group: 'movies'}]);
  one(f.flat(), 'TextField').props.onChange({currentTarget: {value: 'new query'}}); f.flat();
  context.mock.timers.tick(260); await f.settle();
  assert.deepEqual(selects(f).at(-1)?.args, ['new query', {}]);
  assert.equal(all(f.flat(), 'Chip').find(node => textOf(node) === t('search.chip.all'))?.props.selected, true);
});

test('exact zero summary disables its chip; failed and inexact summaries remain honest', async context => {
  const f = await mount(context, {summary: [
    {id: 'movies', status: 'success', totalCountExact: true, totalCount: 0},
    {id: 'shows', status: 'error', totalCountExact: false, totalCount: 0},
    {id: 'episodes', status: 'success', totalCountExact: false, totalCount: 12},
  ]});
  const chip = (label: string) => all(f.flat(), 'Chip').find(node => textOf(node) === t(label));
  assert.equal(chip('search.chip.movies')?.props.disabled, true);
  assert.equal(chip('search.chip.movies')?.props.count, undefined);
  assert.equal(chip('search.chip.shows')?.props.retry, true);
  assert.equal(chip('search.chip.shows')?.props.disabled, false);
  assert.equal(chip('search.chip.episodes')?.props.count, undefined);
});

test('a whole-search error retries through its own service without submitting another query', async context => {
  const error = {code: 'request_failed'};
  const f = await mount(context, {snapshot: {phase: 'error', query: {q: 'harbor'}, groups: [], error, pagination: {cursor: ''}}});
  const problem = one(f.flat(), 'ScreenProblem');
  assert.equal(problem.props.error, error);
  problem.props.retry();
  assert.equal(f.calls.filter(call => call.name === 'retry').length, 1);
  assert.equal(selects(f).length, 0);
});

test('partial group failure uses refresh while unavailable capabilities hide their chip', async context => {
  const groups = [{id: 'movies', status: 'error', items: [], totalCountExact: false, totalCount: 0},
    {id: 'shows', status: 'success', items: [], totalCountExact: true, totalCount: 0}];
  const f = await mount(context, {snapshot: {phase: 'ready', query: {q: 'harbor'}, groups, pagination: {cursor: ''},
    data: {query: {q: 'harbor', group: ''}, capabilities: {groups: [{id: 'shows', available: false}]}}}});
  assert.equal(all(f.flat(), 'Chip').some(node => textOf(node) === t('search.chip.shows')), false);
  const retry = all(f.flat(), 'Button').find(node => node.props.icon === 'i-retry'); assert.ok(retry);
  retry.props.onClick();
  assert.equal(f.calls.filter(call => call.name === 'refresh').length, 1);
  assert.equal(selects(f).length, 0);
});

test('empty-but-continuable All groups keep their pending row in authored order and never show No matches', async context => {
  const groups = [
    {id: 'songs', status: 'success', items: [], totalCount: 20, totalCountExact: true, nextCursor: 'songs-next'},
    {id: 'shows', status: 'success', items: [], totalCount: 0, totalCountExact: true, nextCursor: ''},
    {id: 'movies', status: 'success', items: [], totalCount: 12, totalCountExact: true, nextCursor: 'movies-next'},
  ];
  const f = await mount(context, {snapshot: {generation: 1, phase: 'ready', query: {q: 'harbor'}, groups, pagination: {cursor: ''},
    data: {query: {q: 'harbor', group: ''}, capabilities: {groups: []}}}});
  assert.equal(all(f.flat(), 'SearchEmpty').length, 0);
  assert.deepEqual(all(f.flat(), 'AllResultsRow').map(node => node.props.result), [groups[2], groups[0]]);
  groups[0].nextCursor = '';groups[2].nextCursor = '';
  assert.equal(all(f.flat(), 'AllResultsRow').length, 0);
  assert.equal(all(f.flat(), 'SearchEmpty').length, 1, 'true EOF with no entries is the empty answer');
});


function groupSnapshot(items: any[], cursor = '', nextCursor = 'next-page') {
  return {phase: 'ready', query: {q: 'harbor', group: 'movies'}, pagination: {cursor},
    groups: [{id: 'movies', status: 'success', items, totalCount: 100, totalCountExact: true, nextCursor}],
    data: {query: {q: 'harbor', group: 'movies'}, scope: {viewerFence: 'viewer'}, revision: {catalog: 1}, capabilities: {groups: []}}};
}
const windowItems = (f: any) => f.calls.filter((call: any) => call.name === 'window').at(-1)?.args[0];

test('group paging asks for the selected group only near the end with a ready real cursor', async context => {
  const items = Array.from({length: 40}, (_, id) => ({id: String(id), libraryId: 'library', kind: 'movie'}));
  const snapshot = groupSnapshot(items);
  const f = await mount(context, {group: 'movies', snapshot});
  const grid = one(f.flat(), 'WindowedGrid');
  grid.props.onVisibleRange(0, 19);
  assert.equal(f.calls.some(call => call.name === 'next'), false);
  grid.props.onVisibleRange(0, 20);
  assert.deepEqual(f.calls.filter(call => call.name === 'next').map(call => call.args), [['movies']]);
  snapshot.phase = 'loading'; grid.props.onVisibleRange(0, 39);
  assert.equal(f.calls.filter(call => call.name === 'next').length, 1);
  snapshot.phase = 'ready'; snapshot.groups[0].nextCursor = ''; f.flat();
  one(f.flat(), 'WindowedGrid').props.onVisibleRange(0, 39);
  assert.equal(f.calls.filter(call => call.name === 'next').length, 1);
});

test('continuation appends unseen entry identities and a replacement revision drops old entries', async context => {
  const first = {id: 'same', libraryId: 'first-library', kind: 'movie'};
  const second = {id: 'same', libraryId: 'second-library', kind: 'movie'};
  const snapshot = groupSnapshot([first]);
  const f = await mount(context, {group: 'movies', snapshot});
  assert.deepEqual(windowItems(f), [first]);
  snapshot.pagination.cursor = 'page-2';
  snapshot.groups = [{...snapshot.groups[0], items: [first, second]}];
  f.flat(); await f.settle();
  assert.deepEqual(windowItems(f), [first, second], 'same item ID in another library stays distinct');
  snapshot.data.revision = {catalog: 2};
  snapshot.groups = [{...snapshot.groups[0], items: [second]}];
  f.flat(); await f.settle();
  assert.deepEqual(windowItems(f), [second], 'replacement revision retires prior page entries');
});

test('an empty first-page cursor replaces the existing window even at the same revision', async context => {
  const first = {id: 'first', libraryId: 'library', kind: 'movie'};
  const replacement = {id: 'replacement', libraryId: 'library', kind: 'movie'};
  const snapshot = groupSnapshot([first], 'page-2');
  const f = await mount(context, {group: 'movies', snapshot});
  snapshot.pagination.cursor = '';
  snapshot.groups = [{...snapshot.groups[0], items: [replacement]}];
  f.flat(); await f.settle();
  assert.deepEqual(windowItems(f), [replacement]);
});


test('linked query overrides session text and is consumed once by the mounted screen', async context => {
  const f = await mount(context, {text: 'old query', linkedQuery: 'linked title'});
  assert.equal(one(f.flat(), 'TextField').props.value, 'linked title');
  assert.equal(f.calls.filter(call => call.name === 'consumeLinkedQuery').length, 1);
  context.mock.timers.tick(260); await f.settle();
  assert.deepEqual(selects(f).at(-1)?.args, ['linked title', {}]);
});

const initialLive = {phase: 'ready', sourceNames: ['Real source'], cards: [
  {channel: {sourceId: 'source-one', id: 'channel-one', name: 'One'}, programmes: []},
  {channel: {sourceId: 'source-two', id: 'channel-two', name: 'Two'}, programmes: []},
]};

test('initial On now cards open their actual source while Guide opens the overall guide', async context => {
  const f = await mount(context, {text: '', liveAvailable: true, onNow: initialLive, openLive: true});
  const cards = all(f.flat(), 'LiveCard'); assert.equal(cards.length, 2);
  cards[0].props.onNavigate(); cards[1].props.onNavigate();
  const guide = all(f.flat(), 'Button').find(node => textOf(node) === t('next.controls.guide')); assert.ok(guide);
  assert.equal(guide.props.disabled, false); guide.props.onClick();
  assert.deepEqual(f.calls.filter(call => call.name === 'openLive').map(call => call.args), [['source-one'], ['source-two'], []]);
});

test('On now is absent without live capability even when a retained snapshot has cards', async context => {
  const f = await mount(context, {text: '', onNow: initialLive, openLive: true});
  assert.equal(all(f.flat(), 'LiveCard').length, 0);
  assert.equal(all(f.flat(), 'Button').some(node => textOf(node) === t('next.controls.guide')), false);
});

test('initial Guide is disabled when no navigation callback is supplied', async context => {
  const f = await mount(context, {text: '', liveAvailable: true, onNow: initialLive});
  const guide = all(f.flat(), 'Button').find(node => textOf(node) === t('next.controls.guide')); assert.ok(guide);
  assert.equal(guide.props.disabled, true);
});


test('initial genre tiles keep server labels and open the exact browse target', async context => {
  const genre = {value: 'science-fiction', label: 'Sci-Fi & Fantasy', count: 17, backdrop: {path: '/art/real'}};
  const f = await mount(context, {text: '', genres: {phase: 'ready', genres: [genre]}});
  const tile = one(f.flat(), 'GenreTile');
  assert.equal(tile.props.model.title, genre.label);
  assert.equal(tile.props.artwork, '/art/real');
  tile.props.onNavigate();
  assert.deepEqual(f.calls.filter(call => call.name === 'openBrowse').map(call => call.args), [[{view: 'genre', id: genre.value}]]);
});

test('genres without a backdrop or a resolved artwork never create an empty tile', async context => {
  const f = await mount(context, {text: '', genres: {phase: 'ready', genres: [{value: 'missing', label: 'Missing', count: 1}]}});
  assert.equal(all(f.flat(), 'GenreTile').length, 0);
});

test('unresolved genre artwork omits the tile without inventing an image', async context => {
  const f = await mount(context, {text: '', missingArt: true, genres: {phase: 'ready', genres: [{value: 'genre', label: 'Genre', count: 1, backdrop: {path: '/art/unavailable'}}]}});
  assert.equal(all(f.flat(), 'GenreTile').length, 0);
});

test('blank initial search loads idle genres and live cards once when live is available', async context => {
  const f = await mount(context, {text: '', liveAvailable: true, genres: {phase: 'idle', genres: []}, onNow: {phase: 'idle', cards: [], sourceNames: []}});
  assert.equal(f.calls.filter(call => call.name === 'genres').length, 1);
  assert.equal(f.calls.filter(call => call.name === 'onNow').length, 1);
});

test('a typed query does not load idle initial genre or On now collections', async context => {
  const f = await mount(context, {text: 'harbor', liveAvailable: true, genres: {phase: 'idle', genres: []}, onNow: {phase: 'idle', cards: [], sourceNames: []}});
  assert.equal(f.calls.some(call => ['genres', 'onNow'].includes(call.name)), false);
});
