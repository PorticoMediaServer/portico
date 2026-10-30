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
const {SearchEmpty} = await import('../src/kit/states/SearchEmpty.tsx');
const {SearchResultStatus} = await import('../src/screens/search/SearchResultStatus.tsx');
test('no-match copy is a readable status and completion keeps an initially empty live region', () => {
  const empty = renderToStaticMarkup(createElement(SearchEmpty, {title: 'No matches', body: 'Try another search'}));
  assert.match(empty, /role="status"/);assert.match(empty, /No matches/);assert.match(empty, /Try another search/);
  const status = renderToStaticMarkup(createElement(SearchResultStatus, {count: 4, identity: 'query'}));
  assert.match(status, /role="status" aria-atomic="true"/);
  assert.doesNotMatch(status, /4 results/);
});
