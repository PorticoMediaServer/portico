// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {createNativeMeasurementRegistry, type HostRegistration} from '../src-next/dev/layout-dump/registry';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {useHostRef} from '../src-next/kit/focus/useHostRef';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style;
const line = (text = 'Welcome home.') => ({x: 0, y: 0, width: 280, height: 44, text, ascender: 35, capHeight: 31, descender: 9, xHeight: 23});

async function fixture(dev: boolean) {
  const previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__'), previousRequire = Object.getOwnPropertyDescriptor(globalThis, 'require');
  const registry = createNativeMeasurementRegistry(), registrations: {host: object; lease: HostRegistration; facts: Parameters<typeof registry.register>[1]}[] = [];
  let imports = 0, tree: any, title = 'Welcome home.', textScale = 1;
  const actions: string[] = [], onPortico = () => {actions.push('portico');}, onLocal = () => {actions.push('local');};
  Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: dev});
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {
    if (path === '../../../assets/wordmark.png') return 'existing-wordmark';
    assert.ok(path === '../../dev/layout-dump/index' || path === '../dev/layout-dump/index'); imports++;
    assert.equal(dev, true, 'production must never load the collector');
    return {registerNativeHost: (host: object, facts: Parameters<typeof registry.register>[1]) => {
      const lease = registry.register(host, facts); registrations.push({host, lease, facts}); return lease;
    }};
  }});
  let host: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    const {useNativeObservation} = await screenModule(new URL('../src-next/kit/dev-observation.ts', import.meta.url), {
      react: {...React, default: React}, 'react-native': {findNodeHandle: () => null}, './focus/useHostRef': {useHostRef},
    });
    const {PhoneFrontDoor} = await screenModule(new URL('../src-next/screens/accounts/PhoneFrontDoor.tsx', import.meta.url), {
      react: {...React, default: React},
      'react-native': {Image: 'Image', ScrollView: 'ScrollView', Text: 'Text', View: 'View', StyleSheet: {flatten: (style: unknown) => {assert.equal(dev, true); return flatten(style);}}},
      'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
      '../../../../../packages/i18n/src': {defaultI18n: {...defaultI18n, t: (id: Parameters<typeof defaultI18n.t>[0]) => id === 'web.signin.welcomeHome' ? title : defaultI18n.t(id)}},
      '../../kit/artwork': {ColourBackdrop: 'Backdrop'}, '../../kit/controls': {Button: 'Button'},
      '../../kit/dev-observation': {useNativeObservation},
      '../../kit/theme': {nativeType, useTheme: () => createTheme({isTV: false, width: 390, textScale})},
    });
    host = createFabricHookHost(() => {tree = PhoneFrontDoor({busy: false, onPortico, onLocal});});
    await act(() => host!.mount());
    return {registry, registrations, imports: () => imports, actions, tree: () => tree,
      heading: () => nodes(tree).find(node => node.type === 'Text'),
      title: async (next: string) => {title = next; await act(() => host!.rerender());},
      scale: async (next: number) => {textScale = next; await act(() => host!.rerender());},
      close: async () => {await host?.unmount(); restore();},
    };
  } catch (error) {await host?.unmount(); restore(); throw error;}
  function restore() {
    if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else Reflect.deleteProperty(globalThis, '__DEV__');
    if (previousRequire) Object.defineProperty(globalThis, 'require', previousRequire); else Reflect.deleteProperty(globalThis, 'require');
  }
}

