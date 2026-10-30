/**
 * Authored SOURCE for the pure overlay paint freeze. It exercises only the
 * model's own rules with fake elements; it is not a native host, caret or focus
 * proof and it is not run as part of this change. The stronger stale-timer
 * vectors stay with SB8 for the owner's product fix.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {freezeOverlayPaint, type OverlayPaintNode} from '../src-next/kit/overlays/overlay-paint-model.ts';

const View = 'View';
const Allowed = (_props: {title?: string}) => null;
const Unknown = () => null;
const Portal = () => null;

const child = (type: unknown, props: Record<string, unknown> = {}): OverlayPaintNode => React.createElement(type as React.ElementType, props);
/** A frozen children prop may be one element, an array, or absent. */
const kids = (node: unknown): Array<{type: unknown; props: Record<string, unknown>}> => {
  if (node === undefined || node === null || node === false) return [];
  return (Array.isArray(node) ? node : [node]).filter(Boolean) as Array<{type: unknown; props: Record<string, unknown>}>;
};

test('dropped function, ref and initialFocusRef keys are absent from the rebuilt element', () => {
  const calls: string[] = [];
  const rewrite = () => calls.push('rewrite');
  const root = child(View, {onPress: () => calls.push('press'), renderItem: rewrite, initialFocusRef: {current: null}, ref: {current: null}});
  const frozen = freezeOverlayPaint(root, [View]);
  assert.ok(frozen);
  assert.equal(frozen.type, View, 'the exact type is preserved');
  assert.equal(typeof frozen.props.onPress, 'function');
  assert.notEqual(frozen.props.onPress, root.props.onPress);
  assert.equal('renderItem' in frozen.props, false, 'a dropped function prop is absent, not undefined');
  assert.equal('initialFocusRef' in frozen.props, false);
  assert.equal('ref' in frozen.props, false, 'the rebuilt element carries no ref');
  (frozen.props.onPress as () => void)();
  assert.deepEqual(calls, []);
});

test('a root anchor or return object ref is retained, a function ref never is', () => {
  const nativeCurrent = {focus() {}};
  const anchorRef = {current: nativeCurrent}, returnFocusRef = {current: nativeCurrent};
  const frozen = freezeOverlayPaint(child(View, {anchorRef, returnFocusRef}), [View]);
  assert.ok(frozen);
  assert.equal(frozen.props.anchorRef, anchorRef, 'the real object ref keeps its permission');
  assert.equal(frozen.props.returnFocusRef, returnFocusRef);
  const functionRef = () => {};
  const withFunctionRef = freezeOverlayPaint(child(View, {anchorRef: functionRef, returnFocusRef: functionRef}), [View]);
  assert.ok(withFunctionRef);
  assert.equal('anchorRef' in withFunctionRef.props, false);
  assert.equal('returnFocusRef' in withFunctionRef.props, false);
});

test('nested elements and events inside prop data are frozen by the same traversal', () => {
  const calls: string[] = [];
  const nested = child(Allowed, {onPress: () => calls.push('nested')});
  const context = {item: {onSelect: () => calls.push('select'), label: 'Album'}, touchHeader: nested, count: 2};
  const frozen = freezeOverlayPaint(child(View, {value: context}), [View, Allowed]);
  assert.ok(frozen);
  const value = frozen.props.value as {item: {onSelect: unknown; label: string}; touchHeader: {type: unknown; props: Record<string, unknown>}; count: number};
  assert.equal(typeof value.item.onSelect, 'function', 'a nested event is stripped to a no-op, never rejected');
  (value.item.onSelect as () => void)();
  assert.equal(value.item.label, 'Album');
  assert.equal(value.count, 2);
  assert.equal(value.touchHeader.type, Allowed, 'a nested element is frozen in place');
  (value.touchHeader.props.onPress as () => void)();
  assert.deepEqual(calls, []);
  assert.notEqual(frozen.props.value, context, 'changed nested data is a new readonly object');
});

