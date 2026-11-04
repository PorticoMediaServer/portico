import test from 'node:test';
import assert from 'node:assert/strict';
import {bookChapterIndex, bookChapterMove, bookChapterSteps, bookChapterTarget, playingBookChapter, readBookChapters, type BookChapter} from '../src/book-chapters.ts';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '../src/queue-controller.ts';
import type {V1Http} from '../src/playback-v1/http.ts';

// MU-7 / MU-2: a book's chapters are read across its parts and Previous/Next chapter crosses them.
const wire = [
  {id: 'part-a:1', itemId: 'part-a', index: 1, number: 1, title: 'One', startMs: 0, endMs: 600_000},
  {id: 'part-a:2', itemId: 'part-a', index: 2, number: 2, title: 'Two', startMs: 600_000, endMs: 1_200_000},
  {id: 'part-b:1', itemId: 'part-b', index: 1, number: 3, title: 'Three', startMs: 30_000, endMs: 900_000},
  {id: 'part-b:2', itemId: 'part-b', index: 2, number: 4, title: 'Four', startMs: 900_000, endMs: 1_800_000},
];
const rows = (): BookChapter[] => wire.map(({startMs, endMs, ...row}) => ({...row, startSeconds: startMs / 1000, endSeconds: endMs / 1000}));

test('the whole book is read page by page in book order, in seconds', async () => {
  const asked: string[] = [];
  const request = async (path: string) => {
    asked.push(path);
    return path.includes('cursor=') ? {chapters: wire.slice(2), nextCursor: ''} : {chapters: wire.slice(0, 2), nextCursor: 'part-a:2'};
  };
  const book = await readBookChapters(request, 'book');
  assert.deepEqual(book.chapters, rows());
  assert.deepEqual(asked, ['/v1/books/book/chapters?limit=100', '/v1/books/book/chapters?limit=100&cursor=part-a%3A2']);
});

test('a broken book chapter list is refused rather than half used', async () => {
  const broken = [
    {chapters: [{...wire[0], number: 2}], nextCursor: ''},
    {chapters: [wire[1], wire[0]], nextCursor: ''},
    {chapters: [{...wire[0], endMs: 0}], nextCursor: ''},
    {chapters: [{...wire[0], id: 'other:1'}], nextCursor: ''},
    {chapters: wire.slice(0, 1), nextCursor: ''.padEnd(5000, 'x')},
  ];
  for (const page of broken) await assert.rejects(readBookChapters(async () => page, 'book'), JSON.stringify(page).slice(0, 80));
  let reads = 0;
  await assert.rejects(readBookChapters(async () => {reads++; return {chapters: [], nextCursor: 'same'};}, 'book'));
  assert.equal(reads, 1, 'an empty page with more to come stops the read');
});

test('Previous and Next chapter step through the book, across parts', () => {
  const chapters = rows();
  assert.equal(bookChapterIndex(chapters, 'part-a', 0), 0);
  assert.equal(bookChapterIndex(chapters, 'part-b', 10), 1, 'before a part\'s first chapter is still the last chapter before it');
  assert.equal(bookChapterIndex(chapters, 'part-a', 599.7), 1, 'a seek that lands just short of a chapter counts as that chapter');
  assert.equal(bookChapterIndex(chapters, 'part-z', 10), undefined);
  assert.equal(bookChapterTarget(chapters, 'part-a', 100, 'next')?.id, 'part-a:2');
  assert.equal(bookChapterTarget(chapters, 'part-a', 700, 'next')?.id, 'part-b:1', 'Next from the last chapter of a part goes to the next part');
  assert.equal(bookChapterTarget(chapters, 'part-b', 1000, 'next'), null, 'nothing after the last chapter');
  assert.equal(bookChapterTarget(chapters, 'part-b', 40, 'previous')?.id, 'part-b:1', 'more than three seconds in restarts the chapter');
  assert.equal(bookChapterTarget(chapters, 'part-b', 32, 'previous')?.id, 'part-a:2', 'at the start of a part, Previous goes to the part before');
  assert.equal(bookChapterTarget(chapters, 'part-a', 2, 'previous'), null, 'nothing before the first chapter');
  assert.equal(bookChapterTarget(chapters, 'part-z', 2, 'next'), undefined, 'a part the list doesn\'t know moves the queue instead');
});

