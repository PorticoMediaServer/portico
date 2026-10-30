// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {shellLayout} from '../src-next/shell/model';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {createNativeMeasurementRegistry} from '../src-next/dev/layout-dump/registry';
import * as entryRevision from '../src-next/tv/auth/entry-revision';

async function observationModule() {
  const react = {...React, default: React};
  const hostRef = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react});
  return screenModule(new URL('../src-next/kit/dev-observation.ts', import.meta.url), {react,
    'react-native': {findNodeHandle: (host: {tag: number}) => host.tag}, './focus/useHostRef': hostRef});
}

const flatten = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value;
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

test('the TV front door renders its wordmark and only actual authority-choice callbacks without starting either flow', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'require'), previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
  Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {assert.equal(path, '../../../assets/wordmark.png'); return 'wordmark';}});
  let host: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    const calls: string[] = [], entries: any[] = [], theme = createTheme({isTV: true, width: 1920});
    let dimensions = {width: 1920, height: 1080}, tree: any, flattenCalls = 0;
    const {TVFrontDoor} = await screenModule(new URL('../src-next/tv/auth/TVFrontDoor.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {Image: 'Image', Text: 'Text', View: 'View', useWindowDimensions: () => dimensions, StyleSheet: {flatten: (style: unknown) => {flattenCalls++; return flatten(style);}}},
      '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {ColourBackdrop: 'Backdrop'}, '../../kit/controls': {Button: 'Button'},
      '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/theme': {nativeType, useTheme: () => theme},
      '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: (activation: unknown, address: unknown) => entries.push({activation, address})},
      '../../shell/model': {shellLayout}, '../../kit/dev-observation': await observationModule(),
      './entry-revision': entryRevision,
    });
    const props = {busy: false, onPortico: () => calls.push('portico'), onLocal: () => calls.push('local')};
    host = createFabricHookHost(() => {const provider = TVFrontDoor(props); assert.equal(provider.type, 'Provider'); const child = provider.props.children; tree = child.type(child.props);});
    await act(() => host!.mount());
    assert.deepEqual(calls, [], 'mounting does not contact Portico Services or a media server');
    assert.equal(flattenCalls, 0, 'production does not evaluate diagnostic style flattening');
    const all = nodes(tree), logo = all.find(node => node.type === 'Image');
    assert.equal(tree.props.ref, undefined, 'the production root has no collector ref');
    assert.equal(logo.props.ref, undefined, 'production preserves the original unforwarded Image ref');
    assert.equal(logo.props.source, 'wordmark'); assert.equal(logo.props.accessibilityLabel, defaultI18n.t('accounts.brand'));
    const buttons = all.filter(node => node.type === 'Button');
    assert.deepEqual(buttons.map(node => node.props.children), [defaultI18n.t('accounts.signIn.portico'), defaultI18n.t('accounts.signIn.local')]);
    assert.equal(buttons.length, 2, 'no code, terms, provider or extra Back control appears at the front door');
    assert.ok(buttons.every(node => node.props.loading === undefined), 'idle labels reserve no undrawn loading-glyph slot');
    assert.deepEqual(entries[0].address, {regionId: 'tv.auth.front-door', childId: 'portico'});
    assert.equal(all.find(node => node.type === 'Region').props.orientation, 'horizontal');
    buttons[0].props.onPress(); buttons[1].props.onPress(); assert.deepEqual(calls, ['portico', 'local']);
    const heading = all.find(node => node.props.accessibilityRole === 'header');
    assert.equal(heading.props.style[1].marginBottom, 56);
    assert.equal(heading.props.ref, undefined); assert.equal(heading.props.onTextLayout, undefined); assert.equal(heading.props.onLayout, undefined);
    assert.equal(all.find(node => node.type === 'Region').props.style.gap, 36);
    assert.deepEqual(logo.props.style, {position: 'absolute', left: 0, top: 72, width: 174, height: 40});
    assert.ok(all.some(node => node.props.style?.left === 200 && node.props.style?.width === 900));
    dimensions = {width: 1280, height: 800}; props.busy = true;
    await act(() => host!.rerender());
    const canvas = nodes(tree).find(node => node.props.style?.transformOrigin === 'top left');
    assert.equal(canvas.props.style.width, 1920); assert.equal(canvas.props.style.height, 1080);
    assert.deepEqual(canvas.props.style.transform, [{scale: 2 / 3}]); assert.equal(canvas.props.style.top, 40);
    nodes(tree).filter(node => node.type === 'Button').forEach(node => {assert.equal(node.props.disabled, true); node.props.onPress();});
    assert.deepEqual(calls, ['portico', 'local']);
    assert.equal(entries.at(-1).activation.visible, false);
    assert.equal(flattenCalls, 0, 'production rerenders preserve the zero-evaluation diagnostic boundary');
  } finally {
    await host?.unmount();
    if (previous) Object.defineProperty(globalThis, 'require', previous); else delete (globalThis as {require?: unknown}).require;
    if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else delete (globalThis as {__DEV__?: unknown}).__DEV__;
  }
});


