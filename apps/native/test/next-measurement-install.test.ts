import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as collector from '../src-next/dev/layout-dump/index.ts';
import {createNativeTreeObserver} from '../src-next/dev/layout-dump/native-observer.ts';
import {nativeHost, setNativeObservation, observeNativeWindow, addNativeFixture, windowAPI} from './helpers/native-window-fixture.ts';

const globals = globalThis as typeof globalThis & {
  __DEV__?: boolean;
  __PORTICO_NATIVE_LAYOUT_DUMP__?: Pick<collector.NativeLayoutDump, 'capture'>;
};

test('App measurement command borrows only the current public root and live viewport from the real collector', async () => {
  const original = globals.__DEV__;
  globals.__DEV__ = true;
  let dispose: (() => void) | undefined;
  let retireFixture: (() => void) | undefined;
  try {
    let current = true, width = 390, height = 844, density = 3, fontScale = 1.25, reads = 0;
    const root = nativeHost('RN:View', {x: 0, y: 0, width, height});
    root.getBoundingClientRect = () => ({x: 0, y: 0, width, height});
    setNativeObservation(root, () => ({style: {}, unavailable: {}, windowRect: {x: 0, y: 0, width, height}, windowRectSource: windowAPI}));
    retireFixture = addNativeFixture(root, collector.registerNativeHost);
    const {installNativePublicMeasurement} = await screenModule(new URL('../src-next/dev/measurement-install.ts', import.meta.url), {
      react: {default: {}}, './layout-dump': {...collector, createAndroidNativeTreeObserver: () => observeNativeWindow},
      'react-native': {Platform: {OS: 'android', isTV: false}, Dimensions: {get: (kind: string) => {
        assert.equal(kind, 'window'); return {width, height};
      }}, PixelRatio: {get: () => density, getFontScale: () => fontScale}},
    });
    dispose = installNativePublicMeasurement(() => {reads++; return root;}, () => current);
    const command = globals.__PORTICO_NATIVE_LAYOUT_DUMP__!;
    assert.deepEqual(Object.keys(command), ['capture']);
    assert.equal(Object.isFrozen(command), true);
    const first = await command.capture();
    assert.deepEqual(first.viewport, {width: 390, height: 844, deviceScaleFactor: 3});
    assert.equal(first.diagnostics.fontScale, 1.25);
    assert.equal(first.fullViewport, true);
    width = 844; height = 390; density = 2; fontScale = 0.85;
    const second = await command.capture();
    assert.deepEqual(second.viewport, {width, height, deviceScaleFactor: density});
    assert.equal(second.diagnostics.fontScale, 0.85);
    assert.throws(() => installNativePublicMeasurement(() => root, () => true), /already installed/);
    current = false;
    const previousReads = reads;
    await assert.rejects(command.capture(), /mounted whole-screen native root/);
    assert.equal(reads, previousReads, 'retirement refuses before lending the root to the collector');
    assert.ok(dispose);
    dispose(); dispose = undefined;
    assert.equal(globals.__PORTICO_NATIVE_LAYOUT_DUMP__, undefined);
    await assert.rejects(command.capture(), /disposed/);
  } finally {
    dispose?.();
    retireFixture?.();
    if (original === undefined) delete globals.__DEV__; else globals.__DEV__ = original;
  }
});

test('App forwards the installed observer to the same public collector and rejects a late retired result', async () => {
  const original = globals.__DEV__;
  globals.__DEV__ = true;
  let dispose: (() => void) | undefined;
  let unregister: (() => void) | undefined;
  try {
    let current = true, constructions = 0;
    const fixture = {nodeType: 1, nodeName: 'RN:View', isConnected: true, childNodes: [],
      getBoundingClientRect: () => ({x: 0, y: 0, width: 390, height: 844})};
    const root = {...fixture, childNodes: [fixture]};
    const lease = collector.registerNativeHost(fixture, {kind: 'view', semanticId: 'accounts-welcome'});
    assert.ok(lease); unregister = lease.unregister;
    const calls: number[][] = [];
    let finish!: (value: unknown) => void;
    let entered!: () => void;
    const pendingRead = new Promise<void>(resolve => {entered = resolve;});
    const observer = createNativeTreeObserver({tagForHost: host => host === root ? 11 : host === fixture ? 12 : undefined,
      bridge: {observe: async (rootTag, fixtureTag, tags) => {
        assert.equal(rootTag, 11); assert.equal(fixtureTag, 12);
        calls.push([...tags]);
        const response = {version: 1, unit: 'dp', rootReactTag: rootTag, fixtureReactTag: fixtureTag,
          hosts: tags.map(reactTag => ({reactTag, facts: {isFocused: {value: false, source: 'android.View.isFocused'},
            windowRect: {value: {x: 0, y: 0, width: 390, height: 844}, source: windowAPI}}}))};
        if (calls.length === 1) return response;
        entered();
        return await new Promise<unknown>(resolve => {finish = resolve;});
      }}});
    const {installNativePublicMeasurement} = await screenModule(new URL('../src-next/dev/measurement-install.ts', import.meta.url), {
      react: {default: {}}, './layout-dump': {...collector, createAndroidNativeTreeObserver: () => {constructions++; return observer;}},
      'react-native': {Platform: {OS: 'android', isTV: false}, Dimensions: {get: () => ({width: 390, height: 844})},
        PixelRatio: {get: () => 3, getFontScale: () => 1}},
    });
    dispose = installNativePublicMeasurement(() => root, () => current);
    const command = globals.__PORTICO_NATIVE_LAYOUT_DUMP__!;
    assert.equal(constructions, 1); assert.equal(calls.length, 0, 'install does not traverse native hosts');
    const first = await command.capture();
    assert.deepEqual(calls, [[11, 12]]);
    assert.equal(first.elements.length, 2);
    assert.ok(first.elements.every(element => element.focused === false), 'actual observer facts reach the existing collector');
    const pending = command.capture();
    await pendingRead;
    current = false;
    finish({version: 1, unit: 'dp', rootReactTag: 11, fixtureReactTag: 12, hosts: []});
    await assert.rejects(pending, /scope|retired|cancel/i);
    await assert.rejects(command.capture(), /mounted whole-screen native root/);
    assert.equal(calls.length, 2, 'retired public roots cannot dispatch another native observation');
    assert.ok(dispose);
    dispose(); dispose = undefined;
    assert.equal(globals.__PORTICO_NATIVE_LAYOUT_DUMP__, undefined);
  } finally {
    dispose?.(); unregister?.();
    if (original === undefined) delete globals.__DEV__; else globals.__DEV__ = original;
  }
});

test('production never installs the public command or reads a host', async () => {
  const original = globals.__DEV__;
  globals.__DEV__ = false;
  try {
    const {installNativePublicMeasurement} = await screenModule(new URL('../src-next/dev/measurement-install.ts', import.meta.url), {
      react: {default: {}}, './layout-dump': {...collector, createAndroidNativeTreeObserver: () => {throw new Error('production native module read');}}, 'react-native': {},
    });
    installNativePublicMeasurement(() => {throw new Error('private host read');}, () => {throw new Error('private predicate read');})();
    assert.equal(globals.__PORTICO_NATIVE_LAYOUT_DUMP__, undefined);
  } finally {
    if (original === undefined) delete globals.__DEV__; else globals.__DEV__ = original;
  }
});
