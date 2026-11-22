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
  metadata: {genres: [{id: 'g-crime', name: 'Crime'}], ratings: [{provider: 'community', value: 8.689}], credits: [
    {id: 'c1', personId: 'p1', name: 'Marlon Brando', role: 'Vito', department: 'Acting', provider: ''},
    {id: 'c2', name: 'Francis Ford Coppola', role: 'Director', department: 'Directing', provider: ''}]},
  personal: {watchlisted: false, watched: false, progressSeconds: 0},
  actions: [{id: 'play', enabled: true, playback: {itemId: 'f1'}}, {id: 'watchlist', enabled: true}, {id: 'watched', enabled: true}],
  collections: [{id: 'col', name: 'The Godfather Collection', memberCount: 3, posterUrls: ['/p1.jpg']}], scope: {libraryId: 'movies'},
  related: {rows: []}, ...over});

let episodeSnapshot: Record<string, unknown> = {phase: 'idle'};
async function page(snapshot: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const calls: unknown[][] = [], h = hooks();
  const {useTitle} = await screenModule(new URL('../src-next/pages/shared/title/useTitle.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/client-core/src/presentation/kit-model': kitModel,
    '../../../../../../packages/client-core/src/presentation/kit-model/text': kitText,
    '../../../../../../packages/client-core/src/presentation/errors': errors, '../../../../../../packages/i18n/src': {defaultI18n}});
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({entry: film, retired: false, place: {tab: 'related'}, related: {}, recommendationRows: [], ...snapshot}),
    open: record('open'), play: record('play'), remember: record('remember'), moreCredits: record('moreCredits'), personal: record('personal'), tab: record('tab'), moreRelated: record('moreRelated'), detail: {refresh: record('refresh')},
    episode: {subscribe: () => () => {}, getSnapshot: () => episodeSnapshot, open: record('openEpisode'), close: record('closeEpisode'), play: record('playEpisode'), personal: record('episodePersonal'), retry: record('retryEpisode')},
    workspace: {retry: record('retryWorkspace')}, pageLease: () => 'lease', season: record('season'), group: record('group'), hideWatched: record('hideWatched'), moreSeasons: record('moreSeasons'), moreEpisodes: record('moreEpisodes'), setShowMyList: record('setShowMyList'),
    playSeason: record('playSeason'), markSeason: record('markSeason')};
  const props = {controller, text: {t, locale: 'en-US'}, artwork: (path?: string) => path ? {uri: path} : undefined, ambient: () => undefined,
    back: record('back'), navigate: record('navigate'), more: record('more'), reportError: record('error'), renderError: () => null, aboutLabel: 'About',
    download: record('download'), openCollection: record('collection'), person: record('person'), genre: record('genre'), ...extra};
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
  assert.equal(view.details.people.seeAll.label, t('action.seeAll'));
  assert.deepEqual(view.people.groups.map((group: any[]) => group.map(person => person.name)), [['Marlon Brando'], ['Francis Ford Coppola']]);
  view.details.about.rows.find((row: any) => row.label === t('title.genres')).values[0].onPress();
  assert.deepEqual(calls.at(-1), ['genre', {id: 'g-crime', name: 'Crime'}]);
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

test('See all lists everyone the server has paged so far and reads the next page while there is more', async () => {
  const credits = {credits: [{id: 'c1', personId: 'p1', name: 'Marlon Brando', role: 'Vito', department: 'Acting', provider: ''}, {id: 'c3', name: 'Al Pacino', role: 'Michael', department: 'Acting', provider: ''}],
    hasMore: true, loading: false, waitingId: 'w1'};
  const {view, calls} = await page({detail: {phase: 'ready', data: detail()}, credits});
  assert.deepEqual(view.people.groups[0].map((person: any) => person.name), ['Marlon Brando', 'Al Pacino']);
  view.people.more.onPress(); assert.deepEqual(calls.at(-1), ['moreCredits', credits]);
});

