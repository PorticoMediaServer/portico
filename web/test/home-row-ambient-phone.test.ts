import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {homeRowAmbientView} = await import('../src/screens/browse/HomeRow.tsx');

const source = (key: string) => ({key, url: `/art/${key}`, variant: 'ambient-v1', maxEdge: 64, version: 'a'.repeat(64)}) as never;
const sources = [source('one'), source('two'), source('three')];

test('mobile See all draws the one drawn phone wash from the page artwork', () => {
  const view = homeRowAmbientView(sources, true);
  assert.equal(view.treatment, 'library-phone');
  assert.equal(view.source, sources[0]);
  assert.equal(view.sources, undefined);
});

test('desktop See all keeps the three-image library wash', () => {
  const view = homeRowAmbientView(sources, false);
  assert.equal(view.treatment, 'library');
  assert.equal(view.sources, sources);
  assert.equal(view.source, undefined);
});
