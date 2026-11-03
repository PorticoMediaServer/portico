import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeTrickplayWindow} from '../src-next/screens/watch/trickplay-frame.ts';
import type {TrickplaySet} from '../../../packages/client-core/src/trickplay.ts';

const hash = 'a'.repeat(64);
const set = (fields: Partial<TrickplaySet> = {}): TrickplaySet => ({id: hash, sourceId: 'src', sourceRevision: 'r1', width: 1600, height: 720, tileWidth: 320, tileHeight: 180,
  columns: 5, rows: 4, intervalSeconds: 10, durationSeconds: 1000, sourceOffsetSeconds: 0, tileCount: 5, frameCount: 100, stale: false, revision: hash,
  tilesUrl: `/v1/items/movie/trickplay/${hash}/tiles`, thumbnailsUrl: `/v1/items/movie/trickplay/${hash}/thumbnails.vtt`, ...fields});

for (const tileWidth of [240, 320]) for (const box of [{width: 256, height: 144}, {width: 480, height: 270}]) {
  test(`trickplay ${tileWidth} source shows one complete frame in ${box.width} display pixels`, () => {
    const tileHeight = tileWidth * 9 / 16, source = set({tileWidth, tileHeight, width: tileWidth * 5, height: tileHeight * 4});
    const frame = nativeTrickplayWindow(source, 70, box)!;
    // The sheet stays at its native size, offset so frame 7 (column 2, row 1) sits at the window's corner.
    assert.deepEqual(frame.sheet, {width: tileWidth * 5, height: tileHeight * 4, left: -tileWidth * 2, top: -tileHeight});
    assert.deepEqual(frame.window, {width: tileWidth, height: tileHeight});
    // The one-frame window scales exactly onto the display box.
    assert.deepEqual([frame.window.width * frame.scaleX, frame.window.height * frame.scaleY], [box.width, box.height]);
  });
}

test('PB-2: frames past the first sheet come from the server\'s next sheet, never a guessed one', () => {
  const source = set();
  assert.equal(nativeTrickplayWindow(source, 70, {width: 256, height: 144})!.tileUrl, `${source.tilesUrl}/0.jpg`);
  const later = nativeTrickplayWindow(source, 210, {width: 256, height: 144})!;
  assert.equal(later.tileUrl, `${source.tilesUrl}/1.jpg`, 'frame 21 is the second sheet\'s first frame');
  assert.deepEqual([later.sheet.left, later.sheet.top], [-320, -0]);
});

test('P2-71-PERF: every tile on one sheet asks for the same unscaled sheet, so it decodes once', () => {
  const source = set({columns: 10, rows: 10, width: 3200, height: 1800, tileCount: 1});
  const sizes = new Set([[256, 144], [480, 270]].flatMap(([width, height]) => [0, 10, 20, 30, 40].map(seconds => {
    const frame = nativeTrickplayWindow(source, seconds, {width, height})!;
    return `${frame.tileUrl} ${frame.sheet.width}x${frame.sheet.height}`;
  })));
  assert.deepEqual([...sizes], [`${source.tilesUrl}/0.jpg 3200x1800`], 'small and large tiles share one decode: the sheet\'s own size');
});

test('trickplay keeps a missing set dark, clamps time before the first frame and ends with the set', () => {
  const box = {width: 256, height: 144};
  assert.equal(nativeTrickplayWindow(undefined, 1, box), undefined);
  assert.equal(nativeTrickplayWindow(null, 1, box), undefined);
  assert.equal(nativeTrickplayWindow(set(), NaN, box), undefined);
  assert.equal(nativeTrickplayWindow(set(), 1, {width: 0, height: 144}), undefined);
  assert.equal(nativeTrickplayWindow(set(), 1000, box), undefined, 'past the set\'s duration');
  assert.deepEqual(nativeTrickplayWindow(set(), -10, box)!.sheet, {width: 1600, height: 720, left: -0, top: -0});
});