test('MU-7: the steps a native player holds move exactly as Previous and Next chapter do in the app', () => {
  // Three parts, the middle one with a gap before its first chapter, so every edge is covered.
  const chapters: BookChapter[] = [
    ...rows(),
    {id: 'part-c:1', itemId: 'part-c', index: 1, number: 5, title: 'Five', startSeconds: 0, endSeconds: 60},
  ];
  const entries: Record<string, string> = {'part-a': 'e-a', 'part-b': 'e-b', 'part-c': 'e-c'};
  for (const itemId of ['part-a', 'part-b', 'part-c']) {
    const plan = bookChapterSteps(chapters, 's1', itemId, id => entries[id])!;
    assert.ok(plan.steps.length <= chapters.length);
    for (let seconds = 0; seconds <= 1900; seconds += 0.25) for (const direction of ['previous', 'next'] as const) {
      const target = bookChapterTarget(chapters, itemId, seconds, direction);
      const expected = target === null ? null : target!.itemId === itemId ? {kind: 'seek', seconds: target!.startSeconds} : {kind: 'entry', entryId: entries[target!.itemId], startSeconds: target!.startSeconds};
      assert.deepEqual(bookChapterMove(plan, seconds, direction), expected, `${itemId} ${direction} at ${seconds}`);
    }
  }
  const unseen = bookChapterSteps(chapters, 's1', 'part-b', id => id === 'part-b' ? 'e-b' : undefined)!;
  assert.deepEqual(unseen.steps.map(step => step.entryId), [null, null], 'a part whose queue entry isn\'t known is left out');
  assert.deepEqual(bookChapterMove(unseen, 1000, 'next'), {kind: 'queue'}, 'and the queue moves instead');
  assert.deepEqual(bookChapterMove(unseen, 31, 'previous'), {kind: 'queue'});
  assert.deepEqual(bookChapterMove(unseen, 100, 'next'), {kind: 'seek', seconds: 900});
  assert.equal(bookChapterSteps(chapters, 's1', 'part-z', id => entries[id]), undefined);
});

test('a playing book reads its chapters once, and a chapter in another part starts there in the same queue move', async () => {
  const server = new FakePlaybackServer({audioItem: id => id.startsWith('part-'), itemKind: id => id.startsWith('part-') ? 'audiobook_file' : undefined});
  const inner = fakeHttp(server), log: {method: string; path: string; body?: unknown}[] = [];
  const http: V1Http = {send: async request => {
    log.push({method: request.method, path: request.path, body: request.body});
    if (request.path.startsWith('/v1/books/book/chapters')) return {status: 200, headers: {}, body: {chapters: wire, nextCursor: ''}} as never;
    const response = await inner.send(request as never);
    const body = response.body as {data?: Record<string, unknown>} | undefined;
    if (/\/v1\/items\/part-[ab]\/listening$/.test(request.path) && body?.data) body.data.bookId = 'book';
    return response as never;
  }};
  let serial = 0;
  const api = new V1PlaybackApi(http, {key: () => `k${++serial}-00000000`});
  const playback = new PlaybackService(api, () => `r${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'}, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const stop = player.connect();
  try {
    await player.playEntries(['part-a', 'part-b'].map(queueItemInput));
    for (let i = 0; i < 20 && player.chapterTarget('next') === undefined; i++) await new Promise(r => setTimeout(r, 10));
    assert.ok(player.chapterTarget('next'), 'the book\'s chapters are known while its part plays');
    const view = player.bookChaptersView();
    assert.equal(view?.phase, 'ready'); assert.equal(view?.chapters.length, 4, 'MU-2: the list spans every part');
    assert.equal(view?.itemId, 'part-a'); assert.equal(view?.chapters[view.current]?.itemId, 'part-a');
    const run = playback.getSnapshot().playbackRun;
    await player.selectQueuedItem('part-b', 900);
    const advance = log.findLast(entry => entry.method === 'POST' && entry.path.endsWith(':advance'));
    assert.deepEqual(advance?.body, {reason: 'entry', entryId: (advance?.body as {entryId: string}).entryId, startPositionMs: 900_000});
    assert.equal(playback.getSnapshot().itemId, 'part-b');
    assert.equal(player.currentSession()?.presentation.startPositionMs, 900_000, 'the part starts at the chapter, not where it was left');
    assert.equal(playback.getSnapshot().playbackRun, run, 'a chapter move keeps the run (its sleep timer and speed)');
    await new Promise(r => setTimeout(r, 30));
    assert.equal(log.filter(entry => entry.path.startsWith('/v1/books/')).length, 1, 'one read per book, not per part');
  } finally {stop(); player.dispose();}
});

test('MU-2: the playing chapter is drawn only when it is in the playing part', () => {
  const chapters = rows();
  assert.equal(playingBookChapter({bookId: 'book', itemId: 'part-b', phase: 'ready', chapters, current: 3})?.id, 'part-b:2');
  assert.equal(playingBookChapter({bookId: 'book', itemId: 'part-b', phase: 'ready', chapters, current: 1}), undefined, 'before part B\'s first chapter, part A\'s last doesn\'t apply');
  assert.equal(playingBookChapter({bookId: 'book', itemId: 'part-a', phase: 'loading', chapters, current: 0}), undefined);
  assert.equal(playingBookChapter(null), undefined);
});
