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
const {RecentSearches} = await import('../src/screens/search/RecentSearches.tsx');
test('recent Search renders only supplied history with phone rows or desktop chips, and no invented empty history', () => {
  const queries = ['Stored query', 'Hunger Games: Special Edition'];
  const props = {queries, onSelect() {}, onClear() {}};
  const phone = renderToStaticMarkup(createElement(RecentSearches, {...props, compact: true}));
  assert.match(phone, /Recent searches/);assert.match(phone, />Clear</);
  assert.equal((phone.match(/class="recentRow"/g) ?? []).length, 2);
  assert.match(phone, /Hunger Games: Special Edition/);assert.doesNotMatch(phone, /Remove|casablanca|Crime/);
  const desktop = renderToStaticMarkup(createElement(RecentSearches, {...props, compact: false}));
  assert.doesNotMatch(desktop, /class="recentRow"/);
  assert.match(desktop, /Stored query/);
  assert.equal(renderToStaticMarkup(createElement(RecentSearches, {...props, compact: true, queries: []})), '');
  assert.deepEqual(queries, ['Stored query', 'Hunger Games: Special Edition']);
});
