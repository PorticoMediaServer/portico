import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeWatchTVPresentation} from '../src-next/screens/watch/tv-presentation.ts';

test('TV keeps the whole measured RN pane and scales authored overlay coordinates once', () => {
  for (const window of [{width: 1920, height: 1080}, {width: 960, height: 540}, {width: 1000, height: 800}]) {
    for (const panel of ['options', 'audio', 'subtitles', 'quality', 'playback-info'] as const) {
      const value = nativeWatchTVPresentation(window, false, panel)!;
      const width = panel === 'playback-info' ? 960 : 680;
      assert.deepEqual(value.layout.surface, {x: 0, y: 0, ...window});
      assert.deepEqual(value.panel, {x: 1920 - width, y: 0, width, height: 1080});
      assert.deepEqual(value.layout.panel, {x: value.canvas.left + (1920 - width) * value.canvas.scale, y: value.canvas.top, width: width * value.canvas.scale, height: 1080 * value.canvas.scale});
      assert.equal(value.layout.controlsHidden, false);
    }
  }
  assert.equal(nativeWatchTVPresentation({width: 960, height: 540}, true, 'audio')!.layout.surface, undefined);
  assert.equal(nativeWatchTVPresentation({width: NaN, height: 540}, false), undefined);
});
