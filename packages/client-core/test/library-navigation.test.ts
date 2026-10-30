import test from 'node:test';
import assert from 'node:assert/strict';
import { LibraryNavigationService, navigationStorageKey, type LibraryNavigationMetadata, type NavigationScope, type NavigationStorage } from '../src/library-navigation.ts';
const scope = { viewerId: JSON.stringify(['hosted', 'account-a', 'profile-a']), serverId: 'server-a' };
const libraries: LibraryNavigationMetadata[] = ['movies', 'shows', 'family'].map(id => ({ id, name: id === 'movies' ? 'Cinema' : id, tabs: ['discover', 'browse'], pending: false, queryTab: 'discover', sorts: ['title', 'recent'], filters: { watched: ['yes', 'no'], genre: ['drama', 'comedy'] } }));
function memory() {
  const records = new Map<string, string>();
  const storage: NavigationStorage = { async read(key) { return records.get(key) ?? null; }, async write(key, value) { records.set(key, value); } };
  return { records, storage };
}
function service(storage?: NavigationStorage) { return new LibraryNavigationService({ scope, libraries, storage }); }
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { promise, resolve }; }
const tick = () => new Promise(resolve => setImmediate(resolve));

test('named library destinations retain independent tab, query, cursor, and anchor', () => {
  const nav = service();
  nav.selectLibrary('movies'); nav.selectTab('browse');
  nav.updateLibrary('movies', { sort: 'recent', filters: { watched: 'no' } });
  nav.updateLibrary('movies', { pageCursor: 'cursor-2', focusedItemId: 'movie-40', scrollAnchor: { itemId: 'movie-35', offset: -12.5 } });
  nav.setFocus({ region: 'content', targetId: 'movie-40' });
  const original = nav.getSnapshot().currentLibrary;
  nav.selectLibrary('shows'); nav.updateLibrary('shows', { filters: { genre: 'drama' } });
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, null);
  for (let i = 0; i < 100; i++) { nav.selectLibrary('movies'); nav.selectLibrary('shows'); }
  nav.selectLibrary('movies');
  assert.deepEqual(nav.getSnapshot().currentLibrary, original);
  assert.deepEqual(nav.getSnapshot().route, { kind: 'library', libraryId: 'movies', tab: 'browse' });
  assert.equal(nav.getSnapshot().libraries[0].name, 'Cinema');
  assert.ok(nav.getSnapshot().stackDepth <= 16);
});

test('player Back returns to detail, then exact library origin and focus', () => {
  const nav = service(); nav.selectLibrary('movies', 'browse');
  nav.updateLibrary('movies', { pageCursor: 'page-four', focusedItemId: 'movie-8', scrollAnchor: { itemId: 'movie-7', offset: 42 } });
  nav.setFocus({ region: 'content', targetId: 'movie-8' });
  const library = nav.getSnapshot();
  nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'movie-8' });
  nav.setFocus({ region: 'content', targetId: 'play-button' });
  nav.navigate({ kind: 'player', libraryId: 'movies', itemId: 'movie-8' });
  nav.navigate({ kind: 'settings' });
  assert.equal(nav.goBack(), true); assert.equal(nav.getSnapshot().route.kind, 'player');
  nav.goBack(); assert.equal(nav.getSnapshot().route.kind, 'detail');
  assert.equal(nav.getSnapshot().focus.targetId, 'play-button');
  nav.goBack(); assert.deepEqual(nav.getSnapshot().route, library.route);
  assert.deepEqual(nav.getSnapshot().currentLibrary, library.currentLibrary);
  assert.deepEqual(nav.getSnapshot().focus, library.focus);
});

test('rail and tabs have distinct Back restoration targets without platform focus calls', () => {
  const nav = service(); nav.setFocus({ region: 'rail', targetId: 'movies' });
  nav.selectLibrary('movies'); nav.selectTab('browse');
  nav.navigate({ kind: 'account' }); nav.goBack();
  assert.deepEqual(nav.getSnapshot().focus, { region: 'tabs', targetId: 'browse' });
  nav.goBack(); assert.equal(nav.getSnapshot().route.kind, 'home');
  assert.deepEqual(nav.getSnapshot().focus, { region: 'rail', targetId: 'movies' });
  assert.equal(nav.goBack(), false);
});

