import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `useAlbum` is what an album's page shows on a phone (phone-app frame 13), from the music controller the old album
// screen read: this holds what it shows and what each control does.

const t = defaultI18n.t;
const artist = {id: 'ar', kind: 'artist', title: 'The Weeknd', libraryId: 'music'} as any;
const entry = {id: 'al', kind: 'album', title: 'After Hours', libraryId: 'music', year: 2020, genres: ['R&B/Soul', 'Pop'], posterUrl: '/ah.jpg', artist: {id: 'ar', name: 'The Weeknd'}} as any;
const songs = [{id: 's1', kind: 'song', title: 'Alone Again', libraryId: 'music', trackNumber: 1, duration: 250},
  {id: 's2', kind: 'song', title: 'Too Late', libraryId: 'music', duration: 239}] as any[];
const similar = [{id: 'x1', kind: 'album', title: 'Thriller', libraryId: 'music', year: 1982, posterUrl: '/th.jpg'}, {id: 'x2', kind: 'artist', title: 'Prince', libraryId: 'music'},
  {id: 'x3', kind: 'album', title: 'No library'}] as any[];

async function album(over: Record<string, unknown> = {}, props: Record<string, unknown> = {}) {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]); return Promise.resolve();};
  // The shared song rows load for real, with the card text they read stubbed.
  const rows = await screenModule(new URL('../src-next/pages/shared/music/music-rows.ts', import.meta.url), {react: h.react,
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: (model: any) => ({id: model.id, title: model.title, caption: [], detail: []})}});
  const {useAlbum} = await screenModule(new URL('../src-next/pages/shared/music/useAlbum.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../../../../packages/client-core/src/presentation/errors': errors,
    './music-rows': rows, '../../../screens/destinations/music/use-playing-item': {usePlayingItem: () => 's2'}});
  const page = {phase: 'ready', items: songs, offset: 0};
  const snapshot = {phase: 'ready', songs, songPage: page, songTotal: 2, songDurationSeconds: 489, albums: [], similar, similarBySound: true, favorite: false,
    albumDetails: {badges: [{variant: 'atmos'}]}, ...over};
  let current = true;
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, artistEntry: () => artist, play: record('play'), load: record('load'),
    nextSongs: record('next'), previousSongs: record('previous'), retrySongs: record('retry'), refreshSongs: record('refresh'),
    focusSong: record('focus'), blurSong: record('blur'), similarPage: () => undefined, similarCurrent: () => current};
  const picked = {active: false, selected: false, onToggle: () => {}, onEnter: () => {}};
  const view = h.render(() => useAlbum({controller, entry, runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined,
    text: {locale: 'en-US'}, reportError: record('error')}, actions: {menu: (...args: unknown[]) => {calls.push(['menu', ...args]);}},
    selection: {subscribe: () => () => {}, getSnapshot: () => 0, item: () => picked}, openEntry: record('open'), onBack: record('back'), download: record('download'), ...props}));
  return {view, calls, page, picked, stale: () => {current = false;}};
}

test('an album shows its year, genre, songs and length, its badges, the artist as a link, and the glow from its cover', async () => {
  const {view, calls} = await album();
  assert.equal(view.title, 'After Hours');
  assert.deepEqual(view.facts.map((fact: any) => [fact.id, fact.text]), [['year', '2020'], ['genre', 'R&B/Soul'], ['songs', t('music.songCountAndDuration', {songs: 2, minutes: 8})]]);
  assert.deepEqual(view.badges, [{variant: 'atmos'}]);
  assert.deepEqual([view.cover, view.glow], [{uri: '/ah.jpg'}, {url: '/ah.jpg'}]);
  view.byline.onPress(); assert.deepEqual(calls.at(-1), ['open', artist]);
  const fromDetails = await album({albumDetails: {artist: {id: 'ar', name: 'The Weeknd'}}}, {entry: {...entry, artist: undefined}});
  assert.equal(fromDetails.view.byline.text, 'The Weeknd', 'the details read names the artist when the list entry does not');
  view.play(); assert.deepEqual(calls.at(-1), ['play']);
  view.shuffle(); assert.deepEqual(calls.at(-1), ['play', undefined, true]);
  view.download(); assert.deepEqual(calls.at(-1), ['download', entry]);
  view.more(); assert.deepEqual(calls.at(-1), ['menu', entry]);
});

test('the songs are numbered from the read (or their place), the playing one marked; a song plays, its ⋯ opens its menu with Select, and focus reaches the pager', async () => {
  const {view, calls, page, picked} = await album();
  assert.deepEqual(view.songs.rows.map((row: any) => [row.id, row.number, row.playing]), [['s1', 1, false], ['s2', 2, true]]);
  view.songs.onPlay('s2'); assert.deepEqual(calls.at(-1), ['play', 's2']);
  const anchor = {current: null};
  view.songs.onMore('s1', anchor); assert.deepEqual(calls.at(-1), ['menu', songs[0], anchor, picked.onEnter]);
  assert.equal(view.songs.select('s1'), picked);
  view.songs.onNearEnd(); assert.deepEqual(calls.at(-1), ['next', page]);
  // The focused song (a keyboard or switch on a phone) is told to the pager, which keeps its page while it holds focus.
  view.songs.onFocusRow('s1'); assert.deepEqual(calls.at(-1), ['focus', songs[0], page]);
  view.songs.onBlurRow('s1'); assert.deepEqual(calls.at(-1), ['blur', songs[0], page]);
  view.songs.previous(); assert.deepEqual(calls.at(-1), ['previous', page]);
});

test('More like this holds only albums, with the server\'s reason, and opens one only while it is still this page\'s row', async () => {
  const {view, calls, stale} = await album();
  assert.equal(view.similar.title, t('next.controls.moreLikeThis'));
  assert.equal(view.similar.subtitle, t('music.moreLikeThisBySound'));
  assert.equal(view.similar.shape, 'square');
  assert.deepEqual(view.similar.items.map((item: any) => [item.key, item.year]), [['x1', '1982']]);
  view.similar.items[0].onNavigate(); assert.deepEqual(calls.at(-1), ['open', similar[0]]);
  stale(); const before = calls.length; view.similar.items[0].onNavigate(); assert.equal(calls.length, before, 'a stale row opens nothing');
  assert.equal((await album({similar: []})).view.similar, undefined);
});

test('a failed read offers Try again; a failed More like this says so; no download, no Download', async () => {
  const failed = await album({phase: 'error', error: {code: 'unavailable', message: 'x', retryable: true}});
  assert.equal(failed.view.state.kind, 'error');
  failed.view.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['load']);
  const similarFailed = await album({similar: [], similarError: {code: 'unavailable', message: 'x', retryable: true}});
  assert.equal(similarFailed.view.similarState.kind, 'error');
  assert.equal((await album({}, {download: undefined})).view.download, undefined);
  assert.equal((await album()).view.state, undefined);
});