test('unchanged readonly data and shared aliases keep their identity, and undefined props are valid', () => {
  const style = {color: 'red'};
  const shared = {nested: {count: 2}};
  const uri = 'https://example.invalid/cover.jpg';
  const frozen = freezeOverlayPaint(child(View, {style, value: shared, other: shared, uri, explicit: undefined}), [View]);
  assert.ok(frozen);
  assert.equal(frozen.props.style, style);
  assert.equal(frozen.props.value, shared, 'an alias is not taken for a cycle');
  assert.equal(frozen.props.other, shared);
  assert.equal(frozen.props.uri, uri);
  assert.ok('explicit' in frozen.props);
  assert.equal(frozen.props.explicit, undefined, 'an undefined optional prop is a valid capture');
});

test('a direct omitted child leaves no child, while sibling children survive', () => {
  const direct = freezeOverlayPaint(child(View, {children: child(Portal, {})}), [View], [Portal]);
  assert.ok(direct);
  assert.equal(direct.props.children, undefined, 'the omitted child is absent, not a symbol');
  const siblings = freezeOverlayPaint(child(View, {children: [child(Portal, {}), child(Allowed, {})]}), [View, Allowed], [Portal]);
  assert.ok(siblings);
  assert.equal(kids(siblings.props.children).length, 1);
  assert.equal(kids(siblings.props.children)[0]!.type, Allowed);
});

test('descendant anchor and return refs are dropped even as plain ref objects', () => {
  const anchorRef = {current: null}, returnFocusRef = {current: null};
  const nested = child(Allowed, {anchorRef, returnFocusRef});
  const frozen = freezeOverlayPaint(child(View, {children: nested}), [View, Allowed], []);
  assert.ok(frozen);
  assert.equal(kids(frozen.props.children)[0]!.type, Allowed);
  assert.equal('anchorRef' in kids(frozen.props.children)[0]!.props, false);
  assert.equal('returnFocusRef' in kids(frozen.props.children)[0]!.props, false);
});

test('accessors are rejected without being invoked, including indexed array accessors', () => {
  let reads = 0;
  const value = {};
  Object.defineProperty(value, 'danger', {enumerable: true, get() {reads++; return 1;}});
  assert.equal(freezeOverlayPaint(child(View, {value}), [View]), undefined);
  const indexed: unknown[] = [1, 2, 3];
  Object.defineProperty(indexed, '1', {enumerable: true, get() {reads++; return 2;}});
  assert.equal(freezeOverlayPaint(child(View, {value: indexed}), [View]), undefined);
  assert.equal(reads, 0, 'no getter ran during the audit');
});

test('the shared visit budget refuses an exhausted capture, including primitive-heavy data', () => {
  const wide = Array.from({length: 5000}, (_, index) => ({index}));
  assert.equal(freezeOverlayPaint(child(View, {value: wide}), [View]), undefined);
  const primitives = Array.from({length: 5000}, () => 'title');
  assert.equal(freezeOverlayPaint(child(View, {value: primitives}), [View]), undefined);
});

test('unknown components, function children, functional styles and cycles are rejected', () => {
  assert.equal(freezeOverlayPaint(child(Unknown, {}), [View]), undefined);
  assert.equal(freezeOverlayPaint(child(View, {children: () => null}), [View]), undefined);
  assert.equal(freezeOverlayPaint(child(View, {style: {color: () => 'red'}}), [View]), undefined);
  assert.equal(freezeOverlayPaint(child(View, {style: [{color: 'red'}, {padding: () => 1}]}), [View]), undefined);
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;
  assert.equal(freezeOverlayPaint(child(View, {value: cyclic}), [View]), undefined);
});

test('an unlisted fragment or native host is rejected unless the caller lists it', () => {
  const fragment = child(React.Fragment, {children: child(Allowed, {})});
  assert.equal(freezeOverlayPaint(fragment, [Allowed]), undefined);
  assert.ok(freezeOverlayPaint(fragment, [React.Fragment, Allowed]));
  assert.equal(freezeOverlayPaint(child('Text', {children: 'hi'}), [View]), undefined);
  assert.ok(freezeOverlayPaint(child('Text', {children: 'hi'}), ['Text']));
});