test('query or tab changes invalidate pagination and item position but preserve other libraries', () => {
  const nav = service(); nav.selectLibrary('movies');
  nav.updateLibrary('movies', { pageCursor: 'p2', focusedItemId: 'm2', scrollAnchor: { itemId: 'm2', offset: 5 } });
  nav.updateLibrary('movies', { sort: 'recent', pageCursor: 'stale' });
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, null);
  assert.equal(nav.getSnapshot().currentLibrary!.focusedItemId, null);
  assert.equal(nav.getSnapshot().currentLibrary!.scrollAnchor, null);
  nav.updateLibrary('movies', { pageCursor: 'p3' }); nav.selectTab('browse');
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, null);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, '');
  assert.equal(nav.getSnapshot().metadataPending, true);
  nav.selectTab('discover');
  assert.equal(nav.getSnapshot().currentLibrary!.sort, 'recent');
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, 'p3');
});

test('storage restores last library and bounded view fields, never detail/player', async () => {
  const m = memory(), nav = service(m.storage);
  nav.selectLibrary('movies', 'browse'); nav.updateLibrary('movies', { sort: 'recent' });
  nav.updateLibrary('movies', { pageCursor: 'p3', focusedItemId: 'm3', scrollAnchor: { itemId: 'm3', offset: 4 } });
  nav.setFocus({ region: 'content', targetId: 'm3' });
  nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'm3' });
  nav.navigate({ kind: 'player', libraryId: 'movies', itemId: 'm3' }); await nav.flush();
  const next = service(m.storage); await next.restore();
  assert.deepEqual(next.getSnapshot().route, { kind: 'library', libraryId: 'movies', tab: 'browse' });
  assert.equal(next.getSnapshot().currentLibrary!.pageCursor, 'p3');
  assert.deepEqual(next.getSnapshot().focus, { region: 'content', targetId: 'm3' });
  assert.equal(next.getSnapshot().canGoBack, false);
  assert.equal(next.getSnapshot().stackDepth, 1);
  assert.equal(m.records.size, 1);
});

test('generation fences a pending old-scope restore even when adapter ignores cancellation', async () => {
  const old = deferred<string | null>();
  const otherScope = { ...scope, viewerId: JSON.stringify(['direct', 'account-a', 'profile-b']) };
  const source = memory(); const seeded = service(source.storage); seeded.selectLibrary('movies', 'browse'); await seeded.flush();
  const storage: NavigationStorage = { read: async key => key === navigationStorageKey(scope) ? old.promise : null, write: async () => {} };
  const nav = service(storage); const pending = nav.restore(); await tick();
  await nav.setScope(otherScope, libraries);
  const newerGeneration = nav.getSnapshot().generation;
  old.resolve(source.records.get(navigationStorageKey(scope))!); await pending;
  assert.deepEqual(nav.getSnapshot().scope, otherScope);
  assert.equal(nav.getSnapshot().route.kind, 'home');
  assert.equal(nav.getSnapshot().generation, newerGeneration);
  assert.equal(nav.getSnapshot().remembered.length, 0);
});

test('stored payload from another identity is rejected even under the current storage key', async () => {
  const m = memory();
  m.records.set(navigationStorageKey(scope), JSON.stringify({ version: 1, scope: { ...scope, serverId: 'other-server' }, lastLibrary: { kind: 'library', libraryId: 'movies', tab: 'browse' }, views: [] }));
  const nav = service(m.storage); await nav.restore();
  assert.equal(nav.getSnapshot().route.kind, 'home'); assert.equal(nav.getSnapshot().remembered.length, 0);
  assert.match(nav.getSnapshot().storageError!, /could not be restored/);
  assert.notEqual(navigationStorageKey(scope), navigationStorageKey({ ...scope, viewerId: 'profile-b' }));
  assert.notEqual(navigationStorageKey({ viewerId: 'a:b', serverId: 'c' }), navigationStorageKey({ viewerId: 'a', serverId: 'b:c' }));
});

test('user navigation wins over a delayed same-scope restore', async () => {
  const old = deferred<string | null>(); const nav = service({ read: () => old.promise, write: async () => {} });
  const pending = nav.restore(); await tick(); nav.selectLibrary('shows', 'browse');
  old.resolve(JSON.stringify({ version: 1, scope, views: [], lastLibrary: { kind: 'library', libraryId: 'movies', tab: 'discover' } })); await pending;
  assert.deepEqual(nav.getSnapshot().route, { kind: 'library', libraryId: 'shows', tab: 'browse' });
  assert.equal(nav.getSnapshot().restoring, false);
});

test('malformed and oversized storage cannot poison live navigation', async () => {
  for (const raw of ['{broken', 'null', 'x'.repeat(512 * 1024 + 1), JSON.stringify({ version: 1, scope, views: Array(65).fill({}) })]) {
    const nav = service({ read: async () => raw, write: async () => {} }); await nav.restore();
    assert.equal(nav.getSnapshot().route.kind, 'home'); assert.ok(nav.getSnapshot().storageError);
    nav.selectLibrary('movies'); assert.equal(nav.getSnapshot().route.kind, 'library');
  }
});

