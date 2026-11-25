// @fabric-react
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import test from 'node:test';
import {compileFunction} from 'node:vm';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';

const require = createRequire(import.meta.url);
const blurFile = new URL('../src-next/platform/native-blur.tsx', import.meta.url);
const compiledBlur = require('@babel/core').transformSync(await readFile(blurFile, 'utf8'), {
  filename: blurFile.pathname,
  configFile: false,
  babelrc: false,
  presets: [require.resolve('@react-native/babel-preset')],
  plugins: [require.resolve('@babel/plugin-transform-modules-commonjs')],
}).code;

type PlatformState = {OS: 'android' | 'ios'; isTV: boolean};

function loadBlur(platform: PlatformState, initialLite = false) {
  let lite = initialLite;
  const listeners = new Set<() => void>();
  const jsx = (type: unknown, {children, ...props}: Record<string, unknown>) =>
    React.createElement(type as any, props, ...(Array.isArray(children) ? children : children === undefined ? [] : [children]));
  const modules: Record<string, unknown> = {
    react: {...React, default: React},
    'react/jsx-runtime': {jsx, jsxs: jsx},
    'react-native': {Platform: platform, View: 'View'},
    '@react-native-community/blur': {BlurView: 'NativeBlurView'},
    './motion-policy': {
      getLiteEffects: () => lite,
      subscribeLiteEffects: (listener: () => void) => {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
    },
    './glass': {glassRecipe: () => ({}), phoneTabBarRecipe: () => ({})},
  };
  const mod = {exports: {} as Record<string, any>};
  compileFunction(compiledBlur, ['require', 'exports', 'module'], {filename: blurFile.pathname})(
    (name: string) => {
      if (name.startsWith('@babel/runtime/')) return require(name);
      assert.ok(Object.hasOwn(modules, name), `unexpected native-blur dependency ${name}`);
      return {__esModule: true, ...(modules[name] as object)};
    },
    mod.exports,
    mod,
  );
  return {
    ...(mod.exports as {
      BlurView: any;
      appleMaterialFor: (amount: number | undefined, style: string | undefined) => string | undefined;
      DEFAULT_TRANSLUCENT_FALLBACK: string;
    }),
    setLite(next: boolean) {
      lite = next;
      listeners.forEach(listener => listener());
    },
  };
}

async function renderBlur(BlurView: any, props: Record<string, unknown>, ref: unknown = null) {
  let tree: React.ReactElement<Record<string, any>> | undefined;
  const host = createFabricHookHost(() => {
    tree = BlurView.render(props, ref);
  });
  await act(() => host.mount());
  return {
    get tree() {return tree!;},
    host,
    async unmount() {await act(() => host.unmount());},
  };
}

test('appleMaterialFor maps authored Apple strengths only for absent or dark styles', () => {
  const {appleMaterialFor} = loadBlur({OS: 'ios', isTV: false});
  assert.equal(appleMaterialFor(18, undefined), 'ultraThinMaterialDark');
  assert.equal(appleMaterialFor(24, 'dark'), 'thinMaterialDark');
  assert.equal(appleMaterialFor(30, 'dark'), 'materialDark');
  assert.equal(appleMaterialFor(20, 'dark'), undefined);
  assert.equal(appleMaterialFor(24, 'light'), undefined);
  assert.equal(appleMaterialFor(30, 'chromeMaterialDark'), undefined);
});

test('Android renders a translucent View using reducedTransparencyFallbackColor or default dark glass tint', async () => {
  const {BlurView, DEFAULT_TRANSLUCENT_FALLBACK} = loadBlur({OS: 'android', isTV: false});
  assert.equal(DEFAULT_TRANSLUCENT_FALLBACK, 'rgba(12, 24, 34, 0.88)');

  const child = React.createElement('Child');
  const ref = React.createRef();
  const authored = await renderBlur(
    BlurView,
    {style: {flex: 1}, pointerEvents: 'none', blurType: 'dark', blurAmount: 24, reducedTransparencyFallbackColor: '#102030', children: child},
    ref,
  );
  try {
    assert.equal(authored.tree.type, 'View');
    assert.equal(authored.tree.props.ref, ref);
    assert.equal(authored.tree.props.pointerEvents, 'none');
    assert.deepEqual(authored.tree.props.style, [{flex: 1}, {backgroundColor: '#102030'}]);
    assert.equal(authored.tree.props.children, child);
  } finally {
    await authored.unmount();
  }

  const defaulted = await renderBlur(BlurView, {style: {position: 'absolute'}, blurType: 'dark', blurAmount: 18});
  try {
    assert.equal(defaulted.tree.type, 'View');
    assert.deepEqual(defaulted.tree.props.style, [{position: 'absolute'}, {backgroundColor: 'rgba(12, 24, 34, 0.88)'}]);
  } finally {
    await defaulted.unmount();
  }
});

test('Apple platforms keep hardware NativeBlurView and switch to translucent View when lite effects is enabled', async () => {
  const platform: PlatformState = {OS: 'ios', isTV: false};
  const {BlurView, setLite} = loadBlur(platform, false);

  const ios = await renderBlur(BlurView, {
    style: {flex: 1},
    blurType: 'dark',
    blurAmount: 24,
    reducedTransparencyFallbackColor: '#0d1b25',
  });
  try {
    assert.equal(ios.tree.type, 'NativeBlurView');
    assert.equal(ios.tree.props.blurType, 'thinMaterialDark');
    assert.equal(ios.tree.props.blurAmount, 24);

    // Enabling lite effects dynamically swaps iOS BlurView to the translucent View surface.
    await act(() => setLite(true));
    assert.equal(ios.tree.type, 'View');
    assert.deepEqual(ios.tree.props.style, [{flex: 1}, {backgroundColor: '#0d1b25'}]);

    await act(() => setLite(false));
    assert.equal(ios.tree.type, 'NativeBlurView');
    assert.equal(ios.tree.props.blurType, 'thinMaterialDark');
  } finally {
    await ios.unmount();
  }

  // tvOS keeps hardware UIBlurEffect with the caller's blurType (no iOS-only material rewrite).
  platform.isTV = true;
  const tvos = await renderBlur(BlurView, {
    style: {flex: 1},
    blurType: 'dark',
    blurAmount: 30,
  });
  try {
    assert.equal(tvos.tree.type, 'NativeBlurView');
    assert.equal(tvos.tree.props.blurType, 'dark');
    assert.equal(tvos.tree.props.blurAmount, 30);

    await act(() => setLite(true));
    assert.equal(tvos.tree.type, 'View');
    assert.deepEqual(tvos.tree.props.style, [{flex: 1}, {backgroundColor: 'rgba(12, 24, 34, 0.88)'}]);
  } finally {
    await tvos.unmount();
  }
});
