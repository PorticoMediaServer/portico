import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryContentService, type ContentRoute} from '../src/library-content.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};
const route: ContentRoute = {libraryId: 'movies', view: 'browse'};

function entry(id = 'item') {
  return {id, kind: 'movie', title: `Server title ${id}`, navigation: {view: 'item', entityId: id}};
}

function projection(path: string, changes: Record<string, unknown> = {}): any {
  const u = new URL(path, 'https://server.test');
  return {
    heading: {key: 'library.title', fallback: 'Named Library'},
    scope: {serverId: 'server', libraryId: decodeURIComponent(u.pathname.split('/')[3]), libraryKind: 'movie', view: u.searchParams.get('view'), entityId: u.searchParams.get('entityId') ?? '', viewerFence: 'fence'},
    revision: {catalog: 3, viewer: 4},
    query: {sort: 'title', direction: 'asc', category: '', q: '', limit: 40, searchMode: 'title_prefix'},
    navigation: [{id: 'browse', labelKey: 'library.browse', view: 'browse'}],
    sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']}],
    filters: [{id: 'category', labelKey: 'filter.category', options: [{id: 'decade:1990', label: '1990s', count: 5}]}],
    sections: [{id: 'server-grid', type: 'grid', heading: {key: 'server.heading', fallback: 'Server-selected heading'}, entries: [entry()], totalCount: 1, nextCursor: ''}],
    ...changes,
  };
}

function service(request: (path: string, signal?: AbortSignal) => Promise<unknown>) {
  return new LibraryContentService({api: {request: <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => request(path, signal) as Promise<T>}, scope, timeoutMs: 1000});
}

test('sections and entries preserve exact totals', async () => {
  const nav = service(async path => {
    const data = projection(path);
    data.sections[0].totalCount = 10000;
    data.sections[0].entries[0].count = 9000;
    return data;
  });
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().sections[0].totalCount, 10000);
  assert.equal(nav.getSnapshot().sections[0].entries[0].count, 9000);
  nav.dispose();
});

test('filter option counts are exact and a stray incomplete flag is ignored', async () => {
  const nav = service(async path => {
    const data = projection(path);
    data.filters = [{
      id: 'category', labelKey: 'filter.category', incomplete: true,
      options: [
        {id: 'genre:Action', label: 'Action', count: 8500},
        {id: 'genre:Drama', label: 'Drama', count: 120, },
      ],
    }];
    return data;
  });
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  const filter = nav.getSnapshot().projection!.filters[0]!;
  assert.equal('incomplete' in filter, false);
  assert.equal(filter.options[0]!.count, 8500);
  assert.equal(filter.options[1]!.count, 120);
  nav.dispose();
});

test('filters parse their exact option counts', async () => {
  const nav = service(async path => projection(path));
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().projection!.filters[0]!.options[0]!.count, 5);
  nav.dispose();
});

test('a collection keeps exact watched tallies and absence is never zero', async () => {
  const nav = service(async path => {
    const data = projection(path);
    data.sections[0].entries = [
      {...entry('watched'), kind: 'collection', count: 2, watchedCount: 1, unwatchedCount: 1},
      {...entry('absent'), kind: 'collection', count: 2},
      {...entry('invalid'), kind: 'collection', count: 2, watchedCount: -1, unwatchedCount: 1.5},
    ];
    return data;
  });
  try {
    await nav.select(route);
    assert.equal(nav.getSnapshot().phase, 'ready');
    const [watched, absent, invalid] = nav.getSnapshot().sections[0].entries;
    assert.equal(watched.watchedCount, 1);
    assert.equal(watched.unwatchedCount, 1);
    assert.equal('watchedCount' in absent, false);
    assert.equal('unwatchedCount' in absent, false);
    assert.equal('watchedCount' in invalid, false);
    assert.equal('unwatchedCount' in invalid, false);
  } finally {nav.dispose();}
});
