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
const {SortMenu} = await import('../src/kit/browse/BrowseToolbar.tsx');
const {MenuItem, MenuRow} = await import('../src/kit/overlays/Popover.tsx');

test('sort trigger names both the sort and direction even when direction is not drawn', () => {
  for (const showOrder of [true, false]) {
    const html = renderToStaticMarkup(createElement(SortMenu, {sorts: [{id: 'date', labelKey: 'next.browse.sort.dateAdded', directions: ['asc', 'desc'], defaultDirection: 'desc', expensive: false, applicableKinds: []}],
      value: {field: 'date', direction: 'desc', flippable: true}, onValueChange() {}, label() {return 'Date added';}, showOrder}));
    assert.match(html, /aria-label="Date added, Newest first"/);
  }
});
test('menu and popover selections expose exclusive checked state rather than a hidden check alone', () => {
  const menu = renderToStaticMarkup(createElement(MenuItem, {selected: true}, 'Title'));
  assert.match(menu, /role="menuitemradio" aria-checked="true"/);
  const html = renderToStaticMarkup(createElement('div', {role: 'menu'},
    createElement(MenuRow, {role: 'menuitemradio', 'aria-checked': true, selected: true}, 'Large'),
    createElement(MenuRow, {role: 'menuitemradio', 'aria-checked': false, selected: false}, 'Small')));
  assert.equal((html.match(/role="menuitemradio"/g) ?? []).length, 2);
  assert.equal((html.match(/aria-checked="true"/g) ?? []).length, 1);
  assert.doesNotMatch(html, /aria-pressed=/);
});