test('a show plays its next episode by name, lists the season, opens an episode in place and picks seasons from the sheet', async () => {
  const show = {id: 's1', kind: 'show', title: 'The Last of Us', libraryId: 'tv'} as any;
  const e1 = {id: 'e1', kind: 'episode', title: 'When You\'re Lost', seasonNumber: 1, episodeNumber: 1, duration: 4800, progressSeconds: 1200, libraryId: 'tv'} as any;
  const e2 = {id: 'e2', kind: 'episode', title: 'Infected', seasonNumber: 1, episodeNumber: 2, duration: 3000, libraryId: 'tv'} as any;
  const workspace = {show: {id: 's1', title: 'The Last of Us', libraryId: 'tv', year: 2023, genres: ['Drama'], overview: 'After a pandemic.', network: 'HBO',
    credits: [{id: 'p9', name: 'Pedro Pascal', role: 'Joel', department: 'Acting', ordinal: 0}]}, nextUp: {...e1, playback: {itemId: 'e1', startSeconds: 1200}},
    selected: {seasonId: 'season-1', group: null}, groups: [], seasonTotalCount: 2};
  episodeSnapshot = {phase: 'ready', entry: e2, data: {item: {overview: 'In 2003.', duration: 3000}, personal: {watchlisted: false, progressSeconds: 0}, actions: [], metadata: {credits: []}, episodeDetails: {previous: {id: 'e1', number: 1, title: 'When You\'re Lost'}}}};
  const {view, calls} = await page({entry: show, workspace: {phase: 'ready', data: workspace}, showState: {watchlisted: false}, episodes: [e1, e2],
    seasons: [{id: 'season-1', number: 1, title: '', episodeCount: 9, watchedCount: 3}, {id: 'season-2', number: 2, title: '', episodeCount: 7}], place: {tab: 'episodes', hideWatched: false}});
  assert.equal(view.primary.label, t('title.resumeEpisode', {code: 'S1 E1'}));
  assert.deepEqual(view.tabs.items.map((item: any) => item.value), ['episodes', 'related', 'details']);
  assert.equal(view.episodes.bar.label, t('title.season', {number: 1}));
  assert.deepEqual(view.episodes.list.items.map((item: any) => item.key), ['e1', 'e2']);
  assert.equal(view.episodes.list.opened.key, 'e2');
  assert.equal(view.episodes.list.opened.previous.title, 'When You\'re Lost');
  view.episodes.list.items[0].onNavigate(); assert.deepEqual(calls.at(-1), ['openEpisode', e1]);
  view.episodes.list.opened.close.onPress(); assert.equal(calls.at(-1)![0], 'closeEpisode');
  assert.deepEqual(view.episodes.sheet.rows.map((row: any) => row.id), ['season-1', 'season-2']);
  // Details reads the show's workspace: its facts and its own credits.
  assert.deepEqual(view.details.about.rows.map((row: any) => row.label), [t('title.genres'), t('title.network')]);
  assert.deepEqual(view.details.people.groups[0].map((person: any) => person.name), ['Pedro Pascal']);
  view.actions[0].onPress(); assert.deepEqual(calls.at(-1), ['setShowMyList', true]);
  // Near the page's end on Episodes, the season's next page (the old screen's scroll paging).
  view.onEnd(); assert.deepEqual(calls.at(-1), ['moreEpisodes']);
  episodeSnapshot = {phase: 'idle'};
});

test('a show reached as an episode offers no Watched on its hero: the episode\'s detail marks only that episode', async () => {
  const episode = {id: 'e1', kind: 'episode', title: 'When You\'re Lost', seasonNumber: 1, episodeNumber: 1, libraryId: 'tv'} as any;
  const workspace = {show: {id: 's1', title: 'The Last of Us', libraryId: 'tv', credits: []}, selected: {seasonId: 'season-1', group: null}, groups: [], seasonTotalCount: 1};
  const {view} = await page({entry: episode, detail: {phase: 'ready', data: detail()}, workspace: {phase: 'ready', data: workspace}, showState: {watchlisted: false}, episodes: [episode],
    seasons: [{id: 'season-1', number: 1, title: '', episodeCount: 1}], place: {tab: 'episodes', hideWatched: false}});
  assert.deepEqual(view.actions.map((action: any) => action.label), [t('entry.addToWatchlist'), t('next.cards.download'), t('card.more', {title: 'The Last of Us'})]);
});

