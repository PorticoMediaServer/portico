import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {Toast, Banner} = await import('../../src/kit/overlays/Feedback.tsx');

for (const variant of [undefined, 'glass', 'text'] as const) {
  test(`toast action ${variant ?? 'omitted variant'} renders a named, enabled real Button`, () => {
    const html = renderToStaticMarkup(createElement(Toast, {
      open: true, message: 'Added 2 movies', onOpenChange() {},
      action: {label: 'View playlist', accessibilityLabel: 'Open Family night playlist', icon: 'i-listadd', variant, onAction() {}},
    }));
    assert.match(html, /role="status" aria-live="polite" aria-atomic="true"/);
    assert.equal((html.match(/<button/g) ?? []).length, 1);
    assert.match(html, new RegExp(`data-variant="${variant ?? 'glass'}"`));
    assert.match(html, /type="button"/);
    assert.match(html, /data-size="xs"/);
    assert.match(html, /aria-label="Open Family night playlist"/);
    assert.match(html, /href="#i-listadd"/);
    assert.match(html, /<span class="buttonLabel">View playlist<\/span>/);
    assert.doesNotMatch(html, /disabled=|tabindex="-1"/);
  });
}

test('an actionless toast retains its live status without adding a focus target', () => {
  const html = renderToStaticMarkup(createElement(Toast, {open: true, message: 'Saved', onOpenChange() {}}));
  assert.match(html, /role="status"/);
  assert.doesNotMatch(html, /<button|tabindex=/);
});

test('notice banners retain their existing primary action', () => {
  const html = renderToStaticMarkup(createElement(Banner, {action: {label: 'Try again', onAction() {}}}, 'Connection failed'));
  assert.match(html, /data-variant="primary"/);
});
