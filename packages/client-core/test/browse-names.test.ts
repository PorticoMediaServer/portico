import test from 'node:test';
import assert from 'node:assert/strict';
import {browseNameId, decadePresets, orderFilterValues, sortDirectionKind, toolbarModel} from '../src/presentation/browse-toolbar.ts';
import {defaultI18n} from '../../i18n/src/index.ts';
import type {BrowseCapabilities, BrowseFieldCapability} from '../src/browse.ts';

test('expanded Year values follow the drawn oldest-to-newest order without changing counts or other facets', () => {
  const year: BrowseFieldCapability = {id: 'year', labelKey: 'browse.field.year', type: 'number', operators: ['in'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']};
  const values = Object.freeze([2020, 1980, 2000, 1950, 2010, 1970, 1990].map((value, count) => Object.freeze({value: String(value), count, countIsLowerBound: count === 0})));
  const ordered = orderFilterValues(year, values);
  assert.deepEqual(ordered.map(v => v.value), ['1950', '1970', '1980', '1990', '2000', '2010', '2020']);
  assert.deepEqual(values.map(v => v.value), ['2020', '1980', '2000', '1950', '2010', '1970', '1990']);
  for (const v of ordered) assert.equal(v, values.find(original => original.value === v.value));
  assert.equal(orderFilterValues({...year, id: 'genre'}, values), values);
  assert.deepEqual(decadePresets(1978, 2004).map(v => [v.label, v.from, v.to]), [['1970s', 1970, 1979], ['1980s', 1980, 1989], ['1990s', 1990, 1999], ['2000s', 2000, 2009]]);
});

const has = (id: string) => defaultI18n.has(id);
const name = (id: string, labelKey?: string) => { const key = browseNameId(id, labelKey, has); return key ? defaultI18n.t(key as never) : undefined; };

test('every filter and sort the server publishes has a catalogue name, never its id spelled out', () => {
  const fields: [string, string][] = [['entityKind', 'browse.field.entityKind'], ['search', 'browse.field.search'], ['title', 'browse.field.title'], ['year', 'browse.field.year'], ['decade', 'browse.field.decade'], ['releaseDate', 'browse.field.releaseDate'], ['dateAdded', 'browse.field.dateAdded'], ['playState', 'browse.field.playState'], ['favorite', 'browse.field.favorite'], ['watchlisted', 'browse.field.watchlisted'], ['personalRating', 'browse.field.personalRating'], ['lastPlayedAt', 'browse.field.lastPlayedAt'], ['genre', 'browse.field.genre'], ['tag', 'browse.field.tag'], ['label', 'browse.field.label'], ['collection', 'browse.field.collection'], ['contentRating', 'browse.field.contentRating'], ['communityRating', 'browse.field.communityRating'], ['criticRating', 'browse.field.criticRating'], ['availability', 'browse.field.availability'], ['resolution', 'browse.field.resolution'], ['dynamicRange', 'browse.field.dynamicRange'], ['audioLanguage', 'browse.field.audioLanguage'], ['studio', 'browse.field.studio'], ['network', 'browse.field.network'], ['actor', 'browse.field.actor'], ['director', 'browse.field.director'], ['writer', 'browse.field.writer'], ['durationMs', 'browse.field.durationSeconds'], ['artist', 'browse.field.artist'], ['album', 'browse.field.album'], ['author', 'browse.field.author'], ['narrator', 'browse.field.narrator'], ['series', 'browse.field.series'], ['subtitleLanguage', 'browse.field.subtitleLanguage'], ['versions', 'browse.field.versions'], ['match', 'browse.field.match']];
  const sorts: [string, string][] = [['title', 'sort.title'], ['added', 'sort.added'], ['year', 'sort.year'], ['duration', 'sort.duration'], ['communityRating', 'sort.communityRating'], ['personalRating', 'sort.personalRating'], ['forYou', 'sort.forYou'], ['aired', 'sort.aired'], ['latestAired', 'sort.aired'], ['lastPlayed', 'sort.lastPlayed'], ['releaseDate', 'sort.releaseDate'], ['criticRating', 'sort.criticRating'], ['episodeCount', 'sort.episodeCount'], ['album', 'sort.album'], ['artist', 'sort.artist'], ['trackNumber', 'sort.trackNumber'], ['fileSize', 'sort.fileSize'], ['resolution', 'sort.resolution'], ['random', 'sort.random'], ['playCount', 'sort.playCount'], ['count', 'sort.count'], ['recentActivity', 'sort.recentActivity']];
  for (const [id, key] of [...fields, ...sorts]) assert.ok(name(id, key), `${id} (${key}) has no name`);
});

test('names read as people say them', () => {
  assert.equal(name('durationMs', 'browse.field.durationSeconds'), 'Duration');
  assert.equal(name('personalRating', 'sort.personalRating'), 'Your rating');
  assert.equal(name('lastPlayedAt', 'browse.field.lastPlayedAt'), 'Last played');
  assert.equal(name('communityRating', 'sort.communityRating'), 'Audience rating');
  assert.equal(name('added', 'sort.added'), 'Date added');
  // A show's own sort borrows the episode sort's key; its own name wins.
  assert.equal(name('latestAired', 'sort.aired'), 'Latest episode');
  assert.equal(name('aired', 'sort.aired'), 'Air date');
  // The Sort menu's heading is never a sort's name.
  assert.equal(name('title', 'sort.title'), 'Title');
  assert.equal(name('recentActivity', 'sort.recentActivity'), 'Recently added');
  assert.equal(name('count', 'sort.count'), 'Title count');
  assert.equal(name('forYou', 'sort.forYou'), 'For you');
});

test('an id the catalogue does not know is left to the caller', () => {
  assert.equal(browseNameId('somethingNew', 'browse.field.somethingNew', has), undefined);
  assert.equal(browseNameId('somethingNew', undefined, has), undefined);
});

test('a direction is worded by what the sort measures', () => {
  assert.deepEqual(['title', 'artist', 'album', 'added', 'year', 'releaseDate', 'aired', 'latestAired', 'lastPlayed', 'recentActivity', 'communityRating', 'criticRating', 'personalRating', 'resolution', 'duration', 'episodeCount', 'playCount', 'count', 'fileSize', 'trackNumber', 'forYou'].map(sortDirectionKind),
    ['alpha', 'alpha', 'alpha', 'date', 'date', 'date', 'date', 'date', 'date', 'date', 'rating', 'rating', 'rating', 'rating', 'duration', 'count', 'count', 'count', 'size', 'order', 'other']);
  for (const kind of ['alpha', 'date', 'rating', 'duration', 'count', 'size', 'order', 'other']) {
    const asc = defaultI18n.t('web.filters.direction' as never, {kind, dir: 'asc'}), desc = defaultI18n.t('web.filters.direction' as never, {kind, dir: 'desc'});
    assert.ok(asc && desc && asc !== desc, kind);
  }
});

const capabilities = (pivot: string): BrowseCapabilities => ({
  library: {id: 'l', name: 'Music', kind: 'music', defaultView: '', pinned: false},
  pivots: [{id: pivot, labelKey: 'x', entityKinds: [], defaultSort: [], supportedViews: [], browsable: true}],
  resolvedPivot: {id: pivot, labelKey: 'x', entityKinds: [], defaultSort: [], supportedViews: [], browsable: true},
  fields: [], sorts: [],
  quickFilters: [{id: 'unwatched', labelKey: 'filter.unwatched', query: {field: 'playState', operator: 'equals', value: 'unplayed'}}, {id: 'in-progress', labelKey: 'filter.inProgress', query: {field: 'playState', operator: 'equals', value: 'in-progress'}}, {id: 'favorites', labelKey: 'filter.favorites', query: {field: 'favorite', operator: 'equals', value: true}}],
  queryLimits: {maximumDepth: 4, maximumClauses: 20, maximumBytes: 4096, maximumSorts: 1, defaultLimit: 60, maximumLimit: 200, cursorTtlMs: 60000},
});

test('music has no "watched" on any of its tabs, the Genres tab included', () => {
  const sort = {field: 'title', direction: 'asc'} as const;
  assert.deepEqual(toolbarModel(capabilities('genres'), 'music', [], sort).quick.map(q => q.id), ['favorites']);
  assert.deepEqual(toolbarModel(capabilities('albums'), 'music', [], sort).quick.map(q => q.id), ['favorites']);
  assert.deepEqual(toolbarModel(capabilities('movies'), 'movie', [], sort).quick.map(q => q.id), ['unwatched', 'in-progress', 'favorites']);
});