test('on TV the season column chooses a season as focus reaches it, and the band opens the episode in focus', async () => {
  const show = {id: 's1', kind: 'show', title: 'The Last of Us', libraryId: 'tv'} as any;
  const e1 = {id: 'e1', kind: 'episode', title: 'When You\'re Lost', seasonNumber: 1, episodeNumber: 1, duration: 4800, libraryId: 'tv'} as any;
  const workspace = {show: {id: 's1', title: 'The Last of Us', libraryId: 'tv', credits: []}, selected: {seasonId: 'season-1', group: null}, groups: [{id: 'g1', title: 'Episodes 1 to 100', count: 100}], seasonTotalCount: 2};
  const {view, calls} = await page({entry: show, workspace: {phase: 'ready', data: workspace}, showState: {watchlisted: false}, episodes: [e1],
    seasons: [{id: 'season-1', number: 1, title: '', episodeCount: 9, watchedCount: 9, firstAired: '2023-01-15', overview: 'Joel and Ellie.'}, {id: 'season-2', number: 2, title: '', episodeCount: 7}],
    place: {tab: 'episodes', hideWatched: false}});
  const tv = view.episodes.tv;
  assert.deepEqual(tv.seasons.map((season: any) => [season.id, season.current, season.group]), [['season-1', true, false], ['season-2', false, false], ['group:g1', false, true]]);
  assert.deepEqual([tv.seasons[0].year, tv.seasons[0].overview, tv.seasons[0].complete], ['2023', 'Joel and Ellie.', true]);
  // The tiles carry the length alone (the still's badge), and focus enters on the first when nothing is next.
  assert.deepEqual(tv.tiles.map((tile: any) => [tile.key, tile.length]), [['e1', '1 h 20 min']]);
  assert.equal(tv.nextUp, 'e1');
  // The current season asks nothing; another is chosen with the page's lease taken now, a group by its own id.
  const before = calls.length; tv.select('season-1'); assert.equal(calls.length, before);
  tv.select('season-2'); assert.deepEqual(calls.at(-1), ['season', 'season-2', 'lease']);
  tv.select('group:g1'); assert.deepEqual(calls.at(-1), ['group', 'g1', 'lease']);
  // The band: the episode in focus is opened, and closed when focus goes to a season; OK on a tile plays it.
  tv.focusEpisode('e1'); assert.deepEqual(calls.at(-1), ['openEpisode', e1]);
  tv.focusEpisode(undefined); assert.equal(calls.at(-1)![0], 'closeEpisode');
  tv.play('e1'); assert.equal(calls.at(-1)![0], 'play'); assert.equal(calls.at(-1)![1], e1);
  tv.playSeason('season-2'); assert.deepEqual(calls.at(-1), ['playSeason', 'season-2']);
  tv.markSeason('season-1', false); assert.deepEqual(calls.at(-1), ['markSeason', 'season-1', false]);
});

test('a show watched to the end offers Play from its first episode again, not no button', async () => {
  const show = {id: 's1', kind: 'show', title: 'Breaking Bad', libraryId: 'tv'} as any;
  const e1 = {id: 'e1', kind: 'episode', title: 'Pilot', seasonNumber: 1, episodeNumber: 1, duration: 45, watched: true, libraryId: 'tv', playback: {itemId: 'e1', startSeconds: 45}} as any;
  const workspace = {show: {id: 's1', title: 'Breaking Bad', libraryId: 'tv'}, episodes: {sections: [{entries: [e1]}]},
    selected: {seasonId: 'season-1', group: null}, groups: [], seasonTotalCount: 1};
  episodeSnapshot = {phase: 'idle'};
  const {view, calls} = await page({entry: show, workspace: {phase: 'ready', data: workspace}, showState: {watchlisted: false}, episodes: [e1],
    seasons: [{id: 'season-1', number: 1, title: '', episodeCount: 1, watchedCount: 1}], place: {tab: 'episodes', hideWatched: false}});
  assert.equal(view.primary.label, t('title.playEpisode', {code: 'S1 E1'}));
  assert.equal(view.primary.progress, undefined);
  // It names the episode it starts: the controller plays a show only from the server's next episode otherwise.
  view.primary.onPress(); const [name, started, fromStart] = calls.at(-1)!;
  assert.equal(name, 'play'); assert.equal(fromStart, false); assert.equal((started as any).id, 'e1');
  // From its beginning: the watched episode's own start point (its end) would stop at once.
  assert.deepEqual((started as any).playback, {itemId: 'e1', startSeconds: 0});
});
