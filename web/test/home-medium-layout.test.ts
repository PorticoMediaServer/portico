import test from 'node:test';
import assert from 'node:assert/strict';
import {homeLayout} from '../src/screens/home/layout.ts';

test('Home changes to the approved tablet metrics at medium window boundaries', () => {
  assert.deepEqual(homeLayout(599), {platform: 'phone-web', sizeClass: undefined, poster: 118, square: 132, wide: 262});
  for (const width of [600, 820, 1023]) assert.deepEqual(homeLayout(width), {platform: 'phone-web', sizeClass: 'medium', poster: 152, square: 152, wide: 296});
  assert.deepEqual(homeLayout(1024), {platform: 'desktop', sizeClass: undefined, poster: 190, square: 190, wide: 316});
});
