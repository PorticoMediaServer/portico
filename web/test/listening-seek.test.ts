import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {listeningSeekModel} from '../src/screens/watch/listening/listening-seek.ts';

test('listening seek uses the admitted window and shows remaining time', () => {
  const result = listeningSeekModel(70, 300, {start: 10, end: 250});
  assert.equal(result.valid, true);
  assert.equal(result.progress, .25);
  assert.equal(result.remaining, 230);
  assert.equal(listeningSeekModel(999, 300, {start: 10, end: 250}).at, 250);
  assert.equal(listeningSeekModel(-10, 300, {start: 10, end: 250}).at, 10);
});
test('chapter marks come only from finite real chapter boundaries in the seek window', () => {
  const result = listeningSeekModel(75, 200, {start: 0, end: 200}, [
    {id: 'one', startSeconds: 0, endSeconds: 50}, {id: 'two', startSeconds: 50, endSeconds: 100},
    {id: 'same', startSeconds: 50, endSeconds: 150}, {id: 'bad', startSeconds: NaN, endSeconds: 20},
    {id: 'reversed', startSeconds: 110, endSeconds: 90}, {id: 'outside', startSeconds: 250, endSeconds: 300},
  ]);
  assert.deepEqual(result.ticks, [.25]);
  assert.deepEqual(result.chapter, {start: .25, end: .5});
  assert.deepEqual(listeningSeekModel(75, 200, {start: 0, end: 200}).ticks, []);
});
test('unknown or invalid bounds cannot enable seeking or fabricate chapter marks', () => {
  for (const bounds of [undefined, {start: 0, end: 0}, {start: 50, end: 10}, {start: 0, end: Infinity}]) {
    const result = listeningSeekModel(NaN, 0, bounds, [{id: 'chapter', startSeconds: 0, endSeconds: 30}]);
    assert.equal(result.valid, false);
    assert.equal(result.at, 0);
    assert.equal(result.remaining, undefined);
    assert.deepEqual(result.ticks, []);
    assert.equal(result.chapter, undefined);
  }
});