test('metadata removal prunes active destinations, Back history, and saved library views', async () => {
  const m = memory(), nav = service(m.storage);
  nav.selectLibrary('movies'); nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'm1' });
  nav.selectLibrary('shows'); nav.navigate({ kind: 'player', libraryId: 'shows', itemId: 's1' });
  nav.setLibraries([libraries[2]]);
  assert.equal(nav.getSnapshot().route.kind, 'home');
  assert.equal(nav.getSnapshot().remembered.length, 0);
  while (nav.goBack()) assert.ok(!('libraryId' in nav.getSnapshot().route));
  assert.throws(() => nav.selectLibrary('movies'), /no longer available/);
  await nav.flush();
  const restored = new LibraryNavigationService({ scope, libraries: [libraries[2]], storage: m.storage }); await restored.restore();
  assert.equal(restored.getSnapshot().route.kind, 'home');
});

test('metadata choice changes reset invalid queries and refuse unsupported routes', () => {
  const nav = service(); nav.selectLibrary('movies', 'browse'); nav.updateLibrary('movies', { sort: 'recent' }); nav.updateLibrary('movies', { pageCursor: 'old-query' });
  nav.setLibraries([{ ...libraries[0], tabs: ['discover'], sorts: ['title'] }]);
  assert.deepEqual(nav.getSnapshot().route, { kind: 'library', libraryId: 'movies', tab: 'discover' });
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, null);
  assert.throws(() => nav.selectTab('removed'), /Unknown library tab/);
  assert.throws(() => nav.updateLibrary('movies', { filters: { secret: 'value' } }), /Unknown library filter/);
  assert.throws(() => nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'https://image.example' }), /Invalid media item ID/);
  assert.throws(() => nav.setLibraries([libraries[0], libraries[0]]), /duplicate/);
});

test('history and persistence remain bounded across many libraries and destinations', async () => {
  const m = memory(); const many = Array.from({ length: 100 }, (_, i) => ({ ...libraries[0], id: `library-${i}` }));
  const nav = new LibraryNavigationService({ scope, libraries: many, storage: m.storage });
  for (const library of many) nav.selectLibrary(library.id, 'browse');
  assert.equal(nav.getSnapshot().remembered.length, 64); assert.equal(nav.getSnapshot().stackDepth, 16);
  let backs = 0; while (nav.goBack()) backs++;
  assert.equal(backs, 15); assert.ok(nav.getSnapshot().remembered.length <= 64);
  await nav.flush(); assert.ok(JSON.parse(m.records.get(navigationStorageKey(scope))!).views.length <= 64);
});

test('snapshots are immutable and unrecognized properties never persist', async () => {
  const m = memory(), nav = service(m.storage); nav.selectLibrary('movies');
  nav.updateLibrary('movies', { pageCursor: 'p1', accessToken: 'NEVER-SAVE', posterUrl: 'https://example.test/image' } as any);
  const snapshot = nav.getSnapshot();
  assert.throws(() => { (snapshot.currentLibrary as any).sort = 'changed'; }, TypeError);
  assert.throws(() => { (snapshot.libraries[0].tabs as any).push('bad'); }, TypeError);
  await nav.flush(); const saved = m.records.get(navigationStorageKey(scope))!;
  assert.ok(!saved.includes('NEVER-SAVE')); assert.ok(!saved.includes('posterUrl'));
  nav.selectLibrary('shows'); assert.equal(snapshot.route.kind, 'library');
  assert.equal(snapshot.currentLibrary!.libraryId, 'movies');
});

test('slow storage coalesces focus writes and flush waits for latest position', async () => {
  const blocked = deferred<void>(); const records: string[] = [];
  const nav = service({ read: async () => null, async write(_key, value) { records.push(value); if (records.length === 1) await blocked.promise; } });
  nav.selectLibrary('movies');
  for (let i = 0; i < 1000; i++) nav.setFocus({ region: 'content', targetId: `movie-${i}` });
  blocked.resolve(); await nav.flush();
  assert.equal(records.length, 2);
  assert.equal(JSON.parse(records[1]).views[0].focus.targetId, 'movie-999');
});

test('storage failure is visible, and disposed services ignore late restores', async () => {
  const nav = service({ read: async () => { throw new Error('disk'); }, write: async () => { throw new Error('disk'); } });
  await nav.restore(); assert.ok(nav.getSnapshot().storageError);
  nav.selectLibrary('movies'); await nav.flush(); assert.match(nav.getSnapshot().storageError!, /could not be saved/);
  const late = deferred<string | null>(); const disposed = service({ read: () => late.promise, write: async () => {} });
  let notifications = 0; disposed.subscribe(() => notifications++);
  const pending = disposed.restore(); await tick(); const before = notifications;
  disposed.dispose(); late.resolve(null); await pending;
  assert.equal(notifications, before); assert.throws(() => disposed.selectLibrary('movies'), /disposed/);
});

