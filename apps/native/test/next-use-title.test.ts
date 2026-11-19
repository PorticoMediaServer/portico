import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as kitModel from '../../../packages/client-core/src/presentation/kit-model';
import * as kitText from '../../../packages/client-core/src/presentation/kit-model/text';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useTitle` is what a film's title page shows (the touch and TV pages fill `TitleFrame` from it); this holds what it
// shows and what each control does, for each state the old title screen had.

const t = defaultI18n.t;
const film = {id: 'f1', kind: 'movie', title: 'The Godfather', libraryId: 'movies', year: 1972, duration: 10_000, backdropUrl: '/bd.jpg'} as any;
const detail = (over: Record<string, unknown> = {}) => ({
  item: {title: 'The Godfather', year: 1972, duration: 10_000, overview: 'A family.', backdropUrl: '/bd.jpg', addedAt: '2026-11-18T00:00:00Z'},
  facts: {contentRating: 'R', releaseDate: '1972-03-14', studio: 'Paramount', tagline: 'An offer.'},
  metadata: {genres: [{name: 'Crime'}], ratings: [{provider: 'community', value: 8.689}], credits: [
    {id: 'c1', personId: 'p1', name: 'Marlon Brando', role: 'Vito', department: 'Acting', provider: ''},
    {id: 'c2', name: 'Francis Ford Coppola', role: 'Director', department: 'Directing', provider: ''}]},
  personal: {watchlisted: false, watched: false, progressSeconds: 0},
  actions: [{id: 'play', enabled: true, playback: {itemId: 'f1'}}, {id: 'watchlist', enabled: true}, {id: 'watched', enabled: true}],
  collections: [{id: 'col', name: 'The Godfather Collection', memberCount: 3, posterUrls: ['/p1.jpg']}], scope: {libraryId: 'movies'},
  related: {rows: []}, ...over});

async function page(snapshot: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const calls: unknown[][] = [], h = hooks();
  const {useTitle} = await screenModule(new URL('../src-next/pages/shared/title/useTitle.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText,
    '../../../../../../packages/client-core/src/presentation/errors': errors, '../../../../../../packages/i18n/src': {defaultI18n}});
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({entry: film, retired: false, place: {tab: 'related'}, related: {}, recommendationRows: [], ...snapshot}),
    open: record('open'), play: record('play'), remember: record('remember'), personal: record('personal'), tab: record('tab'), moreRelated: record('moreRelated'), detail: {refresh: record('refresh')}};
  const props = {controller, text: {t, locale: 'en-US'}, artwork: (path?: string) => path ? {uri: path} : undefined, ambient: () => undefined,
    back: record('back'), navigate: record('navigate'), more: record('more'), reportError: record('error'), renderError: () => null, aboutLabel: 'About',
    download: record('download'), openCollection: record('collection'), person: record('person'), ...extra};
  return {view: h.render(() => useTitle(props)), calls};
}

test('a film offers Play, My List, Watched, Download and More, and each does what it says', async () => {
  const {view, calls} = await page({detail: {phase: 'ready', data: detail()}});
  assert.equal(view.state, undefined);
  assert.equal(view.primary.label, t('entry.play'));
  assert.equal(view.primary.progress, undefined);
  assert.deepEqual(view.actions.map((action: any) => action.icon), ['i-plus', 'i-check', 'i-dl', 'i-more']);
  assert.equal(view.actions[1].wide, true, 'Watched is in More on a phone');
  view.primary.onPress(); view.actions[0].onPress(); view.actions[1].onPress(); view.actions[2].onPress(); view.actions[3].onPress();
  assert.deepEqual(calls.map(call => call[0]), ['play', 'personal', 'personal', 'download', 'more']);
  assert.deepEqual(calls[1]![1], {action: 'watchlist', value: true});
  assert.deepEqual(calls[2]![1], {action: 'watched', value: true});
  assert.equal(view.facts.at(-1).id, 'content-rating');
  assert.equal(view.details.about.rows.find((row: any) => row.label === t('title.ratings')).values[0].text, `${t('title.rating.community')} 8.7`);
  assert.equal(view.kicker.label, 'The Godfather Collection');
  view.kicker.onPress(); assert.equal(calls.at(-1)![0], 'collection');
  view.tabs.onValueChange('details'); assert.deepEqual(calls.at(-1), ['tab', 'details']);
  assert.deepEqual(view.details.people.lines.map((line: any) => line.names), ['Francis Ford Coppola']);
  view.details.people.groups[0][0].onPress(); assert.deepEqual(calls.at(-1), ['person', 'p1']);
});

test('a started film offers Resume with the time left and Start over; one not in My List offers to add it', async () => {
  const resumed = detail({personal: {watchlisted: true, watched: false, progressSeconds: 2_500},
    actions: [{id: 'play', enabled: true, playback: {itemId: 'f1', startSeconds: 2_500}}, {id: 'start_over', enabled: true, playback: {itemId: 'f1'}}, {id: 'watchlist', enabled: true}]});
  const {view, calls} = await page({detail: {phase: 'ready', data: resumed}}, {download: undefined});
  assert.equal(view.primary.label, t('entry.resume'));
  assert.equal(view.primary.progress, 0.25);
  assert.ok(view.primary.remaining);
  view.primary.startOver.onPress(); assert.deepEqual(calls.at(-1)!.slice(0, 3), ['play', film, true]);
  assert.deepEqual(view.actions.map((action: any) => action.icon), ['i-check', 'i-more'], 'no Watched or Download offered: neither is drawn');
  assert.equal(view.actions[0].active, true);
});

test('loading, a refusal, a removed title and a failure each draw their page state', async () => {
  assert.deepEqual((await page({detail: {phase: 'loading'}})).view.state, {kind: 'loading'});
  const refused = (await page({detail: {phase: 'error', error: {status: 403}}})).view.state;
  assert.equal(refused.icon, 'i-lock');
  let asked = 0;
  const removed = (await page({detail: {phase: 'error', error: {status: 404}}}, {serverName: 'Portico Demo', search: () => {}, removeContinue: async () => {asked++; return true;}})).view.state;
  assert.equal(removed.icon, 'i-not');
  assert.equal(removed.primaryAction.id, 'search');
  assert.equal(removed.secondaryAction.id, 'remove-continue');
  removed.secondaryAction.onPress(); assert.equal(asked, 1);
  const failed = await page({detail: {phase: 'error', error: new Error('offline')}});
  assert.equal(failed.view.state.primaryAction.id, 'retry');
  failed.view.state.primaryAction.onPress(); assert.equal(failed.calls.at(-1)![0], 'refresh');
});

test('More like this holds the related rows and the server recommendations, each title opening its page', async () => {
  const row = {id: 'r1', heading: t('next.controls.moreLikeThis'), entries: [{id: 'g2', kind: 'movie', title: 'GoodFellas', year: 1990, posterUrl: '/g.jpg'}]};
  const {view, calls} = await page({detail: {phase: 'ready', data: detail()}, recommendationRows: [row]});
  assert.equal(view.related.length, 1);
  assert.equal(view.related[0].title, t('home.row.moreLike', {title: 'The Godfather'}));
  assert.deepEqual(view.related[0].items.map((item: any) => [item.title, item.year]), [['GoodFellas', '1990']]);
  view.related[0].items[0].onNavigate(); assert.equal((calls.at(-1)![1] as any).id, 'g2');
});

test('the page keeps its scroll place, and Play offers the drawn art to the player only while it is drawn', async () => {
  const {view, calls} = await page({detail: {phase: 'ready', data: detail()}, place: {tab: 'related', anchor: {itemId: null, offset: 420}}});
  assert.equal(view.scroll.offset, 420);
  view.scroll.onOffset(600); assert.deepEqual(calls.at(-1), ['remember', {itemId: null, offset: 600}]);
  view.primary.onPress(); assert.equal(calls.at(-1)![3], undefined, 'no art drawn yet: no art offered');
});
