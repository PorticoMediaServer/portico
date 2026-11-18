import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePresentation} from '../src/playback-v1/types.ts';

const base = {generation: 1, mode: 'stream', url: 'https://portico.test/v1/media/s/master.m3u8', startPositionMs: 0, engine: 'native', quality: {}};

test('a presentation carries the server\'s plain sentence for the viewer, and nothing when there is none', () => {
  assert.equal(parsePresentation({...base, notice: ' Only part 1 of 2 can play: the parts are encoded differently. '}).notice, 'Only part 1 of 2 can play: the parts are encoded differently.');
  assert.equal(parsePresentation(base).notice, undefined);
  assert.equal(parsePresentation({...base, notice: '   '}).notice, undefined);
});
