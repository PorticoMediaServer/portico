import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {readFileSync} from 'node:fs';
import {transformTsx} from './transform-tsx.mjs';
import type {ShowWorkspaceData, WorkspaceShowCredit} from '@core/show-workspace.ts';

// The Show screen's real markup and Modules run here; only CSS Modules and the
// TSX loader are supplied. This is source proof of the drawn frame-4 state, not
// a browser capture or a visual verdict.
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {ShowDetails, ShowSettingsPanel, ShowSettingsMenu, seasonMenuDescription} = await import('../src/screens/detail/Show.tsx');
const {showDetailFacts} = await import('../src/screens/detail/show-bindings.ts');

const credits: readonly WorkspaceShowCredit[] = [
  {id: 'pedro-pascal', name: 'Pedro Pascal', role: 'Joel Miller', department: 'Acting', ordinal: 0},
  {id: 'bella-ramsey', name: 'Bella Ramsey', role: 'Ellie Williams', department: 'Acting', ordinal: 1},
  {id: 'gabriel-luna', name: 'Gabriel Luna', role: 'Tommy Miller', department: 'Acting', ordinal: 2},
  {id: 'isabela-merced', name: 'Isabela Merced', role: 'Dina', department: 'Acting', ordinal: 3},
  {id: 'young-mazino', name: 'Young Mazino', role: 'Jesse', department: 'Acting', ordinal: 4},
  {id: 'kaitlyn-dever', name: 'Kaitlyn Dever', role: 'Abby', department: 'Acting', ordinal: 5},
  {id: 'anna-torv', name: 'Anna Torv', role: 'Tess', department: 'Acting', ordinal: 6},
  {id: 'nick-offerman', name: 'Nick Offerman', role: 'Bill', department: 'Acting', ordinal: 7},
  {id: 'craig-mazin', name: 'Craig Mazin', role: 'Creator', department: 'Writing', ordinal: 8},
  {id: 'neil-druckmann', name: 'Neil Druckmann', role: 'Creator', department: 'Writing', ordinal: 9},
];
const episode = {id: 'ep-1', kind: 'episode', libraryId: 'tv', title: 'When You’re Lost in the Darkness', seasonNumber: 1, episodeNumber: 1,
  airDate: '2023-01-15', duration: 45_000, progressSeconds: 30_205, watched: false,
  navigation: {view: 'item', entityId: 'ep-1'}, playback: {itemId: 'ep-1'}} as const;
