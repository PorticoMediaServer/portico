/** Real RecentSearches callbacks; kit controls remain React elements, no layout credit. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactElement} from 'react';
import {transformTsx} from './transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {RecentSearches} = await import('../src/screens/search/RecentSearches.tsx');
const {Button, Chip} = await import('../src/kit/controls/index.ts');
function elements(node: unknown): ReactElement<any>[] {
  if (!isValidElement(node)) return [];
  const element = node as ReactElement<any>;
  return [element, ...Children.toArray(element.props.children).flatMap(elements)];
}
for (const compact of [false, true]) {
  test(`${compact ? 'phone rows' : 'desktop chips'} select the exact stored query and Clear calls only onClear`, () => {
    const queries = Object.freeze(['Mixed CASE: 2026', 'Amélie & friends']);
    const selected: string[] = []; let clears = 0;
    const drawn = RecentSearches({queries, compact, onSelect: query => selected.push(query), onClear: () => { clears++; }});
    const nodes = elements(drawn);
    const choices = nodes.filter(node => compact ? node.type === 'button' : node.type === Chip);
    assert.equal(choices.length, queries.length);
    for (const choice of choices) choice.props.onClick();
    assert.deepEqual(selected, queries);
    assert.equal(clears, 0);
    const clear = nodes.find(node => node.type === Button); assert.ok(clear);
    clear.props.onClick();
    assert.equal(clears, 1);
    assert.deepEqual(selected, queries, 'Clear must not select a query');
    assert.deepEqual(queries, ['Mixed CASE: 2026', 'Amélie & friends']);
  });
}
test('empty history supplies no query or Clear control in either layout', () => {
  for (const compact of [false, true]) {
    let calls = 0;
    assert.equal(RecentSearches({queries: [], compact, onSelect: () => { calls++; }, onClear: () => { calls++; }}), null);
    assert.equal(calls, 0);
  }
});
