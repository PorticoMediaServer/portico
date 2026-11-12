import test from 'node:test';
import assert from 'node:assert/strict';

// An audiobook library's series and author pages on phones and TVs, against the rebuilt server's
// answers: the series' volumes in volume order (series=), an author's books in the library (author=).
const book = (id: string, title: string, index?: number) => ({id, kind: 'audiobook', title, libraryId: 'lib_a', addedAt: '2026-11-12T00:00:00Z',
  images: {poster: `/v1/images/${id}`}, state: {}, ...(index ? {index} : {})});

async function page(kind: 'author' | 'book_series', id: string) {
  const {NativeAudiobookController} = await import('../src-next/screens/destinations/audiobooks/model.ts');
  const seen: string[] = [];
  const request = async (path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test'), query = url.searchParams;
    if (url.pathname === '/v1/items' && query.get('series') === 'ser_1')
      return query.get('cursor') === 'c2' ? {items: [book('bk_3', 'Third', 3)], total: 3} : {items: [book('bk_1', 'First', 1), book('bk_2', 'Second', 2)], nextCursor: 'c2', total: 3};
    if (url.pathname === '/v1/items' && query.get('author') === 'per_1' && query.get('library') === 'lib_a') return {items: [book('bk_9', 'Alone')], total: 1};
    if (url.pathname === '/v1/items/ser_1') return {id: 'ser_1', kind: 'series', title: 'Tides', libraryId: 'lib_a', addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}};
    if (url.pathname === '/v1/people/per_1') return {id: 'per_1', name: 'Ada Adams', biography: 'Writes about the sea.', images: {portrait: '/v1/images/ada'}, titleCount: 1};
    if (url.pathname === '/v1/libraries/lib_a') return {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}]};
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  };
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'};
  const context = {api: {request}, session: () => ({viewer}), authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile'}), current: () => true};
  const controller = new NativeAudiobookController({context: context as never, kind, libraryId: 'lib_a', id});
  const lease = {signal: new AbortController().signal, isCurrent: () => true};
  await controller.load(lease);
  return {controller, seen, lease};
}

test('a series page lists its volumes in volume order from the rebuilt server, and pages on', async () => {
  const {controller, seen, lease} = await page('book_series', 'ser_1');
  const state = controller.getSnapshot();
  assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
  assert.deepEqual([state.entity?.kind, state.entity?.title], ['book_series', 'Tides']);
  assert.deepEqual(state.sections.find(section => section.id === 'books')?.entries.map(entry => entry.id), ['bk_1', 'bk_2']);
  assert.ok(seen.some(request => request.startsWith('GET /v1/items?series=ser_1&sort=order')), seen.join(' | '));
  await controller.next('books', lease);
  assert.deepEqual(controller.getSnapshot().sections.find(section => section.id === 'books')?.entries.map(entry => entry.id), ['bk_1', 'bk_2', 'bk_3']);
  controller.dispose?.();
});

test('an author page is the person, with their books in this library', async () => {
  const {controller, seen} = await page('author', 'per_1');
  const state = controller.getSnapshot();
  assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
  assert.deepEqual([state.entity?.kind, state.entity?.title, state.entity?.overview, state.entity?.posterUrl], ['author', 'Ada Adams', 'Writes about the sea.', '/v1/images/ada']);
  assert.deepEqual(state.sections.find(section => section.id === 'books')?.entries.map(entry => entry.id), ['bk_9']);
  assert.ok(seen.includes('GET /v1/people/per_1'), seen.join(' | '));
  assert.ok(!seen.some(request => request.includes('/content?')), 'no old-server content read is sent');
  controller.dispose?.();
});
