import test from 'node:test';
import assert from 'node:assert/strict';
import {currentListeningChapter, locateListeningChapter, moveListeningChapter, listeningChapterSeek} from '../src-next/screens/destinations/music/audiobook-controls.ts';
import type {NativeListeningReadPort, NativeListeningReadSnapshot} from '../src-next/platform/playback/types.ts';

type Facts = Extract<NativeListeningReadSnapshot, {phase: 'current'}>;
const page = (start: number, end: number, total = 6): Facts => ({phase: 'current', rate: {selected: 1, supported: [1, 1.25, 1.5]},
  preferences: {phase: 'ready', values: null}, chapters: {phase: 'ready', pagination: {cursor: start === 1 ? null : `page-${start}`, canPrevious: start > 1},
    data: {scope: {serverId: 'server', libraryId: 'library', itemId: 'book', viewerFence: 'viewer', sessionId: 'session', generation: 1, sourceId: 'source'},
      revision: 'revision', status: 'available', reason: null, duration: total * 60, totalCount: total, nextCursor: end < total ? `page-${end + 1}` : '',
      chapters: Array.from({length: end - start + 1}, (_, offset) => {const index = start + offset; return {id: `source:${index}`, index, title: `Chapter ${index}`, startSeconds: (index - 1) * 60, endSeconds: index * 60};})}}});
function reader(pages = [page(1, 2), page(3, 4), page(5, 6)], initial = 0) {
  let at = initial, active = true, reads = 0;
  const port: NativeListeningReadPort = {current: () => active, getSnapshot: () => active ? pages[at]! : {phase: 'retired'}, subscribe: () => () => {},
    readChapters: async () => {at = 0; reads++;}, readNextChapterPage: async () => {at = Math.min(pages.length - 1, at + 1); reads++;},
    readPreviousChapterPage: async () => {at = Math.max(0, at - 1); reads++;}, dispose: () => {active = false;}};
  return {port, reads: () => reads, at: () => at, replace: (next: Facts) => {pages[at] = next;}};
}

test('chapter boundaries select the incoming chapter and retain the final end position', () => {
  const snapshot = page(1, 6);
  assert.equal(currentListeningChapter(snapshot, 59)?.index, 1);
  assert.equal(currentListeningChapter(snapshot, 60)?.index, 2);
  assert.equal(currentListeningChapter(snapshot, 360)?.index, 6);
  assert.equal(currentListeningChapter(snapshot, 361), undefined);
  assert.equal(currentListeningChapter(snapshot, NaN), undefined);
  assert.equal(currentListeningChapter({phase: 'retired'}, 10), undefined);
});

test('a resumed position finds its canonical page without retaining the whole chapter archive', async () => {
  const fixture = reader();
  assert.equal((await locateListeningChapter(fixture.port, 310))?.index, 6);
  assert.equal(fixture.at(), 2);
  assert.equal(fixture.reads(), 2);
  assert.equal((fixture.port.getSnapshot() as Facts).chapters.data?.chapters.length, 2);
  assert.equal((await locateListeningChapter(fixture.port, 70))?.index, 2);
  assert.equal(fixture.at(), 0);
  assert.equal(fixture.reads(), 4);
});

test('next and previous chapter cross page edges using the same source seconds', async () => {
  const fixture = reader(), seeks: number[] = [];
  await moveListeningChapter(fixture.port, 70, 1, value => seeks.push(value));
  assert.deepEqual(seeks, [120]);
  await moveListeningChapter(fixture.port, 130, -1, value => seeks.push(value));
  assert.deepEqual(seeks, [120, 60]);
  await moveListeningChapter(fixture.port, 10, -1, value => seeks.push(value));
  await moveListeningChapter(fixture.port, 350, 1, value => seeks.push(value));
  assert.deepEqual(seeks, [120, 60], 'book boundaries never become queue Previous/Next');
});

test('retirement while reading the adjacent page never sends a seek', async () => {
  const fixture = reader(), seeks: number[] = [];
  const readNext = fixture.port.readNextChapterPage;
  const port = {...fixture.port, readNextChapterPage: async () => {await readNext(); fixture.port.dispose();}};
  await moveListeningChapter(port, 70, 1, value => seeks.push(value));
  assert.deepEqual(seeks, []);
});

test('a changed source or chapter revision cannot supply a pending move target', async () => {
  for (const change of ['source', 'revision'] as const) {
    const next = page(3, 4);
    const changed: Facts = {...next, chapters: {...next.chapters, data: {...next.chapters.data!,
      ...(change === 'source' ? {scope: {...next.chapters.data!.scope, sourceId: 'other'}} : {revision: 'other'})}}};
    const fixture = reader([page(1, 2), changed]), seeks: number[] = [];
    await moveListeningChapter(fixture.port, 70, 1, value => seeks.push(value));
    assert.deepEqual(seeks, []);
  }
});

test('a drawn chapter survives progress wrapper updates but refuses changed or retired facts', () => {
  const fixture = reader(), drawn = fixture.port.getSnapshot();
  fixture.replace(page(1, 2));
  assert.equal(listeningChapterSeek(fixture.port, drawn, 'source:2'), 60);
  assert.equal(listeningChapterSeek(fixture.port, drawn, 'source:3'), undefined);
  const next = page(1, 2);
  fixture.replace({...next, chapters: {...next.chapters, data: {...next.chapters.data!, revision: 'new'}}});
  assert.equal(listeningChapterSeek(fixture.port, drawn, 'source:2'), undefined);
  fixture.port.dispose();
  assert.equal(listeningChapterSeek(fixture.port, drawn, 'source:2'), undefined);
});

test('missing chapters and repeated pagination terminate without guessing a chapter', async () => {
  const empty = page(1, 2);
  const fixture = reader([{...empty, chapters: {...empty.chapters, data: {...empty.chapters.data!, status: 'none', chapters: [], totalCount: 0, nextCursor: ''}}}]);
  assert.equal(await locateListeningChapter(fixture.port, 100), undefined);
  assert.equal(fixture.reads(), 0);
  const repeated = reader([page(1, 2)]);
  assert.equal(await locateListeningChapter(repeated.port, 300), undefined);
  assert.equal(repeated.reads(), 1);
});
