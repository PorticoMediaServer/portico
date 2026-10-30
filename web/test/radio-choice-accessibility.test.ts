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
const {RadioGroup, RadioItem, Chip} = await import('../src/kit/controls/index.ts');
const {WatchEndCardView} = await import('../src/screens/watch/EndCard.tsx');
const {stopRadioNavigation} = await import('../src/kit/controls/radio-navigation.ts');

test('unstyled Radix choices retain chip drawing with exclusive checked state and disabled choices', () => {
  const html = renderToStaticMarkup(createElement(RadioGroup, {value: 'movies', 'aria-label': 'Libraries'},
    createElement(RadioItem, {value: 'all', asChild: true}, createElement(Chip, {variant: 'quick', selected: false}, 'All')),
    createElement(RadioItem, {value: 'movies', asChild: true}, createElement(Chip, {variant: 'quick', selected: true}, 'Movies')),
    createElement(RadioItem, {value: 'books', disabled: true}, 'Books')));
  assert.match(html, /role="radiogroup"/);
  assert.equal((html.match(/role="radio"/g) ?? []).length, 3);
  assert.equal((html.match(/aria-checked="true"/g) ?? []).length, 1);
  assert.match(html, /data-variant="quick" data-selected="true"/);
  assert.match(html, /disabled=""/);
  assert.doesNotMatch(html, /aria-pressed=/);
});
test('the finished player exposes its actual stored rating as one checked radio with one tab stop', () => {
  const html = renderToStaticMarkup(createElement(WatchEndCardView, {
    card: {title: 'Film', related: [], rating: {value: 6, async set() {assert.fail('SSR cannot rate');}}, back() {}, openRelated() {}},
    canReplay: true, onReplay() {}, reportError(error: unknown) {throw error;}, artwork() {return undefined;},
  }));
  const radios = html.match(/<button[^>]*role="radio"[^>]*>/g) ?? [];
  assert.equal(radios.length, 5);
  assert.equal(radios.filter(row => row.includes('aria-checked="true"')).length, 1);
  assert.match(radios[2], /aria-checked="true"/);
  assert.equal(radios.filter(row => row.includes('tabindex="0"')).length, 1);
  assert.match(html, /class="stars"/);
});
test('radio navigation cannot reach seek or volume listeners while other keys still bubble', () => {
  for (const key of ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End', 'Tab', 'Escape', 'k']) {
    let stopped = false;
    stopRadioNavigation({key, stopPropagation() {stopped = true;}});
    assert.equal(stopped, !['Tab', 'Escape', 'k'].includes(key), key);
  }
});
