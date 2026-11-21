import test from 'node:test';
import assert from 'node:assert/strict';
import {captionPaint} from '../src-next/platform/playback/caption-style.ts';

test('the subtitle preview paints the effective caption choices the way the player draws them', () => {
  assert.deepEqual(captionPaint({}), {scale: 1, color: '#fff', backgroundColor: 'rgba(0,0,0,0.65)', edge: 'none', position: 'bottom'});
  assert.deepEqual(captionPaint({'playback.subtitleSize': 'extra-large', 'playback.subtitleColor': 'pink', 'playback.subtitleBackground': 'outline', 'playback.subtitlePosition': 'raised'}),
    {scale: 1.6, color: '#ff80c0', backgroundColor: 'transparent', edge: 'outline', position: 'raised'});
  assert.equal(captionPaint({'playback.subtitleBackground': 'opaque'}).backgroundColor, '#000');
  assert.deepEqual(captionPaint({'playback.subtitleSize': 'huge', 'playback.subtitlePosition': 'middle'}), captionPaint({}), 'an unknown value falls back to the default');
});
