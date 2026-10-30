import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
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
const stylesheet = readFileSync(new URL('../src/kit/browse/browse.module.css', import.meta.url), 'utf8');

const capability = {id: 'date', labelKey: 'next.browse.sort.dateAdded', directions: ['asc', 'desc'] as const, defaultDirection: 'desc' as const, expensive: false, applicableKinds: [] as readonly string[]};
const selection = {field: 'date', direction: 'desc' as const, flippable: true};
const props = {sorts: [capability], value: selection, onValueChange: () => {}, label: () => 'Date added'};

function declarations(selector: string): string {
  const start = stylesheet.indexOf(`${selector} {`);
  assert.ok(start >= 0, `missing rule ${selector}`);
  return stylesheet.slice(start + selector.length, stylesheet.indexOf('}', start));
}

test('the saved sort chip draws the frame size: 15px inset and a 4px rule before the order word', () => {
  assert.ok(declarations('.sortTrigger[data-saved]').includes('padding: 0 15px'));
  assert.ok(declarations('.orderSeparator').includes('width: 4px'));
  const saved = renderToStaticMarkup(createElement(SortMenu, {...props, variant: 'saved'}));
  assert.match(saved, /data-saved="true"/);
  assert.match(saved, /<span class="orderSeparator" aria-hidden="true"><\/span><span class="direction">/);
  assert.match(saved, /href="#i-sort"><\/use><\/svg>/);
  assert.match(saved, /width="18" height="18"/);
  assert.match(saved, /width="16" height="16"/);
  assert.match(saved, /Date added/); assert.match(saved, /Newest first/);
});

test('the default sort chip is unchanged: no saved inset, no order rule, same words', () => {
  const standard = renderToStaticMarkup(createElement(SortMenu, props));
  assert.doesNotMatch(standard, /data-saved|orderSeparator/);
  assert.match(standard, /width="18" height="18"/);
  assert.match(standard, /width="16" height="16"/);
  assert.match(standard, /Date added/); assert.match(standard, /Newest first/);
  assert.doesNotMatch(stylesheet, /\.sortTrigger \{[^}]*padding: 0 15px/);
});
