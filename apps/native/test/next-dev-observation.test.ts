// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {createNativeMeasurementRegistry} from '../src-next/dev/layout-dump/registry.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

function setGlobal(name: '__DEV__' | 'require', value: unknown) {
  const previous = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, {configurable: true, value});
  return () => {
    if (previous) Object.defineProperty(globalThis, name, previous);
    else delete (globalThis as Record<string, unknown>)[name];
  };
}

test('production useNativeObservation runs zero hooks/effects and reuses frozen module constants', async () => {
  const restoreDev = setGlobal('__DEV__', false);
  const failHook = (name: string) => () => {throw new Error(`production must not call ${name}`);};
  try {
    const {useNativeObservation} = await screenModule(
      new URL('../src-next/kit/dev-observation.ts', import.meta.url),
      {
        react: {
          useRef: failHook('useRef'),
          useCallback: failHook('useCallback'),
          useLayoutEffect: failHook('useLayoutEffect'),
        },
        'react-native': {findNodeHandle: failHook('findNodeHandle')},
        './focus/useHostRef': {useHostRef: failHook('useHostRef')},
      },
    );

    const forwarded = React.createRef<any>();
    const first = useNativeObservation({kind: 'view', semanticId: 'card'}, forwarded);
    const second = useNativeObservation({kind: 'text', semanticId: 'label'}, undefined, 'rev-1');

    assert.equal(first.ref, forwarded);
    assert.equal(second.ref, undefined);
    assert.equal(first.textProps, second.textProps, 'empty textProps is a shared module constant');
    assert.deepEqual(first.textProps, {});
    assert.equal(Object.isFrozen(first.textProps), true);
    assert.equal(first.recordFocus, second.recordFocus, 'recordFocus is a shared no-op');
    assert.equal(first.recordDeclaredStyle, second.recordDeclaredStyle, 'recordDeclaredStyle is a shared no-op');

    // Calling no-ops is safe and has no side effects.
    assert.equal(first.recordFocus({nativeEvent: {target: 42}}, true), undefined);
    assert.equal(first.recordDeclaredStyle({backgroundColor: '#000'}), undefined);
  } finally {
    restoreDev();
  }
});

test('DEV useNativeObservation registers hosts, records text/focus/declared styles, and unregisters on unmount', async () => {
  const registry = createNativeMeasurementRegistry();
  const restoreDev = setGlobal('__DEV__', true);
  const restoreRequire = setGlobal('require', (specifier: string) => {
    assert.equal(specifier, '../dev/layout-dump/index');
    return {registerNativeHost: registry.register};
  });
  let host: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    const react = {...React, default: React};
    const hostRef = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react});
    const {useNativeObservation} = await screenModule(
      new URL('../src-next/kit/dev-observation.ts', import.meta.url),
      {
        react,
        'react-native': {findNodeHandle: (node: {tag: number}) => node.tag},
        './focus/useHostRef': hostRef,
      },
    );

    const forwarded: Array<unknown> = [];
    let revision = 'a';
    let current: any;
    host = createFabricHookHost(() => {
      current = useNativeObservation(
        {kind: 'text', semanticId: 'heading', flattenedStyle: {fontSize: 20}, declaredStyleComplete: true},
        (node: unknown) => forwarded.push(node),
        revision,
      );
    });

    await act(() => host!.mount());
    const nativeNode = {tag: 77};
    current.textProps.ref(nativeNode);
    assert.deepEqual(forwarded, [nativeNode]);
    assert.equal(registry.read(nativeNode)?.semanticId, 'heading');

    const lines = [{x: 0, y: 0, width: 120, height: 24, text: 'Portico'}];
    current.textProps.onTextLayout({nativeEvent: {target: 77, lines}});
    assert.deepEqual(registry.read(nativeNode)?.lines, lines);

    current.recordFocus({nativeEvent: {target: 77}}, true);
    assert.equal(registry.actualFocus(nativeNode, new Set([nativeNode])), true);

    current.recordDeclaredStyle({fontSize: 24});
    assert.deepEqual(registry.read(nativeNode)?.flattenedStyle, {fontSize: 24});

    revision = 'b';
    await act(() => host!.rerender());
    assert.equal(registry.read(nativeNode)?.lines, undefined, 'textRevision invalidates recorded lines');

    await act(() => host!.unmount());
    host = undefined;
    assert.equal(registry.read(nativeNode), undefined, 'unmount unregisters host lease');
  } finally {
    await host?.unmount();
    restoreRequire();
    restoreDev();
  }
});