test('server may omit sort choices on fixed-order views without a dummy control', () => {
  const nav = new LibraryNavigationService({ scope, libraries: [{ ...libraries[0], sorts: [] }] });
  nav.selectLibrary('movies');
  assert.deepEqual(nav.getSnapshot().libraries[0].sorts, []);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, '');
  assert.throws(() => nav.updateLibrary('movies', { sort: 'invented' }), /Invalid saved/);
  nav.setLibraries([{ ...libraries[0], sorts: ['title'] }]);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, 'title');
  nav.setLibraries([{ ...libraries[0], sorts: [] }]);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, '');
});

test('server-directed entity hierarchies share bounded Back and restore exact focus origin', async () => {
  for (const hierarchy of [['show','season'], ['artist','album'], ['book'], ['collection']] as const) {
    const m = memory(), nav = service(m.storage); nav.selectLibrary('movies', 'browse');
    nav.updateLibrary('movies', { focusedItemId: 'root-card', scrollAnchor: { itemId: 'root-card', offset: 75 } });
    nav.setFocus({ region: 'content', targetId: 'root-card' });
    for (const view of hierarchy) {
      nav.navigate({ kind: 'entity', libraryId: 'movies', entityId: `${view}-id`, view });
      nav.setFocus({ region: 'content', targetId: `${view}-child` });
    }
    nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'playable' });
    nav.navigate({ kind: 'player', libraryId: 'movies', itemId: 'playable' });
    nav.goBack(); assert.equal(nav.getSnapshot().route.kind, 'detail');
    for (const view of [...hierarchy].reverse()) {
      nav.goBack();
      assert.deepEqual(nav.getSnapshot().route, { kind: 'entity', libraryId: 'movies', entityId: `${view}-id`, view });
      assert.equal(nav.getSnapshot().focus.targetId, `${view}-child`);
    }
    nav.goBack(); assert.equal(nav.getSnapshot().route.kind, 'library');
    assert.equal(nav.getSnapshot().currentLibrary!.scrollAnchor!.offset, 75);
    assert.equal(nav.getSnapshot().focus.targetId, 'root-card');
    nav.navigate({ kind: 'entity', libraryId: 'movies', entityId: 'book-id', view: 'book' }); await nav.flush();
    const restored = service(m.storage); await restored.restore(); assert.equal(restored.getSnapshot().route.kind, 'library');
    nav.setLibraries([libraries[1]]); assert.equal(nav.getSnapshot().route.kind, 'home');
    while (nav.goBack()) assert.notEqual(nav.getSnapshot().route.kind, 'entity');
  }
});

test('direction and search query persist, restore through Back, and invalidate cursors', async () => {
  const m = memory(), nav = service(m.storage); nav.selectLibrary('movies', 'browse');
  nav.updateLibrary('movies', { direction: 'desc', q: 'Alien' });
  nav.updateLibrary('movies', { pageCursor: 'page2', focusedItemId: 'alien-2' });
  nav.navigate({ kind: 'detail', libraryId: 'movies', itemId: 'alien-2' }); nav.goBack();
  assert.equal(nav.getSnapshot().currentLibrary!.direction, 'desc');
  assert.equal(nav.getSnapshot().currentLibrary!.q, 'Alien');
  assert.equal(nav.getSnapshot().currentLibrary!.pageCursor, 'page2');
  await nav.flush(); const restored = service(m.storage); await restored.restore();
  assert.equal(restored.getSnapshot().currentLibrary!.direction, 'desc'); assert.equal(restored.getSnapshot().currentLibrary!.q, 'Alien');
  restored.updateLibrary('movies', { direction: 'asc' }); assert.equal(restored.getSnapshot().currentLibrary!.pageCursor, null);
  restored.updateLibrary('movies', { pageCursor: 'page3' }); restored.updateLibrary('movies', { q: 'Blade' }); assert.equal(restored.getSnapshot().currentLibrary!.pageCursor, null);
  assert.throws(() => restored.updateLibrary('movies', { direction: 'random' } as any), /Invalid saved/);
  assert.throws(() => restored.updateLibrary('movies', { q: 'x'.repeat(513) }), /Invalid saved/);
});

