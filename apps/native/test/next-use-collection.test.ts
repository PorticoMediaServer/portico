import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import * as kitText from '../../../packages/client-core/src/presentation/kit-model/text';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useCollection` is what a collection's page shows (phone-detail d3 frame 8), from the old collection screen's read: this
// holds what it shows and what each control does.

const t = defaultI18n.t;
const fellowship = {id: 'f1', kind: 'movie', title: 'The Fellowship of the Ring', libraryId: 'movies', year: 2001, duration: 10_680, watched: true, genres: ['Fantasy', 'Adventure'], posterUrl: '/f1.jpg', backdropUrl: '/f1-bd.jpg'} as any;
const towers = {id: 'f2', kind: 'movie', title: 'The Two Towers', libraryId: 'movies', year: 2002, genres: ['Fantasy'], duration: 10_740, progressSeconds: 5_370, posterUrl: '/f2.jpg'} as any;

async function collection(over: Record<string, unknown> = {}, form = 'phone') {
  const calls: unknown[][] = [], h = hooks(), scrollers: unknown[][] = [];
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]);};
  const {useCollection} = await screenModule(new URL('../src-next/pages/shared/collection/useCollection.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors,
    '../../../kit/frames': {titleRowsScroller: (props: unknown) => {scrollers.push(['rows', props]); return 'rows';}, posterGridScroller: (props: unknown) => {scrollers.push(['grid', props]); return 'grid';}},
    '../../../kit/theme': {useTheme: () => ({form})},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText,
    '../../../screens/collections/CollectionScreen': {sortLabel: (sort: {id: string}) => sort.id === 'release' ? 'Release order' : 'Title', years: (state: any) => state.complete ? '2001 – 2002' : undefined,
    composedCollectionLede: (state: any) => `${state.totalCount} films`}});
  const state = {phase: 'ready', title: 'The Lord of the Rings', totalCount: 2, first: fellowship, complete: [fellowship, towers], collection: {}, sorts: [
    {id: 'release', labelKey: 'x', directions: ['asc']}, {id: 'title', labelKey: 'y', directions: ['asc']}], sort: 'release', projection: {entity: {origin: 'automatic'}}, ...over};
  const props = {state, focusScope: 'collection:x', runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined},
    actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}, menu: record('menu')},
    selection: {subscribe: () => () => {}, getSnapshot: () => 0, item: () => undefined, start: record('select')},
    onPlay: record('play'), onShuffle: record('shuffle'), onSort: record('sort'), onOpen: record('open'), onBack: record('back'), onRetry: record('retry')};
  return {view: h.render(() => useCollection(props)), calls, scrollers};
}

test('a collection shows its count, years and genres, the automatic note, and its films as rows on a phone', async () => {
  const {view, calls, scrollers} = await collection();
  assert.equal(view.kicker, t('next.home.collection'));
  assert.deepEqual(view.facts.map((fact: any) => fact.text), [t('next.browse.filmCount', {count: 2}), '2001 – 2002', 'Fantasy, Adventure']);
  assert.equal(view.note, t('next.collection.automaticDescription'));
  assert.equal(view.heading, t('next.browse.filmCount', {count: 2}));
  assert.equal(view.scroller, 'rows');
  const item = (scrollers[0]![1] as any).item;
  // Lengths read as on the title pages ("2 h 58 min").
  assert.deepEqual(item(fellowship).facts, ['2001', kitText.durationText(10_680, t)]); assert.equal(item(fellowship).watched, true);
  assert.equal(item(towers).progress, .5); assert.equal(item(towers).remaining, t('next.kit.remaining', {time: kitText.durationText(5_370, t)}));
  item(towers).onNavigate(); assert.deepEqual(calls.at(-1), ['open', towers]);
  view.play(); assert.deepEqual(calls.at(-1), ['play']);
  view.shuffle(); assert.deepEqual(calls.at(-1), ['shuffle']);
});

test('the sort offers the read\'s sorts; the page menu holds Play all, Shuffle and Select; a tablet draws posters and leads with Select', async () => {
  const {view, calls} = await collection();
  // The read's sort keys are not sort names in the catalogue (`sort.title` is "Sort by"): the page names each sort.
  assert.equal(view.sort.label, t('sort.releaseDate'));
  assert.deepEqual(view.sortMenu.items.map((item: any) => item.label), [t('sort.releaseDate'), t('next.browse.native.title')]);
  view.sortMenu.items[1].onSelect(); assert.deepEqual(calls.at(-1), ['sort', 'title', 'asc']);
  assert.deepEqual(view.pageMenu.items.map((item: any) => item.id), ['play', 'shuffle', 'select']);
  assert.equal(view.select, undefined, 'a phone selects from a row\'s menu');
  assert.equal((await collection({sorts: [{id: 'release', labelKey: 'x', directions: ['asc']}]})).view.sort, undefined, 'one sort is no choice');
  const tablet = await collection({}, 'expanded');
  assert.equal(tablet.view.scroller, 'grid');
  tablet.view.select.onPress(); assert.deepEqual(tablet.calls.at(-1), ['select']);
});

test('on TV the films are one row of posters, with the lede under the facts and how many are watched', async () => {
  const {view, calls} = await collection({}, 'tv');
  assert.equal(view.lede, '2 films', 'composed from the films when the read has no overview');
  assert.equal((await collection({projection: {entity: {overview: ' Frodo and the Ring. '}}}, 'tv')).view.lede, 'Frodo and the Ring.');
  assert.equal(view.watched, t('next.browse.watchedOf', {watched: 1, count: 2}));
  assert.equal(view.row.testID, 'collection-row');
  const card = view.row.item(towers); assert.deepEqual([card.title, card.caption, card.progress], ['The Two Towers', '2002', .5]);
  view.row.item(fellowship).onNavigate(); assert.deepEqual(calls.at(-1), ['open', fellowship]);
  assert.equal((await collection({complete: undefined, projection: {entity: {}}}, 'tv')).view.watched, undefined, 'nothing watched to say');
});

test('an empty collection says so; a failed read offers Try again; a hand-made one has no note', async () => {
  assert.equal((await collection({totalCount: 0, complete: []})).view.state.kind, 'empty');
  const failed = await collection({totalCount: 0, error: {code: 'unavailable', message: 'x', retryable: true}});
  assert.equal(failed.view.state.kind, 'error');
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['retry']);
  assert.equal((await collection({projection: {entity: {origin: 'manual'}}})).view.note, undefined);
});
