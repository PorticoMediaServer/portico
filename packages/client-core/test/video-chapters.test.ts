import test from 'node:test';
import assert from 'node:assert/strict';
import {videoChapterOffer, videoChapters} from '../src/presentation/video-chapters.ts';

const untitled = (n: number) => `Chapter ${n}`;
test('a title\'s chapters run each to the next one\'s start, the last to the end, with the playing one marked', () => {
  const rows = videoChapters([{startMs: 5000, title: 'Home before dawn'}, {startMs: 0, title: 'The letter'}, {startMs: 2500, title: ''}], 8, 3, untitled);
  assert.deepEqual(rows.map(row => [row.index, row.title, row.startSeconds, row.endSeconds, row.current]),
    [[1, 'The letter', 0, 2.5, false], [2, 'Chapter 2', 2.5, 5, true], [3, 'Home before dawn', 5, 8, false]]);
  assert.equal(videoChapters([{startMs: 0, title: 'x'}, {startMs: 5000, title: 'y'}], 8, 8, untitled)[1]!.current, true, 'the end is the last chapter');
});
test('one chapter, none, or chapters past the end draw nothing', () => {
  assert.deepEqual(videoChapters([{startMs: 0, title: 'All'}], 8, 0, untitled), []);
  assert.deepEqual(videoChapters(undefined, 8, 0, untitled), []);
  assert.deepEqual(videoChapters([{startMs: 0, title: 'a'}, {startMs: 9000, title: 'past the end'}], 8, 0, untitled), []);
});

// Desk B on #3453: INTEGRATION.md §7 keeps chapter ticks on the seek bar for every title; only the drawer is for titles without episodes.
test('an episode with chapters draws its ticks and offers no Chapters drawer; a film offers both', () => {
  const chapters = videoChapters([{startMs: 0, title: 'Cold open'}, {startMs: 2500, title: 'Act one'}, {startMs: 5000, title: 'Act two'}], 8, 0, untitled);
  assert.deepEqual(videoChapterOffer(chapters, true, 8), {ticks: [{id: 'chapter-1', kind: 'chapter', start: .3125}, {id: 'chapter-2', kind: 'chapter', start: .625}], drawer: false});
  assert.equal(videoChapterOffer(chapters, false, 8).drawer, true);
  assert.deepEqual(videoChapterOffer([], false, 8), {ticks: [], drawer: false});
});