test('provisional server defaults cannot erase restored Browse query before its projection arrives', async () => {
  const m = memory();
  const browse = { ...libraries[0], queryTab: 'browse' };
  const original = new LibraryNavigationService({ scope, libraries: [browse], storage: m.storage });
  original.selectLibrary('movies', 'browse'); original.updateLibrary('movies', { sort: 'recent', direction: 'desc', q: 'Alien', filters: { watched: 'no' } });
  original.updateLibrary('movies', { pageCursor: 'signed-page', focusedItemId: 'alien2', scrollAnchor: { itemId: 'alien2', offset: 88 } }); await original.flush();
  const pending = { id: 'movies', name: 'Cinema', tabs: ['discover'], sorts: [], pending: true, queryTab: null };
  const restored = new LibraryNavigationService({ scope, libraries: [pending], storage: m.storage }); await restored.restore();
  assert.deepEqual(restored.getSnapshot().route, { kind: 'library', libraryId: 'movies', tab: 'browse' });
  assert.equal(restored.getSnapshot().metadataPending, true);
  assert.equal(restored.getSnapshot().currentLibrary!.q, 'Alien');
  assert.equal(restored.getSnapshot().currentLibrary!.sort, 'recent');
  assert.equal(restored.getSnapshot().currentLibrary!.pageCursor, 'signed-page');
  // A Discover projection knows the library's tabs but not Browse's query choices.
  restored.setLibraries([{ ...pending, tabs: ['discover','browse'], pending: false, queryTab: 'discover' }]);
  assert.equal(restored.getSnapshot().metadataPending, true);
  assert.equal(restored.getSnapshot().currentLibrary!.sort, 'recent');
  restored.setLibraries([browse]);
  assert.equal(restored.getSnapshot().metadataPending, false);
  assert.equal(restored.getSnapshot().currentLibrary!.scrollAnchor!.offset, 88);
});

test('per-tab query slots preserve Browse across Discover and process restart', async () => {
  const m = memory(), browse = { ...libraries[0], queryTab: 'browse' };
  const discover = { ...libraries[0], sorts: [], filters: {}, queryTab: 'discover' };
  const nav = new LibraryNavigationService({ scope, libraries: [browse], storage: m.storage });
  nav.selectLibrary('movies', 'browse'); nav.updateLibrary('movies', { sort: 'recent', direction: 'desc', q: 'Alien' });
  nav.updateLibrary('movies', { pageCursor: 'browse-page', focusedItemId: 'm4', scrollAnchor: { itemId: 'm4', offset: 20 } });
  nav.selectTab('discover'); nav.setLibraries([discover]);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, ''); assert.equal(nav.getSnapshot().currentLibrary!.q, '');
  await nav.flush();
  const restored = new LibraryNavigationService({ scope, libraries: [discover], storage: m.storage }); await restored.restore();
  restored.selectTab('browse');
  assert.equal(restored.getSnapshot().currentLibrary!.sort, 'recent');
  assert.equal(restored.getSnapshot().currentLibrary!.q, 'Alien');
  assert.equal(restored.getSnapshot().currentLibrary!.pageCursor, 'browse-page');
  assert.equal(restored.getSnapshot().metadataPending, true);
  restored.setLibraries([browse]); assert.equal(restored.getSnapshot().metadataPending, false);
  assert.equal(restored.getSnapshot().currentLibrary!.pageCursor, 'browse-page');
});

test('authoritative metadata rejects provisional unsupported tabs and queries, and tab slots stay bounded', async () => {
  const m = memory(); const many = Array.from({ length: 80 }, (_, i) => ({ ...libraries[0], id: `lib-${i}`, pending: true, queryTab: null }));
  const nav = new LibraryNavigationService({ scope, libraries: many, storage: m.storage });
  for (const lib of many) { nav.selectLibrary(lib.id, 'browse'); nav.selectTab('discover'); }
  await nav.flush(); const data = JSON.parse(m.records.get(navigationStorageKey(scope))!);
  assert.ok(data.tabViews.length <= 64); assert.ok(data.views.length <= 64);
  nav.selectLibrary('lib-79', 'unconfirmed'); nav.updateLibrary('lib-79', { sort: 'unconfirmed', q: 'saved' });
  assert.equal(nav.getSnapshot().metadataPending, true);
  nav.setLibraries([{ ...many[79], pending: false, queryTab: 'browse' }]);
  assert.deepEqual(nav.getSnapshot().route, { kind: 'library', libraryId: 'lib-79', tab: 'discover' });
  assert.equal(nav.getSnapshot().currentLibrary!.q, '');
  nav.selectTab('browse'); nav.updateLibrary('lib-79', { sort: 'recent' });
  nav.setLibraries([{ ...many[79], pending: false, queryTab: 'browse', sorts: ['title'] }]);
  assert.equal(nav.getSnapshot().currentLibrary!.sort, 'title');
});

