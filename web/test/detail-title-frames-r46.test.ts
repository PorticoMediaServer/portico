import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement, useContext} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import {transformTsx} from './transform-tsx.mjs';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';
import {queueScope, playbackFence} from '../../packages/player-core/src/index.ts';

const fenceScope = queueScope({serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'});
// As playbackFence() returns for a streamed session: an equal, freshly frozen fence on every read.
const streamedFence = (sessionId = 'session') => playbackFence({kind: 'queue', playback: {scope: fenceScope, run: 1}, sessionId, generation: 1, itemId: 'ep-1', entryId: 'entry', sourceId: 'source'});

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('/shell/viewport.ts')) return {format: 'module', source: 'export const useShellWidth = () => globalThis.__detailWidth.width; export const useShellScrolled = () => false;', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
(globalThis as {__detailWidth?: {width: number}}).__detailWidth = {width: 390};
const width = (value: number) => { (globalThis as {__detailWidth: {width: number}}).__detailWidth.width = value; };

// Artwork pixel sizes are read from the browser global; the server render has no window.
(globalThis as {devicePixelRatio?: number}).devicePixelRatio = 1;

const {TitleActions, TitleDetails} = await import('../src/screens/detail/Detail.tsx');
const {createDetailSpecimen} = await import('../src/dev/entries/detail-specimen.tsx');
const {WatchBodyPlaybackContext, watchEpisodeLine, watchHeaderFacts} = await import('../src/screens/detail/watch-body-playback.tsx');
const {WatchBodyPlaybackProvider} = await import('../src/screens/watch/WatchBodyPlayback.tsx');
const {DetailService} = await import('@core/detail.ts');

const specimenContext = (specimen: ReturnType<typeof createDetailSpecimen>) => ({
  api: specimen.api, events: specimen.events, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
  isCurrent: () => true, artwork: specimen.artwork,
  preferences: {getSnapshot: () => specimen.preferences, subscribe: specimen.subscribePreferences},
  effects: {openEntry() {}, openSettings() {}, async play() {}, reportError(error: unknown) { throw error; }},
});

async function detailData(frame: '2' | '3') {
  const specimen = createDetailSpecimen(frame);
  const service = new DetailService(withQueries({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()}));
  await service.select({libraryId: 'movies', itemId: specimen.itemId});
  const data = service.getSnapshot().data;
  assert.ok(data);
  const binding = {context: specimenContext(specimen), service, readVersions: async () => { throw new Error('not asked'); },
    play: async () => {}, mutate: async () => {}, browseGenre: () => { throw new Error('not asked'); }} as never;
  return {specimen, service, data, binding};
}

const text = (html: string) => html.replace(/<[^>]+>/g, '');

test('frame 2 draws the white Resume button with the long time left and Start over beside it', async () => {
  width(390);
  const {data, binding, service} = await detailData('2');
  try {
    const html = renderToStaticMarkup(createElement(TitleActions, {binding, data}));
    const body = text(html);
    assert.match(body, new RegExp(defaultI18n.t('next.controls.resume')));
    // The drawn caption is the title's own long duration, not the compact "1h 26m left".
    assert.match(body, /1 h 26 min left/);
    assert.match(body, new RegExp(defaultI18n.t('next.controls.startOver')));
  } finally { service.dispose(); }
});

test('phone-player 4: on the watch page the white button follows the video, Pause while it plays and Play when paused', async () => {
  width(390);
  const {data, binding, service} = await detailData('2');
  try {
    let toggles = 0;
    const watch = (playing: boolean) => ({playing, enabled: true, toggle: () => {toggles++;}});
    const draw = (playing: boolean) => text(renderToStaticMarkup(createElement(WatchBodyPlaybackContext.Provider, {value: watch(playing)},
      createElement(TitleActions, {binding, data, watch: watch(playing)}))));
    const playing = draw(true);
    assert.match(playing, new RegExp(defaultI18n.t('next.dock.pause')));
    assert.doesNotMatch(playing, new RegExp(defaultI18n.t('next.controls.resume')), 'never Resume over the video that is playing');
    assert.doesNotMatch(playing, new RegExp(defaultI18n.t('next.controls.startOver')));
    assert.doesNotMatch(playing, /1 h 26 min left/, 'no resume line under it');
    assert.match(draw(false), new RegExp(`^${defaultI18n.t('next.dock.play')}`));
  } finally { service.dispose(); }
});

test('phone-player 4: under the watch page an expanded episode keeps Play or Resume for itself, never the video’s Pause', async () => {
  width(390);
  const {data, binding, service} = await detailData('2');
  try {
    // The watch page provides the playing video; an expanded episode's TitleActions gets no `watch`.
    const html = text(renderToStaticMarkup(createElement(WatchBodyPlaybackContext.Provider, {value: {playing: true, enabled: true, toggle: () => assert.fail('the episode must not pause the video')}},
      createElement(TitleActions, {binding, data}))));
    assert.match(html, new RegExp(`^${defaultI18n.t('next.controls.resume')}`));
    assert.doesNotMatch(html, new RegExp(defaultI18n.t('next.dock.pause')));
    assert.match(html, /1 h 26 min left/, 'its own resume line');
  } finally { service.dispose(); }
});

test('phone-player 4: under the playing video a show names its episode on one line, then year, genres and the time left', () => {
  const env = {t: defaultI18n.t, locale: 'en-US'};
  const episode = {kind: 'video', item: {id: 'ep-1', kind: 'episode', title: 'Failure’s Contagious', show: {title: 'Slow Horses'}, episode: {mode: 'seasonal', season: 1, episode: 1}}} as const;
  assert.deepEqual(watchEpisodeLine(episode, env).map(part => part.text), ['S1 E1', 'Failure’s Contagious']);
  assert.deepEqual(watchEpisodeLine({kind: 'video', item: {id: 'm', kind: 'movie', title: 'Inside Out'}}, env), [], 'a film has no episode line');
  assert.deepEqual(watchEpisodeLine(undefined, env), []);
  const facts = [{id: 'year', role: 'primary', text: '2022'}, {id: 'seasons', role: 'supplement', text: '4 seasons'}, {id: 'genres', role: 'supplement', text: 'Crime, Drama, Mystery'}, {id: 'content-rating', role: 'supplement', text: 'TV-MA'}] as const;
  assert.deepEqual(watchHeaderFacts(facts, 37 * 60, env).map(part => part.text), ['2022', 'Crime, Drama, Mystery', '37 min left']);
  assert.deepEqual(watchHeaderFacts(facts, undefined, env).map(part => part.text), ['2022', 'Crime, Drama, Mystery'], 'no time left without a length');
});

test('phone-player 4: the watch provider hands the header what plays and its time left in the steps the words change', () => {
  const subject = {kind: 'video', item: {id: 'ep-1', kind: 'episode', title: 'Failure’s Contagious'}} as const;
  const seen = (positionSeconds: number, durationSeconds = 2940) => {
    const snapshot = {disposed: false, fence: streamedFence(), presentation: {subject, playing: true, controls: ['pause'], durationSeconds, positionSeconds}};
    let value: any;
    const Probe = () => { value = useContext(WatchBodyPlaybackContext); return null; };
    renderToStaticMarkup(createElement(WatchBodyPlaybackProvider, {runtime: {subscribe: () => () => {}, getSnapshot: () => snapshot, toggle: async () => {}} as never, reportError() {}}, createElement(Probe)));
    return value;
  };
  assert.equal(seen(724).subject, subject);
  assert.equal(seen(724).leftSeconds, 37 * 60, '12:04 of 49:00 reads 37 min left');
  assert.equal(seen(725).leftSeconds, seen(740).leftSeconds, 'no redraw while the minute reads the same');
  assert.equal(seen(2927).leftSeconds, 13, 'under a minute it counts seconds, as the episode row does');
  assert.equal(seen(10, 0).leftSeconds, undefined, 'no length, no time left');
  assert.equal(seen(724).enabled, true);
});

test('#638: a streamed tick with a fresh fence object but the same words keeps the watch context, so the title body does not redraw', async () => {
  const {fenceKey} = await import('../src/shell/playback-model.ts');
  const h = createRenderer();
  const {WatchBodyPlaybackProvider: Provider} = await componentModule(new URL('../src/screens/watch/WatchBodyPlayback.tsx', import.meta.url), {
    react: h.react, '../detail/watch-body-playback': {WatchBodyPlaybackContext: {Provider: 'provider'}}, '../../shell/playback-model': {fenceKey},
  });
  const subject = {kind: 'video', item: {id: 'ep-1', kind: 'episode', title: 'Pilot'}};
  const fenceFor = streamedFence;
  let position = 724, sessionId = 's1';
  const toggled: unknown[] = [];
  const runtime = {subscribe: () => () => {}, toggle: async (fence: unknown) => {toggled.push(fence);},
    getSnapshot: () => ({disposed: false, fence: fenceFor(sessionId), presentation: {subject, playing: true, controls: ['pause'], durationSeconds: 2940, positionSeconds: position}})};
  const reportError = () => {};
  const value = () => {const tree: any = h.render(() => createElement(Provider, {runtime, reportError}, null));return tree.props.value;};
  try {
    const first = value();
    assert.equal(first.leftSeconds, 37 * 60);
    position = 729;
    assert.equal(value(), first, 'same playback, same words: the very same context value (no redraw)');
    position = 760;
    const next = value();
    assert.notEqual(next, first, 'the words changed (36 min left): a new value');
    assert.equal(next.leftSeconds, 36 * 60);
    next.toggle(); await Promise.resolve();
    assert.equal(toggled.length, 1); assert.equal(fenceKey(toggled[0] as never), fenceKey(fenceFor('s1') as never), 'toggles the live fence of this playback');
    sessionId = 's2';
    next.toggle(); await Promise.resolve();
    assert.equal(toggled.length, 1, 'an older value never toggles a different playback');
  } finally {h.dispose();}
});

test('the phone Details draws its short About, the real ratings and the credit lines under the people', async () => {
  width(390);
  const {data, binding, service} = await detailData('2');
  try {
    const body = text(renderToStaticMarkup(createElement(TitleDetails, {binding, data})));
    assert.match(body, /About/);
    assert.match(body, /Ratings/);
    assert.match(body, /TMDB 8\.1/);
    assert.match(body, /Cast &amp; crew/);
    assert.match(body, new RegExp(defaultI18n.t('shelf.seeAll')));
    assert.match(body, /Directed by/);
    assert.match(body, /Pete Docter/);
    assert.match(body, /Written by/);
    assert.match(body, /Meg LeFauve/);
    // The phone hero still carries these facts above, so Details does not repeat them.
    assert.doesNotMatch(body, /Runtime/);
    assert.doesNotMatch(body, /Subtitles/);
    assert.doesNotMatch(body, /films/);
  } finally { service.dispose(); }
});

test('the phone Details draws the film collection as the compact row card', async () => {
  width(390);
  const {data, binding, service} = await detailData('3');
  try {
    const body = text(renderToStaticMarkup(createElement(TitleDetails, {binding, data})));
    assert.match(body, /Collection/);
    assert.match(body, /Dune collection/);
    assert.match(body, /2 titles/);
    assert.doesNotMatch(body, /Part of a collection/);
    assert.doesNotMatch(body, /films/);
  } finally { service.dispose(); }
});

test('frame 3 draws About with Rated and the collection banner with honest member facts', async () => {
  width(1440);
  const {data, binding, service} = await detailData('3');
  try {
    const body = text(renderToStaticMarkup(createElement(TitleDetails, {binding, data})));
    assert.match(body, /About/);
    assert.match(body, /Runtime/);
    assert.match(body, /Rated/);
    assert.match(body, /PG-13/);
    // The producer counts every visible member; it never claims a film-only total.
    assert.match(body, /Part of a collection/);
    assert.match(body, /Dune collection/);
    assert.match(body, /2 titles on your server, from 2021 to 2024\./);
    assert.doesNotMatch(body, /films/);
    assert.match(body, new RegExp(defaultI18n.t('next.home.openCollection')));
    assert.match(body, new RegExp(defaultI18n.t('shelf.seeAllCount', {count: 64}).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  } finally { service.dispose(); }
});

test('the phone route owns its drawn back bar instead of the shell chrome', () => {
  const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
  const route = read('../src/screens/detail/Item.route.tsx'), detail = read('../src/screens/detail/Detail.tsx'), css = read('../src/screens/detail/detail.module.css');
  assert.match(route, /phoneChrome: 'standalone'/);
  assert.match(detail, /standalonePhone/);
  assert.match(detail, /styles\.phoneTopBar/);
  assert.match(css, /\.phoneTopBar/);
});
