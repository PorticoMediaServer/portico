import test from 'node:test';
import assert from 'node:assert/strict';

// An audiobook's own page on phones and TVs, against the rebuilt server's detail: its chapters (the one
// file's chapter markers), who wrote it, and where the listener is. Every row plays the book itself.
const audiobook = {id: 'bk_1', kind: 'audiobook', title: 'The Martian', libraryId: 'lib_a', durationMs: 600_000, images: {}, addedAt: '2026-11-12T00:00:00Z',
  state: {positionMs: 250_000}, credits: [{personId: 'per_1', name: 'Andy Weir', role: 'author'}, {personId: 'per_2', name: 'R. C. Bray', role: 'narrator'}],
  versions: [{id: 'v1', parts: [{size: 1, startMs: 0, endMs: 600_000, streams: []}],
    markers: [{kind: 'chapter', startMs: 0, endMs: 200_000, title: 'Sol 6'}, {kind: 'chapter', startMs: 200_000, endMs: 600_000, title: 'Sol 7'}]}]};

test('an audiobook page lists its chapters from the rebuilt server, and a chapter or Resume plays the book from its place', async () => {
  const {NativeAudiobookController} = await import('../src-next/screens/destinations/audiobooks/model.ts');
  const seen: string[] = [], starts: {itemId: string; startSeconds?: number}[] = [];
  const request = async (path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/items/bk_1') return audiobook;
    if (url.pathname === '/v1/libraries/lib_a') return {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}]};
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  };
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'};
  const context = {api: {request}, session: () => ({viewer}), authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile'}), current: () => true,
    playback: {start: async (input: {target: {itemId: string; startSeconds?: number}}) => {starts.push(input.target);}}};
  const controller = new NativeAudiobookController({context: context as never, kind: 'book', libraryId: 'lib_a', id: 'bk_1'});
  const lease = {signal: new AbortController().signal, isCurrent: () => true};
  await controller.load(lease);
  const state = controller.getSnapshot();
  assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
  assert.deepEqual([state.entity?.title, state.entity?.author, state.entity?.narrators, state.entity?.bookProgress?.chapter?.number], ['The Martian', 'Andy Weir', ['R. C. Bray'], 2]);
  const chapters = state.sections.find(section => section.id === 'chapters')!.entries;
  assert.deepEqual(chapters.map(entry => entry.title), ['Sol 6', 'Sol 7']);
  assert.deepEqual(state.sections.find(section => section.id === 'context')?.entries.map(entry => [entry.kind, entry.title]), [['author', 'Andy Weir']]);
  await controller.playChapter(lease, chapters[1]!);
  await controller.resume(lease);
  assert.deepEqual(starts, [{itemId: 'bk_1', startSeconds: 200}, {itemId: 'bk_1', startSeconds: 250}]);
  assert.ok(!seen.some(path => path.includes('/content?')), 'no old-server content read is sent');
  controller.dispose();
});