test('a keyed React element keeps its key without reading the development key accessor', () => {
  const root = child(View, {key: 'overlay-row', onPress: () => {}});
  const frozen = freezeOverlayPaint(root, [View]);
  assert.ok(frozen);
  assert.equal(frozen.key, 'overlay-row');
  assert.equal('key' in frozen.props, false, 'the reserved key never enters props');
});

test('an undefined optional style is valid while a functional one rejects', () => {
  assert.ok(freezeOverlayPaint(child(View, {style: undefined}), [View]));
  assert.equal(freezeOverlayPaint(child(View, {style: () => ({color: 'red'})}), [View]), undefined);
});

test('a __proto__ key and symbol overrides are refused without touching a prototype', () => {
  const malicious = JSON.parse('{"__proto__": {"polluted": true}, "safe": 1}');
  assert.equal(freezeOverlayPaint(child(View, {value: malicious}), [View]), undefined);
  assert.equal(({} as {polluted?: boolean}).polluted, undefined);
  const symbols = [1, 2];
  Object.defineProperty(symbols, Symbol.iterator, {configurable: true, get() {throw new Error('iterator ran');}});
  assert.equal(freezeOverlayPaint(child(View, {value: symbols}), [View]), undefined);
});

test('a shared readonly style alias is legal in two places', () => {
  const style = {color: 'red'};
  const frozen = freezeOverlayPaint(child(View, {style, children: child(Allowed, {style})}), [View, Allowed]);
  assert.ok(frozen);
  assert.equal(frozen.props.style, style);
  assert.equal(kids(frozen.props.children)[0]!.props.style, style);
});

test('an accessor marker is rejected before any element probe runs', () => {
  let reads = 0;
  const data: Record<PropertyKey, unknown> = {safe: 1};
  Object.defineProperty(data, '$$typeof', {enumerable: true, get() {reads++; return Symbol.for('react.element');}});
  assert.equal(freezeOverlayPaint(child(View, {value: data}), [View]), undefined);
  assert.equal(reads, 0, 'the marker getter never ran');
});

test('a non-enumerable function inside plain data refuses the capture', () => {
  const data: Record<string, unknown> = {safe: 1};
  Object.defineProperty(data, 'hidden', {enumerable: false, value: () => 1});
  assert.equal(freezeOverlayPaint(child(View, {value: data}), [View]), undefined);
});

// Owner composition: real pinned React19 tags and hidden executable array methods.
test('real React elements retain their type and key under the pinned React tag', () => {
  const element = React.createElement(Allowed, {key: 'original', title: 'real'});
  const paint = freezeOverlayPaint(element as never, [Allowed]);
  assert.ok(paint); assert.equal(paint.type, Allowed); assert.equal(paint.key, 'original');
});
test('array subclasses and extra methods cannot survive through unchanged data identity', () => {
  const extra = [1, 2]; Object.defineProperty(extra, 'map', {value: () => {throw Error('must_not_run');}});
  const subclass = new (class extends Array<number> {})(1, 2);
  for (const values of [extra, subclass]) assert.equal(freezeOverlayPaint(child(View, {values}) as never, [View]), undefined);
});
test('hidden callback objects and root prototype keys refuse capture without invocation', () => {
  const values = {}; Object.defineProperty(values, 'hidden', {value: {onSelect: () => {throw Error('must_not_run');}}});
  assert.equal(freezeOverlayPaint(child(View, {values}) as never, [View]), undefined);
  const props = Object.create(null); Object.defineProperty(props, '__proto__', {value: {poison: true}, enumerable: true});
  const raw = {...child(View), props};
  assert.equal(freezeOverlayPaint(raw as never, [View]), undefined);
});
