import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
import type {ReactElement} from 'react';

// Exercise the actual renderer under Node, including its real Icon and CSS-module imports.
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SelectionCircle} = await import('../../src/kit/selection/SelectionCircle.tsx');
const {SelectionHoldFeedback} = await import('../../src/kit/selection/SelectionHoldFeedback.tsx');
const {selectionPickerTitleParts, selectionText} = await import('../../src/kit/selection/copy.ts');
const {PickerAddedToast} = await import('../../src/kit/selection/SelectionPicker.tsx');

test('picker toast uses the public text action and actual inserted rather than processed counts', () => {
  const viewed: string[] = [];
  const result = {destination: {id: 'playlist-1', name: 'Family night'}, inserted: 2, processed: 4, total: 4, alreadyIn: 2, failed: 0, unknown: 0, pending: false, complete: true, failures: []};
  const element = PickerAddedToast({open: true, onOpenChange() {}, selection: {kind: 'movie', count: 4}, destination: 'playlist', result, onView: id => viewed.push(id)});
  const toast = element.props.toast;
  assert.equal(element.props.className, 'pickerToastHost');
  assert.equal(element.props.toastClassName, 'pickerToast');
  assert.equal(toast.action.variant, 'text');
  assert.equal(renderToStaticMarkup(toast.message), 'Added 2 movies to Family night. 2 were already there.');
  for (const destination of ['playlist', 'collection'] as const) {
    const action = PickerAddedToast({open: true, onOpenChange() {}, selection: {kind: 'movie', count: 4}, destination, result, onView() {}}).props.toast!.action!;
    const fullLabel = destination === 'playlist' ? 'View playlist' : 'View collection';
    assert.equal(renderToStaticMarkup(action.label), fullLabel);
    assert.equal(action.accessibilityLabel, fullLabel);
    const previousWindow = Object.getOwnPropertyDescriptor(globalThis, 'window');
    try {
      Object.defineProperty(globalThis, 'window', {configurable: true, value: {matchMedia: (query: string) => ({matches: query === '(max-width: 599px)'})}});
      assert.equal(renderToStaticMarkup(action.label), 'View');
    } finally {
      if (previousWindow) Object.defineProperty(globalThis, 'window', previousWindow);
      else Reflect.deleteProperty(globalThis, 'window');
    }
  }
  toast.action.onAction();
  assert.deepEqual(viewed, ['playlist-1']);
  assert.equal(PickerAddedToast({open: true, onOpenChange() {}, selection: {kind: 'movie', count: 4}, destination: 'playlist', result: {...result, failed: 1}, onView() {}}).props.toast, null);
});

test('picker emphasis follows the canonical items argument without matching user text', () => {
  for (const key of ['next.selection.pickerTitle', 'next.selection.newPickerTitle'] as const) {
    const values = {items: '4 movies', destination: 'playlist'};
    const parts = selectionPickerTitleParts(key, values);
    assert.equal(parts.map(part => part.text).join(''), selectionText(key, values));
    assert.deepEqual(parts.filter(part => part.argument === 'items').map(part => part.text), ['4 movies']);
  }
  const parts = selectionPickerTitleParts('next.selection.pickerTitle', {items: 'playlist', destination: 'playlist'});
  const heading = renderToStaticMarkup(createElement('div', {}, ...parts.map((part, index) => part.argument === 'items' ? createElement('b', {key: index}, part.text) : part.text)));
  assert.equal(heading, '<div>Add <b>playlist</b> to a playlist</div>');
});

test('the interactive circle is a named native button with controlled pressed and disabled states', () => {
  const html = renderToStaticMarkup(createElement(SelectionCircle, {selected: true, selecting: true, disabled: true, accessibilityLabel: 'Select the movie', name: 'selection', value: 'movie-1', size: 24}));
  assert.match(html, /^<button/);
  assert.match(html, /type="button"/);
  assert.match(html, /aria-label="Select the movie"/);
  assert.match(html, /aria-pressed="true"/);
  assert.match(html, /disabled=""/);
  assert.match(html, /name="selection"/);
  assert.match(html, /value="movie-1"/);
  assert.match(html, /--selection-circle-size:24px/);
});

test('a card-owned circle contributes no nested activation or focus target', () => {
  const html = renderToStaticMarkup(createElement('button', {type: 'button'}, createElement(SelectionCircle, {interactive: false, selected: true, selecting: true, accessibilityLabel: 'Select the movie'})));
  assert.equal((html.match(/<button/g) ?? []).length, 1);
  assert.match(html, /<span[^>]+aria-hidden="true"/);
  assert.doesNotMatch(html, /tabindex=|role=|aria-pressed=/);
});

test('inactive feedback paints nothing and unknown progress paints no arc', () => {
  assert.equal(renderToStaticMarkup(createElement(SelectionHoldFeedback, {active: false, progress: .5})), '');
  const html = renderToStaticMarkup(createElement(SelectionHoldFeedback, {active: true, progress: null}));
  assert.match(html, /Keep holding to select/);
  assert.doesNotMatch(html, /<circle|<svg|progressbar/);
});

test('active feedback paints only the supplied fraction and uses the exact catalogue hint', () => {
  const html = renderToStaticMarkup(createElement(SelectionHoldFeedback, {active: true, progress: .25}));
  assert.match(html, /stroke-dashoffset="0.75"/);
  assert.match(html, /Keep holding to select/);
  assert.doesNotMatch(html, /animate|duration/);
});

test('actual circle activation respects cancellation, stops parent activation and reports current controlled state', () => {
  const calls: unknown[] = [];
  const render = (SelectionCircle as unknown as {render: (props: object, ref: null) => ReactElement<{onClick: (event: object) => void}>}).render;
  const button = render({selected: true, selecting: true, accessibilityLabel: 'Select', onSelectedChange: (...args: unknown[]) => calls.push(args)}, null);
  let stopped = false;
  button.props.onClick({defaultPrevented: false, shiftKey: false, stopPropagation() { stopped = true; }});
  assert.equal(stopped, true);
  assert.deepEqual(calls, [[false, {intent: 'toggle', shiftKey: false}]]);
  calls.length = 0;
  button.props.onClick({defaultPrevented: true, shiftKey: false, stopPropagation() { throw new Error('Cancelled click propagated into semantic activation.'); }});
  assert.deepEqual(calls, []);
  button.props.onClick({defaultPrevented: false, shiftKey: true, stopPropagation() {}});
  assert.deepEqual(calls, [[true, {intent: 'range', shiftKey: true}]]);
});
