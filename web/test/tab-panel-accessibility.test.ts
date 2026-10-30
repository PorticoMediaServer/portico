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
const {Tabs, TabsParts} = await import('../src/kit/controls/index.ts');

test('sticky title tabs point to the separately drawn active panel without losing Radix semantics', () => {
  const html = renderToStaticMarkup(createElement(Tabs, {externalPanelId: 'show-panel', value: 'episodes',
    items: [{value: 'episodes', label: 'Episodes'}, {value: 'details', label: 'Details'}]}));
  const tabs = html.match(/<button[^>]*role="tab"[^>]*>/g) ?? [];
  assert.equal(tabs.length, 2);
  assert.match(tabs[0], /id="show-panel-tab-episodes"/);
  assert.match(tabs[0], /aria-controls="show-panel"/);
  assert.match(tabs[0], /aria-selected="true"/);
  assert.match(tabs[1], /aria-selected="false"/);
});
test('unstyled editor tabs preserve their nav/buttons and link the selected panel both ways', () => {
  const html = renderToStaticMarkup(createElement(TabsParts.Root, {value: 'general'},
    createElement(TabsParts.List, {asChild: true}, createElement('nav', {className: 'editorTabs'},
      createElement(TabsParts.Trigger, {value: 'general', asChild: true}, createElement('button', {type: 'button'}, 'General')),
      createElement(TabsParts.Trigger, {value: 'artwork', asChild: true}, createElement('button', {type: 'button'}, 'Artwork')))),
    createElement(TabsParts.Content, {value: 'general'}, 'Fields')));
  const tab = html.match(/<button[^>]*aria-selected="true"[^>]*>/)?.[0];
  const panel = html.match(/<div[^>]*role="tabpanel"[^>]*>/)?.[0];
  assert.ok(tab);assert.ok(panel);
  const id = tab.match(/id="([^"]+)"/)?.[1], controls = tab.match(/aria-controls="([^"]+)"/)?.[1];
  assert.ok(id);assert.ok(controls);
  assert.ok(panel.includes(`id="${controls}"`));
  assert.ok(panel.includes(`aria-labelledby="${id}"`));
  const nav = html.match(/<nav[^>]*>/)?.[0];
  assert.ok(nav);assert.match(nav, /role="tablist"/);assert.match(nav, /class="editorTabs"/);
});
