import test from 'node:test';
import assert from 'node:assert/strict';
import {fetchHome} from '../src/home.ts';
import {homeSpotlightModels} from '../src/presentation/home-spotlight.ts';
import {homeEntryCapabilities} from '../src/home-selection.ts';

const state = {watched: false, inList: true, favorite: false, notInterested: false, playCount: 0, updatedAt: '2026-11-12T00:00:00Z'};
const movie = {id: 'itm_film', kind: 'movie', title: 'The film', libraryId: 'lib_films', addedAt: '2026-11-12T00:00:00Z',
  year: 2026, summary: 'A real synopsis.', tags: [{kind: 'genre', name: 'Drama'}], durationMs: 90 * 60_000,
  images: {backdrop: '/v1/images/backdrop'}, available: true, state};
const reason = {kind: 'recent', text: {code: 'home.row.recentlyAdded', fallback: 'Recently added'}};

test('rebuilt Home maps the ordered spotlight, metadata and offered Play/My List with the real show resume target', async () => {
  const episode = {...movie, id: 'itm_episode', kind: 'episode', index: 3, parentIndex: 2, rootId: 'itm_show', state: {...state, positionMs: 65_000}};
  const show = {...movie, id: 'itm_show', kind: 'show', counts: {children: 2, leaves: 20, unwatchedLeaves: 14}};
  const asked: string[] = [];
  const api = {request: async <T>(path: string) => {asked.push(path); return {rows: [], spotlight: [
    {entry: movie, reason, play: true, myList: true}, {entry: show, reason, resumeTarget: episode, play: true, myList: true},
  ]} as T;}};
  const document = await fetchHome(api);
  assert.deepEqual(asked, ['/v1/home'], 'no client detail reads or ranking');
  assert.deepEqual(document.spotlight.map(slide => slide.entry.id), ['itm_film', 'itm_show']);
  const slides = homeSpotlightModels(document);
  assert.deepEqual(slides[0].facts, {year: 2026, genres: ['Drama'], runtimeSeconds: 5400});
  assert.equal(slides[0].artwork.backdropUrl, '/v1/images/backdrop');
  assert.equal(slides[0].overview, movie.summary);
  assert.equal(slides[1].primary?.target.itemId, 'itm_episode');
  assert.equal(slides[1].primary?.target.startSeconds, 65);
  assert.deepEqual(slides[1].primary?.label, {key: 'next.home.resumeEpisode', params: {season: 2, episode: 3}});
  const offers = homeEntryCapabilities(document.spotlight[1].entry, {platform: 'web', playbackAvailable: true});
  assert.equal(offers.playable, true); assert.equal(offers.watchlisted, true);
  assert.equal(offers.editMetadata, false, 'the adapter does not add curation grants');
});

test('an older server with no spotlight has none, and an absent grant does not offer Play', async () => {
  const old = await fetchHome({request: async <T>() => ({rows: []}) as T});
  assert.deepEqual(old.spotlight, []);
  const current = await fetchHome({request: async <T>() => ({rows: [], spotlight: [{entry: movie, reason}]}) as T});
  assert.equal(homeEntryCapabilities(current.spotlight[0].entry, {platform: 'web', playbackAvailable: true}).playable, false);
});

test('a dated show\'s episode code is drawn as a code, never with a thousands separator (Morning Tide S2026 E316)', async () => {
  const {defaultI18n} = await import('../../i18n/src/index.ts');
  for (const key of ['next.home.playEpisode', 'next.home.resumeEpisode', 'next.layout.playEpisode', 'next.watch.wasSeason'] as const)
    assert.doesNotMatch(defaultI18n.t(key, {season: 2026, episode: 1316}), /2,026|1,316/, key);
  assert.equal(defaultI18n.t('next.home.playEpisode', {season: 2026, episode: 316}), 'Play S2026 E316');
});
