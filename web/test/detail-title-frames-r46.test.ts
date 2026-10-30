import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import {transformTsx} from './transform-tsx.mjs';

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
const {DetailService} = await import('@core/detail.ts');

const specimenContext = (specimen: ReturnType<typeof createDetailSpecimen>) => ({
  api: specimen.api, events: specimen.events, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
  isCurrent: () => true, artwork: specimen.artwork,
  preferences: {getSnapshot: () => specimen.preferences, subscribe: specimen.subscribePreferences},
  effects: {openEntry() {}, openSettings() {}, async play() {}, reportError(error: unknown) { throw error; }},
});

async function detailData(frame: '2' | '3') {
  const specimen = createDetailSpecimen(frame);
  const service = new DetailService({api: specimen.api, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
    requestId: async () => crypto.randomUUID()});
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