test('frontdoor heading records only actual native text lines and diagnostic style, invalidating stale layout and retiring host leases', async () => {
  const f = await fixture(true), nativeHost = {};
  try {
    const heading = f.heading(); heading.props.ref(nativeHost);
    assert.equal(f.imports(), 1); assert.equal(f.registry.read(nativeHost)!.kind, 'text');
    assert.equal(f.registry.read(nativeHost)!.semanticId, 'accounts-welcome-title');
    assert.equal(f.registry.read(nativeHost)!.declaredStyleComplete, true);
    assert.equal(f.registry.read(nativeHost)!.flattenedStyle.fontSize, 42);
    assert.deepEqual(f.registry.read(nativeHost)!.measuredStyle, {}, 'input styles never become observed native paint/fonts');
    assert.equal(await f.registry.measureTextRectangles(nativeHost), undefined, 'no host-box or scale-derived glyph rectangles');
    heading.props.onTextLayout({nativeEvent: {lines: [line()]}});
    assert.deepEqual(f.registry.read(nativeHost)!.lines, [line()]);
    heading.props.onLayout({nativeEvent: {layout: {x: 0, y: 0, width: 346, height: 44}}});
    assert.deepEqual(f.registry.read(nativeHost)!.lines, [line()], 'first layout does not discard an already-delivered native Text event');
    f.registrations[0]!.lease.recordResolvedStyle({fontFamily: 'Observed native fixture font', color: '#ffffff'});
    heading.props.onLayout({nativeEvent: {layout: {x: 0, y: 0, width: 300, height: 88}}});
    assert.equal(f.registry.read(nativeHost)!.lines, undefined); assert.deepEqual(f.registry.read(nativeHost)!.measuredStyle, {});
    heading.props.onTextLayout({nativeEvent: {lines: [line()]}});
    await f.title('Public translated welcome');
    assert.equal(f.registry.read(nativeHost), undefined, 'changed text retires the old native observations');
    f.heading().props.ref(nativeHost); heading.props.onTextLayout({nativeEvent: {lines: [line()]}});
    assert.equal(f.registry.read(nativeHost)!.lines, undefined, 'a retired text callback cannot publish to the new registration');
    f.heading().props.onTextLayout({nativeEvent: {lines: [line('Public translated welcome')]}});
    assert.deepEqual(f.registry.read(nativeHost)!.lines, [line('Public translated welcome')]);
    await f.scale(1.25); assert.equal(f.registry.read(nativeHost), undefined);
    f.heading().props.ref(nativeHost); assert.equal(f.registry.read(nativeHost)!.flattenedStyle.fontSize, 52.5);
    const replacement = {}; f.heading().props.ref(replacement);
    assert.equal(f.registry.read(nativeHost), undefined); assert.ok(f.registry.read(replacement));
    f.heading().props.ref(null); assert.equal(f.registry.read(replacement), undefined);
    f.heading().props.ref(nativeHost);
  } finally {await f.close();}
  assert.equal(f.registry.read(nativeHost), undefined, 'unmount retires the lease even without another native ref callback');
});

test('production frontdoor never loads measurement code or attaches callbacks and retains the existing controls/styles', async () => {
  const f = await fixture(false);
  try {
    const heading = f.heading(), theme = createTheme({isTV: false, width: 390});
    assert.deepEqual(f.tree().props.style, {flex: 1, backgroundColor: theme.colors.ink});
    assert.deepEqual(nodes(f.tree()).find(node => node.type === 'Image').props.style, {width: 17 * 512 / 117, height: 17, alignSelf: 'flex-start'});
    assert.equal(nodes(f.tree()).find(node => node.type === 'Image').props.ref, undefined);
    assert.equal(f.imports(), 0); assert.equal(f.tree().props.ref, undefined); assert.equal(heading.props.ref, undefined); assert.equal(heading.props.onLayout, undefined); assert.equal(heading.props.onTextLayout, undefined);
    assert.deepEqual(flatten(heading.props.style), {...nativeType({size: 42, lineHeight: 43.68, weight: 800, tracking: -.04}, theme.textScale), includeFontPadding: false, color: theme.colors.text, marginBottom: 22});
    assert.equal(nodes(f.tree()).filter(node => node.type === 'Text').length, 1, 'no body or wrapper markup is invented');
    const buttons = nodes(f.tree()).filter(node => node.type === 'Button');
    assert.deepEqual(buttons.map(button => button.props.testID), ['accounts-welcome-portico', 'accounts-welcome-local']);
    assert.deepEqual(buttons.map(button => button.props.style), Array(2).fill({alignSelf: 'stretch', height: 48, borderRadius: 11, paddingHorizontal: 20}));
    assert.deepEqual(f.actions, []); buttons[0].props.onPress(); buttons[1].props.onPress(); assert.deepEqual(f.actions, ['portico', 'local']);
  } finally {await f.close();}
});