test('DEV TV heading records only native text events, invalidates changed layout and unregisters replaced/unmounted hosts', async () => {
  const beforeRequire = Object.getOwnPropertyDescriptor(globalThis, 'require'), beforeDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
  const registry = createNativeMeasurementRegistry(), requires: string[] = [];
  Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: true});
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {
    requires.push(path);
    if (path === '../../../assets/wordmark.png') return 'wordmark';
    assert.ok(path === '../../dev/layout-dump/index' || path === '../dev/layout-dump/index');
    return {registerNativeHost: registry.register};
  }});
  let host: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    let dimensions = {width: 1920, height: 1080}, theme = createTheme({isTV: true, width: 1920}), tree: any;
    const {TVFrontDoor} = await screenModule(new URL('../src-next/tv/auth/TVFrontDoor.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {Image: 'Image', Text: 'Text', View: 'View', useWindowDimensions: () => dimensions,
        StyleSheet: {flatten}, findNodeHandle: (native: {tag: number}) => native.tag},
      '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {ColourBackdrop: 'Backdrop'}, '../../kit/controls': {Button: 'Button'},
      '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/theme': {nativeType, useTheme: () => theme},
      '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: () => {}}, '../../shell/model': {shellLayout},
      '../../kit/dev-observation': await observationModule(),
      './entry-revision': entryRevision,
    });
    const props = {busy: false, onPortico: () => {throw new Error('unexpected sign-in');}, onLocal: () => {throw new Error('unexpected local sign-in');}};
    host = createFabricHookHost(() => {const provider = TVFrontDoor(props); const child = provider.props.children; tree = child.type(child.props);});
    await act(() => host!.mount());
    const heading = () => nodes(tree).find(node => node.props.accessibilityRole === 'header');
    const image = () => nodes(tree).find(node => node.type === 'Image');
    const wordmark = {tag: 301}, replacementWordmark = {tag: 302};
    image().props.ref(wordmark);
    assert.equal(registry.read(wordmark)?.kind, 'image');
    assert.equal(registry.read(wordmark)?.semanticId, 'tv-front-door-wordmark');
    assert.equal(registry.read(wordmark)?.declaredStyleComplete, true);
    assert.deepEqual(registry.read(wordmark)?.flattenedStyle, image().props.style, 'registration declares exactly the existing Image style');
    assert.deepEqual(registry.read(wordmark)?.measuredStyle, {}, 'asset contents and paint are not observed by this hook');
    const imageRef = image().props.ref;
    imageRef(replacementWordmark);
    assert.equal(registry.read(wordmark), undefined);
    assert.equal(registry.read(replacementWordmark)?.kind, 'image');
    const root = {tag: 201}, replacementRoot = {tag: 202};
    tree.props.ref(root);
    assert.equal(registry.read(root)?.kind, 'view');
    assert.equal(registry.read(root)?.semanticId, 'tv-front-door');
    assert.equal(registry.read(root)?.declaredStyleComplete, true);
    assert.deepEqual(registry.read(root)?.flattenedStyle, {backgroundColor: tree.props.style.backgroundColor}, 'the root declares its actual theme background, not observed paint');
    assert.deepEqual(registry.read(root)?.measuredStyle, {});
    tree.props.ref(replacementRoot);
    assert.equal(registry.read(root), undefined);
    assert.equal(registry.read(replacementRoot)?.semanticId, 'tv-front-door');
    assert.equal(nodes(tree).filter(node => node.type === 'Text').length, 1, 'no body node is added');
    const first = {tag: 101}, replacement = {tag: 102};
    heading().props.ref(first);
    const observed = [{x: 0, y: 2, width: 630, height: 88, text: defaultI18n.t('web.signin.welcomeHome'), ascender: 70}];
    const event = (tag: number) => ({nativeEvent: {target: tag, lines: observed}});
    heading().props.onTextLayout(event(first.tag));
    assert.deepEqual(registry.read(first)?.lines, observed);
    assert.equal(registry.read(first)?.semanticId, 'tv-front-door-title');
    assert.equal(registry.read(first)?.declaredStyleComplete, true);
    assert.equal(registry.read(first)?.flattenedStyle.fontSize, 88);
    assert.deepEqual(registry.read(first)?.measuredStyle, {}, 'flattened inputs are never promoted to observed paint/fonts');
    assert.equal(registry.read(first)?.effectiveBackgroundColor, undefined);
    props.busy = true; await act(() => host!.rerender());
    assert.equal(image().props.ref, imageRef, 'the existing Image keeps one stable native callback');
    assert.deepEqual(registry.read(first)?.lines, observed, 'unchanged heading inputs retain its current text event');
    dimensions = {width: 1280, height: 800}; await act(() => host!.rerender());
    assert.equal(registry.read(first)?.lines, undefined);
    heading().props.onTextLayout(event(first.tag));
    heading().props.ref(replacement);
    assert.equal(registry.read(first), undefined);
    heading().props.onTextLayout(event(first.tag));
    assert.equal(registry.read(replacement)?.lines, undefined, 'a replaced native target cannot populate the current lease');
    heading().props.onTextLayout(event(replacement.tag));
    assert.deepEqual(registry.read(replacement)?.lines, observed);
    theme = {...theme, textScale: 1.25}; await act(() => host!.rerender());
    assert.equal(tree.props.style.backgroundColor, theme.colors.ink);
    assert.deepEqual(registry.read(replacementRoot)?.flattenedStyle, {backgroundColor: theme.colors.ink});
    assert.deepEqual(registry.read(replacementWordmark)?.flattenedStyle, image().props.style);
    assert.deepEqual(registry.read(replacementRoot)?.measuredStyle, {}, 'declared paint remains separate from native observations');
    assert.equal(registry.read(replacement)?.lines, undefined);
    assert.equal(registry.read(replacement)?.flattenedStyle.fontSize, 110);
    for (const current of [replacementRoot, replacementWordmark, replacement]) assert.equal(registry.read(current)?.declaredStyleComplete, true, 'the current final declaration stays complete after the theme update');
    heading().props.onTextLayout(event(replacement.tag));
    assert.equal(heading().props.onLayout, undefined, 'generic layout delivery cannot erase fresh native text lines');
    assert.deepEqual(registry.read(replacement)?.lines, observed);
    dimensions = {width: 1920, height: 1080}; await act(() => host!.rerender());
    assert.equal(registry.read(replacement)?.lines, undefined);
    assert.deepEqual(registry.read(replacement)?.measuredStyle, {});
    heading().props.onTextLayout(event(replacement.tag));
    await act(() => host!.unmount()); host = undefined;
    assert.equal(registry.read(replacement), undefined);
    assert.equal(registry.read(replacementWordmark), undefined, 'the existing Image lease retires on unmount');
    assert.equal(registry.read(replacementRoot), undefined, 'leaving this existing Welcome view retires the fixture root');
    heading().props.onTextLayout(event(replacement.tag));
    assert.equal(registry.read(replacement), undefined, 'late events after unmount cannot recreate a registration');
    assert.ok(requires.includes('../../dev/layout-dump/index'));
    assert.ok(requires.includes('../dev/layout-dump/index'));
  } finally {
    await host?.unmount();
    if (beforeRequire) Object.defineProperty(globalThis, 'require', beforeRequire); else delete (globalThis as {require?: unknown}).require;
    if (beforeDev) Object.defineProperty(globalThis, '__DEV__', beforeDev); else delete (globalThis as {__DEV__?: unknown}).__DEV__;
  }
});
