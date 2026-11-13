// R4: the player's seek line says the time at the pointer even with no preview still, and keeps its place once
// playback has ended instead of jumping back to the start (desktop-player frame 1).
import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import {watchLineValue} from '../src/screens/watch/model.ts';

test('the time at the pointer is drawn above it, at its place, only while the pointer is over the line', async context => {
  const loader = registerHooks({load(url, ctx, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
  context.after(() => loader.deregister());
  const {SeekLine} = await import('../src/kit/media/SeekLine.tsx');
  const over = renderToStaticMarkup(createElement(SeekLine, {variant: 'player', value: .2, hover: .5, hoverLabel: '0:04'}));
  assert.match(over, /class="hoverLabel"[^>]*style="inset-inline-start:clamp\(var\(--seek-label-half\), 50%, calc\(100% - var\(--seek-label-half\)\)\)">0:04</);
  assert.doesNotMatch(renderToStaticMarkup(createElement(SeekLine, {variant: 'player', value: .2, hover: null, hoverLabel: '0:04'})), /hoverLabel/);
  assert.doesNotMatch(renderToStaticMarkup(createElement(SeekLine, {variant: 'player', value: .2, hover: .5})), /hoverLabel/, 'a still with its own time carries it instead');
});

test('with no seek window the line stays where playback is, not at the start', () => {
  assert.equal(watchLineValue({start: 0, end: 8}, 4, 8), .5);
  assert.equal(watchLineValue(null, 8, 8), 1, 'ended: the fill and the thumb stay at the end');
  assert.equal(watchLineValue(null, 3, 0), 0, 'no length known: the start');
});
