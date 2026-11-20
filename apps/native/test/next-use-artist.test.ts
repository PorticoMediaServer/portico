import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import {artistReleaseGroups} from '../../../packages/client-core/src/presentation/library-tabs';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useArtist` is what an artist's page shows on a phone (phone-app frame 15b), from the music controller the old artist
// screen read: this holds what it shows and what each control does.

const t = defaultI18n.t;
const entry = {id: 'ar', kind: 'artist', title: 'The Weeknd', libraryId: 'music', genres: ['R&B/Soul']} as any;
const songs = [{id: 's3', kind: 'song', title: 'Hardest To Love', libraryId: 'music', trackNumber: 3}, {id: 's1', kind: 'song', title: 'Alone Again', libraryId: 'music', trackNumber: 1}] as any[];
const after = {id: 'al', libraryId: 'music', title: 'After Hours', year: 2020, posterUrl: '/ah.jpg', role: 'album'}, single = {id: 'sg', libraryId: 'music', title: 'Blinding Lights', year: 2019, role: 'single'};
const similar = [{id: 'x1', kind: 'artist', title: 'Prince', libraryId: 'music', artworkPaths: ['/p1.jpg', '/p2.jpg']}, {id: 'x2', kind: 'album', title: 'Thriller', libraryId: 'music'}] as any[];

async function artist(over: Record<string, unknown> = {}, form = 'phone') {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  const rows = await screenModule(new URL('../src-next/pages/shared/music/music-rows.ts', import.meta.url), {react: h.react,
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: (model: any) => ({id: model.id, title: model.title, caption: [], detail: []})}});
  const {useArtist} = await screenModule(new URL('../src-next/pages/shared/music/useArtist.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors,
    '../../../../../../packages/client-core/src/presentation/library-tabs': {artistReleaseGroups}, './music-rows': rows,
    '../../../screens/destinations/music/use-playing-item': {usePlayingItem: () => 's3'}, '../../../kit/theme': {useTheme: () => ({form})}});
  const releases = {phase: 'ready', items: [after, single], offset: 0};
  const snapshot = {phase: 'ready', songs, songPage: {phase: 'ready', items: songs, offset: 0}, songTotal: 4, albums: [after, single], releases, similar,
    similarBySound: false, favorite: false, artistDetails: {albumCount: 1}, allSongs: {phase: 'idle', items: [], songs: [], offset: 0}, ...over};
  const entries = new Map<unknown, any>();
  let current = true;
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, play: record('play'), load: record('load'),
    nextReleases: record('next'), previousReleases: record('previous'), retryReleases: record('retry'), refreshReleases: record('refresh'),
    releaseEntry: (row: any) => {if (!entries.has(row)) entries.set(row, {...row, kind: 'album'}); return entries.get(row);}, contains: () => current,
    similarPage: () => undefined, similarCurrent: () => current, toggleFavorite: record('favorite'), loadAllSongs: record('all'), songFavorite: () => false, toggleSongFavorite: record('song-favorite'),
    nextAllSongs: record('all-next'), focusAllSong: (...args: unknown[]) => {calls.push(['all-focus', ...args]);}};
  const picked = {active: false, selected: false, onToggle: () => {}, onEnter: () => {}};
  const view = h.render(() => useArtist({controller, entry, runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined,
    text: {locale: 'en-US'}, reportError: record('error')}, actions: {menu: (...args: unknown[]) => {calls.push(['menu', ...args]);}},
    selection: {subscribe: () => () => {}, getSnapshot: () => 0, item: () => picked}, openEntry: record('open'), onBack: record('back')}));
  return {view, calls, releases, entries, picked, stale: () => {current = false;}};
}

test('an artist shows the circle of their albums, the counts and genre, and the wash from their first album', async () => {
  const {view, calls} = await artist();
  assert.equal(view.title, 'The Weeknd');
  assert.equal(view.initials, 'T');
  assert.deepEqual(view.covers, [{uri: '/ah.jpg'}]);
  assert.deepEqual(view.glow, {url: '/ah.jpg'});
  assert.deepEqual(view.facts.map((fact: any) => fact.text), [t('title.albumCount', {count: 1}), t('title.songCount', {count: 4}), 'R&B/Soul']);
  view.play(); assert.deepEqual(calls.at(-1), ['play']);
  view.shuffle(); assert.deepEqual(calls.at(-1), ['play', undefined, true]);
  view.more(); assert.deepEqual(calls.at(-1), ['menu', entry]);
});

test('Top songs are ranked, the playing one marked; a song plays and its ⋯ opens its menu with Select', async () => {
  const {view, calls, picked} = await artist();
  assert.equal(view.topSongs.title, t('music.topSongs'));
  // Ranked by the server's order, not their album track numbers (frame 15b: 1, 2, 3).
  assert.deepEqual(view.topSongs.rows.map((row: any) => [row.id, row.number, row.playing]), [['s3', 1, true], ['s1', 2, false]]);
  view.topSongs.onPlay('s1'); assert.deepEqual(calls.at(-1), ['play', 's1']);
  const anchor = {current: null};
  view.topSongs.onMore('s3', anchor); assert.deepEqual(calls.at(-1), ['menu', songs[0], anchor, picked.onEnter]);
  assert.equal((await artist({songs: []})).view.topSongs, undefined);
});

test('releases are a row per kind; one opens only while it is still this page\'s, and the server\'s pages page on', async () => {
  const {view, calls, releases, entries, stale} = await artist();
  assert.deepEqual(view.releases.map((row: any) => [row.title, row.shape, row.items.map((item: any) => item.title)]),
    [[t('title.albums'), 'square', ['After Hours']], [t('title.singlesAndEps'), 'square', ['Blinding Lights']]]);
  assert.equal(view.releases[0].items[0].year, '2020');
  view.releases[0].items[0].onNavigate(); assert.deepEqual(calls.at(-1), ['open', entries.get(after)]);
  view.releasePages.next(); assert.deepEqual(calls.at(-1), ['next', releases]);
  stale(); const before = calls.length; view.releases[0].items[0].onNavigate(); assert.equal(calls.length, before, 'a stale release opens nothing');
});

test('similar artists are circles of their covers with the server\'s reason; a failed read offers Try again', async () => {
  const {view, calls} = await artist();
  assert.deepEqual([view.similar.title, view.similar.subtitle, view.similar.shape], [t('music.similarArtists'), t('music.moreLikeThisHint'), 'circle']);
  assert.deepEqual(view.similar.items.map((item: any) => [item.title, item.covers]), [['Prince', [{uri: '/p1.jpg'}, {uri: '/p2.jpg'}]]]);
  view.similar.items[0].onNavigate(); assert.deepEqual(calls.at(-1), ['open', similar[0]]);
  const failed = await artist({phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}});
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['load']);
  assert.equal((await artist({similar: []})).view.similar, undefined);
});

test('TV: the longer facts line, the heart, Details, and All songs read when asked for (tv-more frame 8, the old TV page)', async () => {
  const {view, calls} = await artist({favorite: true, songTotal: 4,
    artistDetails: {albumCount: 1, genres: ['R&B'], country: 'Canada', activeBeginYear: 2009, overview: 'A singer.', overviewSource: 'Wikipedia', artistType: 'Person'}});
  assert.deepEqual(view.factsTV.map((fact: any) => [fact.id, fact.text]), [['genre', 'R&B'], ['country', 'Canada'], ['years', defaultI18n.t('title.activeSince', {year: '2009'})],
    ['count', defaultI18n.t('music.onServer', {albums: 1, songs: 4})]]);
  assert.equal(view.favorite.on, true); view.favorite.toggle(); assert.deepEqual(calls.at(-1), ['favorite']);
  assert.deepEqual(view.details.rows.map((row: any) => row.values[0].text), ['Canada', defaultI18n.t('title.activeSince', {year: '2009'}), 'Person']);
  assert.deepEqual([view.details.overview, view.details.source], ['A singer.', defaultI18n.t('title.fromSource', {source: 'Wikipedia'})]);
  assert.deepEqual(view.releases.map((row: any) => row.group), ['albums', 'singles']);
  assert.equal(view.allSongs.list, undefined, 'All songs is not read until asked for'); view.allSongs.load(); assert.deepEqual(calls.at(-1), ['all']);
  const read = await artist({allSongs: {phase: 'ready', items: songs, songs, offset: 20, total: 40}});
  assert.deepEqual(read.view.allSongs.list.rows.map((row: any) => row.number), songs.map((_song: unknown, index: number) => 21 + index), 'numbered from the page offset');
  assert.equal(read.view.allSongs.total, defaultI18n.t('title.songCount', {count: 40}));
  read.view.allSongs.focus(songs[0].id); assert.equal(read.calls.at(-1)![0], 'all-focus');
});

// A tablet (the old tablet artist page): Top songs as a table with hearts, and Details (genres, country, active years) beside
// the songs; a phone (frame 15b) has neither.
test('a tablet gets the song table\'s hearts and the Details beside the songs; a phone neither', async () => {
  const phone = await artist();
  assert.equal(phone.view.about, undefined); assert.equal(phone.view.topSongs?.onFavorite, undefined);
  const tablet = await artist({}, 'expanded');
  assert.ok(tablet.view.about && tablet.view.about.length > 0, 'Details rows on a tablet');
  assert.equal(typeof tablet.view.topSongs?.onFavorite, 'function');
  assert.equal(tablet.view.topSongs?.rows[0].favorite, false, 'each song carries its heart, on or off');
});
