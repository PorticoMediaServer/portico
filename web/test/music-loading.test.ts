import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {MusicDetailSkeleton} = await import('../src/screens/detail/MusicLoading.tsx');

for (const kind of ['album', 'artist'] as const) test(`${kind} reserves its layout, then draws still shapes at 300 ms and a quiet line at 8 s`, () => {
  const render = (elapsedMs: number) => renderToStaticMarkup(createElement(MusicDetailSkeleton, {kind, elapsedMs}));
  const waiting = render(299), shapes = render(300), slow = render(8000);
  assert.match(waiting, new RegExp(`data-kind="${kind}"`));
  assert.match(waiting, /class="hero"/);
  assert.match(waiting, /class="content"/);
  assert.doesNotMatch(waiting, /class="block/);
  assert.match(shapes, /class="block art"/);
  assert.match(shapes, new RegExp(`border-radius:${kind === 'artist' ? 999 : 12}px`));
  assert.match(shapes, /class="tracks"/);
  assert.match(shapes, /class="details"/);
  assert.doesNotMatch(shapes, /class="quietBanner"/);
  assert.match(slow, /class="quietBanner"/);
  assert.doesNotMatch(slow, /<button|<input|role="progressbar"/);
});