test('paged season detail/player Back restores transient entity query, cursor and anchor', () => {
  const nav = service(); nav.selectLibrary('shows', 'browse');
  nav.updateLibrary('shows', { q: 'Root show search' });
  nav.updateLibrary('shows', { pageCursor: 'root-page', focusedItemId: 'show-card', scrollAnchor: { itemId: 'show-card', offset: 25 } });
  const root = nav.getSnapshot().currentLibrary;
  nav.navigate({ kind: 'entity', libraryId: 'shows', entityId: 'show1', view: 'show' });
  nav.setFocus({ region: 'content', targetId: 'season2' });
  nav.navigate({ kind: 'entity', libraryId: 'shows', entityId: 'season2', view: 'season' });
  nav.updateEntity({ sort: 'episode', direction: 'desc', q: 'Finale' });
  nav.updateEntity({ pageCursor: 'season-page2', focusedItemId: 'episode42', scrollAnchor: { itemId: 'episode42', offset: 72 } });
  nav.setFocus({ region: 'content', targetId: 'episode42' });
  const origin = nav.getSnapshot().currentEntity;
  nav.navigate({ kind: 'detail', libraryId: 'shows', itemId: 'episode42' });
  nav.navigate({ kind: 'player', libraryId: 'shows', itemId: 'episode42' });
  nav.goBack(); nav.goBack();
  assert.deepEqual(nav.getSnapshot().currentEntity, origin);
  assert.equal(nav.getSnapshot().focus.targetId, 'episode42');
  assert.deepEqual(nav.getSnapshot().currentLibrary, root);
  nav.goBack(); assert.equal(nav.getSnapshot().currentEntity!.entityId, 'show1');
  assert.equal(nav.getSnapshot().focus.targetId, 'season2');
  nav.goBack(); assert.equal(nav.getSnapshot().currentEntity, null); assert.deepEqual(nav.getSnapshot().currentLibrary, root);
});

test('artist query is isolated from library and other entity frames and never persists', async () => {
  const m = memory(), nav = service(m.storage); nav.selectLibrary('movies', 'browse');
  nav.updateLibrary('movies', { sort: 'recent', q: 'root-query' });
  nav.navigate({ kind: 'entity', libraryId: 'movies', entityId: 'artist1', view: 'artist' });
  nav.updateEntity({ sort: 'title', q: 'artist-query', direction: 'desc' });
  nav.updateEntity({ pageCursor: 'artist-page2', focusedItemId: 'album2', scrollAnchor: { itemId: 'album2', offset: 200 } });
  nav.navigate({ kind: 'entity', libraryId: 'movies', entityId: 'album2', view: 'album' });
  assert.equal(nav.getSnapshot().currentEntity!.q, '');
  nav.updateEntity({ q: 'album-query' }); nav.goBack();
  assert.equal(nav.getSnapshot().currentEntity!.q, 'artist-query');
  assert.equal(nav.getSnapshot().currentEntity!.pageCursor, 'artist-page2');
  assert.equal(nav.getSnapshot().currentLibrary!.q, 'root-query');
  nav.updateEntity({ q: 'new-artist-query' });
  assert.equal(nav.getSnapshot().currentEntity!.pageCursor, null); assert.equal(nav.getSnapshot().currentEntity!.scrollAnchor, null);
  await nav.flush(); const saved = m.records.get(navigationStorageKey(scope))!;
  assert.ok(!saved.includes('artist-query')); assert.ok(!saved.includes('artist-page2')); assert.ok(!saved.includes('album2'));
  const restored = service(m.storage); await restored.restore(); assert.equal(restored.getSnapshot().currentEntity, null);
  assert.equal(restored.getSnapshot().currentLibrary!.q, 'root-query');
  assert.throws(() => restored.updateEntity({ q: 'invalid-route' }), /Select an entity/);
});