test('the existing brand Image owns a DEV lease without changing its asset or presentation', async () => {
  const f = await fixture(true), first = {}, replacement = {};
  const image = () => nodes(f.tree()).find(node => node.type === 'Image');
  try {
    const {ref, ...props} = image().props;
    assert.deepEqual(props, {source: 'existing-wordmark', accessibilityLabel: defaultI18n.t('accounts.brand'), resizeMode: 'contain',
      style: {width: 17 * 512 / 117, height: 17, alignSelf: 'flex-start'}});
    ref(first);
    assert.equal(f.registry.read(first)!.kind, 'image');
    assert.equal(f.registry.read(first)!.semanticId, 'accounts-welcome-wordmark');
    assert.equal(f.registry.read(first)!.declaredStyleComplete, true);
    assert.deepEqual(f.registry.read(first)!.flattenedStyle, props.style, 'declared style is the same resolved style rendered by the Image');
    assert.deepEqual(f.registry.read(first)!.measuredStyle, {}, 'declared inputs are not native-observed paint');
    await f.title('Public translated welcome');
    assert.equal(image().props.ref, ref);
    assert.ok(f.registry.read(first));
    ref(replacement);
    assert.equal(f.registry.read(first), undefined); assert.ok(f.registry.read(replacement));
    ref(null); assert.equal(f.registry.read(replacement), undefined);
    const cleanup = ref(first);
    cleanup(); assert.equal(f.registry.read(first), undefined);
    ref(first);
  } finally {await f.close();}
  assert.equal(f.registry.read(first), undefined);
});


test('the existing welcome View owns a DEV scope lease that retires on replacement and unmount', async () => {
  const f = await fixture(true), first = {}, replacement = {};
  try {
    const root = f.tree();
    assert.equal(root.type, 'View'); assert.equal(root.props.testID, 'accounts-welcome');
    const ref = root.props.ref;
    assert.equal(typeof ref, 'function'); ref(first);
    assert.equal(f.registry.read(first)!.kind, 'view');
    assert.equal(f.registry.read(first)!.semanticId, 'accounts-welcome');
    assert.equal(f.registry.read(first)!.declaredStyleComplete, true);
    assert.deepEqual(f.registrations.find(registration => registration.host === first)!.facts.flattenedStyle, root.props.style, 'the callback supplies the same theme-resolved style rendered by the View');
    assert.deepEqual(f.registry.read(first)!.flattenedStyle, {backgroundColor: root.props.style.backgroundColor}, 'collector curation excludes flex layout from declared paint');
    assert.deepEqual(f.registry.read(first)!.measuredStyle, {}, 'declared inputs are not native-observed paint');
    await f.title('Public translated welcome');
    assert.equal(f.tree().props.ref, ref, 'existing live native host ref stays stable across title renders');
    assert.ok(f.registry.read(first));
    ref(replacement); assert.equal(f.registry.read(first), undefined); assert.ok(f.registry.read(replacement));
    ref(null); assert.equal(f.registry.read(replacement), undefined);
    const cleanup = ref(first);
    assert.equal(typeof cleanup, 'function'); cleanup(); assert.equal(f.registry.read(first), undefined, 'React ref cleanup retires scope');
    ref(first);
  } finally {await f.close();}
  assert.equal(f.registry.read(first), undefined, 'welcome unmount retires its scope without relying on the full NativeRoot disappearing');
});
