import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeDocument} from '../../../packages/client-core/src/home.ts';
import {validateContentProjection} from '../../../packages/client-core/src/library-content.ts';

const known = {id: 'movie', kind: 'movie', title: 'Known title', libraryId: 'movies'};
const future = {id: 'future', kind: 'interactive', title: 'Future title', libraryId: 'movies'};

test('an unknown entry kind drops only that entry from Home', () => {
  const row = {id: 'recent', title: 'Recent', kind: 'recent', artworkShape: 'poster', endpoint: '/v1/home/rows/recent', libraryId: 'movies', viewerFence: 'viewer', privacySensitivity: 'catalog', policyState: 'available', priority: 1, cacheTtlMs: 60000, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: false, entries: [known, future], total: 2, start: 0, limit: 2, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}};
  const document = {serverId: 'server', viewerFence: 'viewer', rows: [row], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-09-25T00:00:00Z'};
  assert.deepEqual(validateHomeDocument(document).rows[0].entries.map(e => e.id), ['movie']);
  assert.deepEqual(validateHomeDocument({...document, rows: [{...row, entries: [future]}]}).rows[0].entries, []);
  assert.deepEqual(validateHomeDocument({...document, rows: [{...row, entries: [known, {...known, id: 'bad', title: 2}]}]}).rows[0].entries.map(e => e.id), ['movie']);
  assert.throws(() => validateHomeDocument({...document, rows: 'invalid'}));
});

test('an unknown entry kind keeps the rest of a Discover projection', () => {
  const route = {libraryId: 'movies', view: 'discover'} as const;
  const scope = {viewerId: 'viewer', serverId: 'server'};
  const projection = {heading: {key: 'library.title', fallback: 'Movies'}, scope: {serverId: 'server', libraryId: 'movies', libraryKind: 'movie', view: 'discover', entityId: '', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, query: {sort: 'title', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'none'}, navigation: [], sorts: [], filters: [], sections: [{id: 'discover', type: 'rail', heading: {key: 'discover', fallback: 'Discover'}, entries: [known, future], totalCount: 2, nextCursor: ''}]};
  assert.deepEqual(validateContentProjection(projection, route, scope, 40).sections[0].entries.map(e => e.id), ['movie']);
  const badSeeAll = {...projection, sections: [{...projection.sections[0], seeAll: {pivot: 'movies', query: {futureOperator: []}, sort: [{field: 'forYou', direction: 'desc'}]}}]};
  assert.equal(validateContentProjection(badSeeAll, route, scope, 40).sections[0].seeAll, undefined);
});