test('opaque base64url IDs may start with underscore or dash throughout navigation and storage', async () => {
  const m = memory();
  const opaque = '_6_-Qc4xHTq1O5JWK838oyfaERbP4aqsFgOaBGsBI6g';
  const libs = [{ ...libraries[0], id: opaque }, { ...libraries[1], id: '-library' }];
  const nav = new LibraryNavigationService({ scope, libraries: libs, storage: m.storage });
  nav.selectLibrary(opaque, 'browse');
  nav.updateLibrary(opaque, { focusedItemId: '_item', scrollAnchor: { itemId: '-anchor', offset: 15 } });
  nav.setFocus({ region: 'content', targetId: '_section:-item' });
  nav.navigate({ kind: 'entity', libraryId: opaque, entityId: '-show', view: 'show' });
  nav.updateEntity({ focusedItemId: '-season', scrollAnchor: { itemId: '_season', offset: 25 } });
  nav.setFocus({ region: 'content', targetId: '-season' });
  nav.navigate({ kind: 'detail', libraryId: opaque, itemId: '_episode' });
  nav.navigate({ kind: 'player', libraryId: opaque, itemId: '-episode' });
  nav.goBack(); nav.goBack();
  assert.equal(nav.getSnapshot().currentEntity!.focusedItemId, '-season');
  nav.goBack(); assert.equal(nav.getSnapshot().focus.targetId, '_section:-item');
  nav.selectLibrary('-library'); nav.selectLibrary(opaque); await nav.flush();
  const restored = new LibraryNavigationService({ scope, libraries: libs, storage: m.storage }); await restored.restore();
  assert.equal(restored.getSnapshot().currentLibrary!.libraryId, opaque);
  assert.equal(restored.getSnapshot().currentLibrary!.focusedItemId, '_item');
  assert.equal(restored.getSnapshot().currentLibrary!.scrollAnchor!.itemId, '-anchor');
  for (const unsafe of ['bad/id', 'bad\\id', 'bad\n', 'https://host', 'x'.repeat(129)]) {
    assert.throws(() => restored.setFocus({ region: 'content', targetId: unsafe }), /Invalid navigation focus/);
  }
});


test('Saved and playlist Back frames retain occurrence focus independently of library state', async () => {
 const nav = service(); nav.selectLibrary('movies', 'browse');
 nav.updateLibrary('movies', {pageCursor:'movies-page-2'});
 nav.navigate({kind:'saved',view:'playlists'});
 nav.setFocus({region:'content',targetId:'playlist-one'});
 nav.navigate({kind:'playlist',playlistId:'playlist-one'});
 nav.setFocus({region:'content',targetId:'occurrence-two'});
 nav.navigate({kind:'detail',libraryId:'movies',itemId:'same-movie'});
 nav.goBack();
 assert.deepEqual(nav.getSnapshot().route,{kind:'playlist',playlistId:'playlist-one'});
 assert.equal(nav.getSnapshot().focus.targetId,'occurrence-two');
 nav.goBack();
 assert.deepEqual(nav.getSnapshot().route,{kind:'saved',view:'playlists'});
 assert.equal(nav.getSnapshot().focus.targetId,'playlist-one');
 nav.setLibraries([]);
 assert.equal(nav.getSnapshot().route.kind,'saved');
 await nav.setScope({serverId:'other',viewerId:'other'},[]);
 assert.equal(nav.getSnapshot().route.kind,'home');
 assert.equal(nav.getSnapshot().canGoBack,false);
 assert.throws(()=>nav.navigate({kind:'playlist',playlistId:'../bad'}));
});


test('Saved Back position retains scoped opaque page history and clears after query or account change',async()=>{
 const nav=service();nav.navigate({kind:'saved',view:'favorites'});
 nav.updateSaved({sort:'saved',direction:'desc'});
 nav.updateSaved({cursor:'opaque-page-2',history:[null],focusedItemId:'entry-41',scrollAnchor:{itemId:'entry-41',offset:32}});
 nav.setFocus({region:'content',targetId:'entry-41'});const prior=nav.getSnapshot().currentSaved;
 nav.navigate({kind:'detail',libraryId:'movies',itemId:'item-41'});nav.goBack();
 assert.deepEqual(nav.getSnapshot().currentSaved,prior);
 nav.updateSaved({sort:'title'});assert.equal(nav.getSnapshot().currentSaved?.cursor,null);assert.deepEqual(nav.getSnapshot().currentSaved?.history,[]);
 assert.throws(()=>nav.updateSaved({history:Array(65).fill(null)}));
 assert.throws(()=>nav.updateSaved({cursor:'x'.repeat(4097)}));
 await nav.setScope({serverId:'other',viewerId:'other'},[]);assert.equal(nav.getSnapshot().currentSaved,null);
});

test('Search origin and exact result focus survive detail Back without persisting private query state',async()=>{
 const store=memory(),nav=service(store.storage);
 nav.navigate({kind:'search'});
 nav.setFocus({region:'content',targetId:'search:movies:movies:_result-41'});
 nav.navigate({kind:'detail',libraryId:'movies',itemId:'_result-41'});
 assert.equal(nav.goBack(),true);
 assert.deepEqual(nav.getSnapshot().route,{kind:'search'});
 assert.deepEqual(nav.getSnapshot().focus,{region:'content',targetId:'search:movies:movies:_result-41'});
 nav.navigate({kind:'entity',view:'show',libraryId:'shows',entityId:'show-one'});
 nav.goBack();assert.deepEqual(nav.getSnapshot().route,{kind:'search'});
 await tick();for(const record of store.records.values())assert(!record.includes('search:movies'));
 await nav.setScope({serverId:scope.serverId,viewerId:'another-profile'},libraries);
 assert.equal(nav.getSnapshot().route.kind,'home');assert.equal(nav.getSnapshot().canGoBack,false);assert.equal(nav.getSnapshot().focus.targetId,'home');nav.dispose();
});