const revision = {catalog: 1, viewer: 1};
const data = {
  scope: {serverId: 'specimen-server', libraryId: 'tv', viewerFence: 'specimen-viewer-fence'}, revision,
  show: {id: 'the-last-of-us', libraryId: 'tv', title: 'The Last of Us', year: 2023, libraryKind: 'tv', providerMatchStatus: 'matched',
    overview: 'A hardened survivor takes charge of a 14-year-old girl.', network: 'HBO', studio: 'PlayStation Productions',
    country: 'United States', contentRating: 'TV-MA', status: 'Continuing', firstAired: '2023-01-15', originalLanguage: 'en',
    addedAt: '2026-10-17T12:00:00Z', episodeCount: 4, genres: ['Drama'], credits},
  seasons: [{id: 'season-1', showId: 'the-last-of-us', number: 1, title: 'Season 1', episodeCount: 4, watchedCount: 0, unwatchedCount: 4}],
  seasonTotalCount: 1, nextSeasonCursor: '', groups: [], selected: {seasonId: 'season-1'},
  episodes: {scope: {serverId: 'specimen-server', libraryId: 'tv', libraryKind: 'tv', view: 'season', entityId: 'season-1', viewerFence: 'specimen-viewer-fence'},
    revision, query: {sort: 'episode', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'},
    navigation: [], sorts: [], filters: [],
    sections: [{id: 'episodes', type: 'list', heading: 'Season 1', totalCount: 1, nextCursor: '', entries: [episode]}]},
  nextUp: episode,
  settings: {hideSeasons: false, absoluteNumbering: false, ranges: false, newestFirst: false, revision: 1},
} as unknown as ShowWorkspaceData;
const binding = {
  context: {artwork: {foreground: () => undefined}, effects: {reportError() {}}, isCurrent: () => true},
  current() {}, canManage: () => true,
  setSettings: async () => {},
} as unknown as Parameters<typeof ShowDetails>[0]['binding'];

test('the drawn Details facts come from the stored show record and never from files', () => {
  const facts = showDetailFacts(data.show, data.seasonTotalCount, 'en-US');
  assert.equal(facts.firstAired, '2023-01-15');
  assert.equal(facts.status, 'Continuing');
  assert.equal(facts.network, 'HBO');
  assert.equal(facts.studio, 'PlayStation Productions');
  assert.equal(facts.rating, 'TV-MA');
  assert.equal(facts.language, 'English');
  assert.deepEqual(facts.onServer, {seasons: 1, episodes: 4});
  assert.deepEqual(facts.creators.map(credit => credit.name), ['Craig Mazin', 'Neil Druckmann']);
  assert.equal('files' in facts, false);
  assert.doesNotMatch(JSON.stringify(facts), /path|size|bitrate|resolution|codec/i);
  assert.equal(showDetailFacts({...data.show, originalLanguage: undefined}, 1, 'en-US').language, undefined);
  assert.equal(showDetailFacts(data.show, 0, 'en-US').onServer, undefined);
});

test('the drawn season rows carry the stored years, episode count and real watched progress', () => {
  const render = (season: Parameters<typeof seasonMenuDescription>[0]) => renderToStaticMarkup(createElement('div', null, seasonMenuDescription(season)));
  assert.equal(render({id: 's3', showId: 'the-simpsons', number: 3, title: 'Season 3', episodeCount: 24,
    watchedCount: 4, unwatchedCount: 20, firstAired: '1991-09-19', lastAired: '1992-05-14'}),
  '<div><span class="seasonDescription"><span>1991 to 1992</span><span>24 episodes</span><span>4 of 24 watched</span></span></div>');
  assert.equal(render({id: 's1', showId: 'the-simpsons', number: 1, title: 'Season 1', episodeCount: 13,
    watchedCount: 13, unwatchedCount: 0, firstAired: '1989-12-17', lastAired: '1990-05-13'}),
  '<div><span class="seasonDescription"><span>1989 to 1990</span><span>13 episodes</span><span>13 of 13 watched</span></span></div>');
  assert.equal(render({id: 'sp', showId: 'the-simpsons', number: 0, title: 'Specials', episodeCount: 12,
    watchedCount: 0, unwatchedCount: 12}), '<div><span class="seasonDescription"><span>12 episodes</span><span>0 of 12 watched</span></span></div>');
  assert.equal(seasonMenuDescription({id: 's2', showId: 'the-simpsons', number: 2, title: 'Season 2'}), undefined);
});

test('frame 4 renders the About facts, the stored cast and creators, and the owner Server panel', () => {
  const html = renderToStaticMarkup(createElement(ShowDetails, {binding, data, owner: true}));
  assert.match(html, /About</);
  for (const label of ['First aired', 'Status', 'Network', 'Studio', 'Country', 'Genres', 'Created by', 'Rated', 'Language', 'On this server', 'Added']) assert.match(html, new RegExp(`<dt>${label}</dt>`));
  assert.match(html, /January 15, 2023/);
  assert.match(html, /October 17, 2026/);
  assert.match(html, /Season 1, 4 episodes/);
  assert.match(html, /Cast and crew/);
  assert.match(html, /See all 10/);
  assert.match(html, /Pedro Pascal/);
  assert.match(html, /Craig Mazin/);
  assert.match(html, /Show settings/);
  assert.match(html, /Owner only/);
  assert.match(html, /Episode display/);
  assert.match(html, />Change</);
  assert.doesNotMatch(html, /File info|File details|\/srv\/|Mbps|GB/);
});

test('the owner Episode display menu opens the four stored settings with a real episode preview', () => {
  const panel = renderToStaticMarkup(createElement(ShowSettingsPanel, {binding, data}));
  assert.match(panel, /Members never see this\./);
  assert.match(panel, /By season/);
  assert.match(panel, /Oldest first/);
  assert.match(panel, /Episode display/);
  assert.match(panel, />Change</);
  const html = renderToStaticMarkup(createElement(ShowSettingsMenu, {binding, data}));
  for (const label of ['Group episodes', 'Number them', 'Order', 'By season', 'In one list', 'Ranges of 100', 'Across seasons', 'Oldest first', 'Newest first']) assert.match(html, new RegExp(label));
  assert.match(html, /For everyone/);
  assert.match(html, /Reads as/);
  assert.match(html, /S1 E1/);
  assert.match(html, /When You’re Lost in the Darkness/);
});

test('a non-owner sees no Server panel and no settings command', () => {
  const html = renderToStaticMarkup(createElement(ShowDetails, {binding, data, owner: false}));
  assert.doesNotMatch(html, /Show settings|Owner only|Episode display|Change/);
  assert.match(html, /Cast and crew/);
});

test('the long-running show specimen keeps the drawn 36-season frame facts', () => {
  const entry = readFileSync(new URL('../src/dev/entries/show.tsx', import.meta.url), 'utf8');
  assert.match(entry, /frame !== '6'/);
  assert.match(entry, /seasonTotalCount: 36/);
  assert.match(entry, /watchedCount: 4, unwatchedCount: 20/);
  assert.match(entry, /data-show-frame6-ready/);
  assert.match(entry, /FreshnessProvider/);
});
