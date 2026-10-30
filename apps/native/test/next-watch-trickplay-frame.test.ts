import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeTrickplayWindow} from '../src-next/screens/watch/trickplay-frame.ts';

for (const tileWidth of [240, 320]) for (const box of [{width: 256, height: 144}, {width: 480, height: 270}]) {
  test(`trickplay ${tileWidth} source shows one complete frame in ${box.width} display pixels`, () => {
    const source = {url: '/trickplay.jpg', intervalMs: 10000, tileWidth, tileHeight: tileWidth * 9 / 16, columns: 5, rows: 4};
    const frame = nativeTrickplayWindow(source, 70, box)!;
    // The sheet stays at its native size, offset so frame 7 (column 2, row 1) sits at the window's corner.
    assert.deepEqual(frame.sheet, {width: tileWidth * 5, height: source.tileHeight * 4, left: -tileWidth * 2, top: -source.tileHeight});
    assert.deepEqual(frame.window, {width: tileWidth, height: source.tileHeight});
    // The one-frame window scales exactly onto the display box.
    assert.deepEqual([frame.window.width * frame.scaleX, frame.window.height * frame.scaleY], [box.width, box.height]);
  });
}

test('P2-71-PERF: every tile of the strip asks for the same unscaled sheet, so it decodes once', () => {
  const source = {url: '/trickplay.jpg', intervalMs: 10000, tileWidth: 320, tileHeight: 180, columns: 10, rows: 10};
  const sizes = new Set([[256, 144], [480, 270]].flatMap(([width, height]) => [0, 10, 20, 30, 40].map(seconds => {
    const frame = nativeTrickplayWindow(source, seconds, {width, height})!;
    return `${frame.sheet.width}x${frame.sheet.height}`;
  })));
  assert.deepEqual([...sizes], ['3200x1800'], 'small and large tiles share one decode size: the sheet\'s own');
});

test('trickplay keeps missing or invalid sheets dark and clamps time before the first tile', () => {
  const box = {width: 256, height: 144}, source = {url: '/trickplay.jpg', intervalMs: 10000, tileWidth: 320, tileHeight: 180, columns: 5, rows: 4};
  assert.equal(nativeTrickplayWindow(undefined, 1, box), undefined);
  assert.equal(nativeTrickplayWindow({...source, tileWidth: 0}, 1, box), undefined);
  assert.equal(nativeTrickplayWindow({...source, rows: Infinity}, 1, box), undefined);
  assert.equal(nativeTrickplayWindow(source, NaN, box), undefined);
  assert.deepEqual(nativeTrickplayWindow(source, -10, box)!.sheet, {width: 1600, height: 720, left: -0, top: -0});
});
