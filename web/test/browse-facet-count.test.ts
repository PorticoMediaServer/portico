import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {parseBrowseFacets} from '@core/browse.ts';
import {transformTsx} from './transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {FacetChips} = await import('../src/kit/browse/FilterBuilder.tsx');
test('published facet lower bounds retain their exact count and flag through the rendered control', () => {
  const page = parseBrowseFacets({field: 'genre', total: 3, values: [
    {value: 'a', label: 'A', count: 10_001, countIsLowerBound: true},
    {value: 'b', label: 'B', count: 10_002, countIsLowerBound: false},
    {value: 'c', label: 'C', count: 0},
  ]}, 'genre');
  const html = renderToStaticMarkup(createElement(FacetChips, {values: page.values, included: [], excluded: [], onValueChange() {}}));
  assert.match(html, /<small>10,001\+<\/small>/);
  assert.match(html, /<small>10,002<\/small>/);
  assert.match(html, /<small>0<\/small>/);
  assert.doesNotMatch(html, /10K|10,000/);
});