test('metadata requires explicit current tab binding and rejects the old global shape',()=>{
 const old:any={...libraries[0]};delete old.queryTab;assert.throws(()=>new LibraryNavigationService({scope,libraries:[old]}));
 assert.throws(()=>new LibraryNavigationService({scope,libraries:[{...libraries[0],queryTab:null}]}));
 const nav=service();nav.selectLibrary('movies','browse');assert.equal(nav.getSnapshot().metadataPending,true);assert.equal(nav.getSnapshot().currentLibrary!.sort,'');
 nav.setLibraries([{...libraries[0],queryTab:'browse'}]);assert.equal(nav.getSnapshot().metadataPending,false);assert.equal(nav.getSnapshot().currentLibrary!.sort,'title');
});
test('current navigation storage refuses missing tabViews and old view query fields',async()=>{
 const seed=memory(),nav=service(seed.storage);nav.selectLibrary('movies');await nav.flush();const current=JSON.parse(seed.records.get(navigationStorageKey(scope))!);
 for(const missing of ['lastLibrary','tabViews','direction','q']){const raw=structuredClone(current);if(missing==='tabViews'||missing==='lastLibrary')delete raw[missing];else{delete raw.views[0][missing];delete raw.tabViews[0][missing];}
  const restored=service({read:async()=>JSON.stringify(raw),write:async()=>{}});await restored.restore();assert.ok(restored.getSnapshot().storageError);assert.equal(restored.getSnapshot().route.kind,'home');}
});


test('native root tabs retain independent detail, query, scroll and Back histories', () => {
  const nav = service(); nav.enableNavigationTabs('home');
  nav.navigate({kind:'detail',libraryId:'movies',itemId:'home-movie'});
  nav.switchNavigationTab('library',{kind:'library',libraryId:'shows',tab:'browse'});
  assert.equal(nav.getSnapshot().stackDepth,1);
  nav.updateLibrary('shows',{scrollAnchor:{itemId:'show-3',offset:37}});
  nav.navigate({kind:'entity',libraryId:'shows',entityId:'show-3',view:'show'});
  nav.updateEntity({pageCursor:'season-two'});
  nav.switchNavigationTab('saved',{kind:'saved',view:'watchlist'});
  nav.updateSaved({cursor:'page-2',focusedItemId:'saved-4'});
  nav.switchNavigationTab('home',{kind:'home'});
  assert.deepEqual(nav.getSnapshot().route,{kind:'detail',libraryId:'movies',itemId:'home-movie'});
  nav.goBack(); assert.equal(nav.getSnapshot().route.kind,'home');
  assert.equal(nav.goBack(),false);
  nav.switchNavigationTab('library',{kind:'library',libraryId:'shows',tab:'browse'});
  assert.equal(nav.getSnapshot().currentEntity?.pageCursor,'season-two');
  nav.goBack(); assert.equal(nav.getSnapshot().currentLibrary?.scrollAnchor?.offset,37);
  assert.equal(nav.goBack(),false);
  nav.switchNavigationTab('saved',{kind:'saved',view:'watchlist'});
  assert.equal(nav.getSnapshot().currentSaved?.cursor,'page-2');
  assert.equal(nav.getSnapshot().navigationTab,'saved');
});

test('native tab history cannot reopen removed libraries or another profile', async () => {
  const nav = service(); nav.enableNavigationTabs('home');
  nav.navigate({kind:'detail',libraryId:'movies',itemId:'movie-1'});
  nav.switchNavigationTab('saved',{kind:'saved',view:'favorites'});
  nav.setLibraries(libraries.filter(library=>library.id!=='movies'));
  nav.switchNavigationTab('home',{kind:'home'});
  assert.equal(nav.getSnapshot().route.kind,'home');
  assert.equal(nav.goBack(),false);
  await nav.setScope({...scope,viewerId:'another-profile'},libraries);
  assert.equal(nav.getSnapshot().navigationTab,null);
  nav.enableNavigationTabs('home');
  nav.switchNavigationTab('saved',{kind:'saved',view:'watchlist'});
  assert.deepEqual(nav.getSnapshot().route,{kind:'saved',view:'watchlist'});
  assert.equal(nav.goBack(),false);
});
