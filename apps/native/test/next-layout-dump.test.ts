import assert from 'node:assert/strict';
import test from 'node:test';
import {measureNativeHost, relativeToRoot} from '../src-next/dev/layout-dump/native-host.ts';
import {createNativeMeasurementRegistry, curateFlattenedStyle} from '../src-next/dev/layout-dump/registry.ts';
import {collectNativeScreen} from '../src-next/dev/layout-dump/collect.ts';
import {installNativeLayoutDump, registerNativeHost} from '../src-next/dev/layout-dump/index.ts';

import {nativeHost as host, setNativeObservation, observeNativeWindow, addNativeFixture, windowAPI,
  type TestNativeHost} from './helpers/native-window-fixture.ts';

async function collectTestScene(options: Parameters<typeof collectNativeScreen>[0]) {
  const dispose = addNativeFixture(options.root as TestNativeHost, options.registry.register.bind(options.registry));
  try {return await collectNativeScreen({...options, observe: observeNativeWindow, isCurrent: () => true});}
  finally {dispose();}
}
const paint = {fontFamily: 'Public Test Font', resolvedFontFamily: 'Public Test Font', fontSize: 24, fontWeight: 700, color: 'rgb(237, 245, 250)',
  effectiveOpacity: 1, effectiveBackgroundColor: 'rgb(7, 16, 24)'};

test('native host numbers remain fractional logical window coordinates, never density-adjusted', async () => {
  const result = await measureNativeHost(host('RN:View', {x: 10.25, y: 20.5, width: 300.75, height: 400.125}));
  assert.ok('rect' in result);
  assert.deepEqual(result.rect, {x: 10.25, y: 20.5, width: 300.75, height: 400.125});
  assert.deepEqual(relativeToRoot(result.rect, {x: 2.25, y: 4.5, width: 390, height: 844}), {x: 8, y: 16, width: 300.75, height: 400.125});
  assert.equal('unavailable' in await measureNativeHost({getBoundingClientRect: () => ({x: NaN, y: 0, width: 10, height: 10})}), true);
});

test('RN full viewport accepts exact float32 extents without changing raw facts or accepting epsilon drift', async () => {
  const width = 411.42857142857144, height = 914.2857142857143;
  for (const platform of ['android', 'ios', 'tvos'] as const) {
    const rect = {x: 0, y: 0, width: Math.fround(width), height: Math.fround(height)};
    const viewport = {width, height, deviceScaleFactor: 2.625};
    const options = {root: host('RN:View', rect), viewport, platform, registry: createNativeMeasurementRegistry()};
    const measured = await collectTestScene(options);
    assert.equal(measured.measurement.geometrySource, 'native-window');
    assert.equal(measured.diagnostics.nativeCoordinates.windowAPI, windowAPI);
    assert.equal(measured.fullViewport, true);
    assert.deepEqual(measured.root, rect);
    assert.deepEqual(measured.viewport, viewport);
    const reverse = await collectTestScene({...options, root: host('RN:View', {x: 0, y: 0, width, height}),
      viewport: {...viewport, width: Math.fround(width), height: Math.fround(height)}});
    assert.equal(reverse.fullViewport, true);
    for (const invalid of [{...rect, width: rect.width + 0.0001}, {...rect, x: 0.000001}, {...rect, y: 0.000001}, {...rect, width: 390, height: 844}]) {
      assert.equal((await collectTestScene({...options, root: host('RN:View', invalid)})).fullViewport, false);
    }
  }
});

test('a nonzero observed root origin stays invalid and records provenance rather than selecting a zero-returning API', async () => {
  const root = Object.assign(host('RN:View', {x: 0, y: 24, width: 390, height: 844}), {
    measureInWindow(callback: (x: number, y: number, width: number, height: number) => void) {callback(0, 0, 390, 844);}});
  const measured = await collectTestScene({root, viewport: {width: 390, height: 844, deviceScaleFactor: 3},
    platform: 'android', registry: createNativeMeasurementRegistry()});
  assert.equal(measured.fullViewport, false);
  assert.deepEqual(measured.root, {x: 0, y: 0, width: 390, height: 844});
  const gap = measured.unavailable.find(item => item.property === 'fullViewport')!;
  assert.match(gap.reason, /coordinate-space=attached-native-window/);
  assert.deepEqual(measured.diagnostics.nativeCoordinates.rootWindow, {x: 0, y: 24, width: 390, height: 844});
  assert.deepEqual(measured.diagnostics.nativeCoordinates.publicRootReadback?.rect, {x: 0, y: 0, width: 390, height: 844});
});

test('font scaling is optional unscored metadata and never changes logical observations', async () => {
  const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844});
  const options = {root, viewport: {width: 390, height: 844, deviceScaleFactor: 3}, platform: 'android' as const,
    registry: createNativeMeasurementRegistry()};
  const baseline = await collectTestScene(options);
  for (const fontScale of [0.85, 1, 1.3, 2]) {
    const measured = await collectTestScene({...options, diagnostics: {fontScale}});
    assert.equal(measured.diagnostics.fontScale, fontScale);
    const {fontScale: _, ...diagnostics} = measured.diagnostics;
    assert.deepEqual({...measured, diagnostics}, baseline);
  }
  for (const fontScale of [undefined, 0, -1, NaN, Infinity]) {
    assert.deepEqual(await collectTestScene({...options, diagnostics: {fontScale}}), baseline);
  }
});

test('DEV metadata getter reads fresh font scaling only for an active capture', async () => {
  const globals = globalThis as typeof globalThis & {__DEV__?: boolean};
  const original = globals.__DEV__;
  let installed: ReturnType<typeof installNativeLayoutDump> = undefined;
  let retireFixture: (() => void) | undefined;
  let fontScale = 1, reads = 0;
  try {
    globals.__DEV__ = true;
    const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844});
    retireFixture = addNativeFixture(root, registerNativeHost);
    installed = installNativeLayoutDump({root: () => root,
      viewport: () => ({width: 390, height: 844, deviceScaleFactor: 3}), platform: 'android',
      observe: observeNativeWindow,
      diagnostics: () => {reads++; return {fontScale};}})!;
    assert.equal(reads, 0);
    assert.equal((await installed.capture()).diagnostics.fontScale, 1);
    fontScale = 1.4;
    assert.equal((await installed.capture()).diagnostics.fontScale, 1.4);
    installed.dispose();
    await assert.rejects(installed.capture(), /disposed/);
    assert.equal(reads, 2);
  } finally {
    installed?.dispose();
    retireFixture?.();
    if (original === undefined) delete globals.__DEV__;
    else globals.__DEV__ = original;
  }
});

test('late native callbacks are retired after cancellation or deadline', async () => {
  let callback: ((x: number, y: number, width: number, height: number) => void) | undefined;
  const controller = new AbortController();
  const pending = measureNativeHost({measureInWindow: value => {callback = value;}}, {signal: controller.signal, timeoutMs: 100});
  controller.abort();
  assert.deepEqual(await pending, {unavailable: 'capture cancelled'});
  callback!(1, 2, 3, 4);
  assert.deepEqual(await measureNativeHost({measureInWindow() {}}, {timeoutMs: 1}), {unavailable: 'native measurement callback did not arrive'});
});

test('curated diagnostics never read getters, arbitrary props, URLs, objects or asset references', () => {
  const raw = {fontSize: 24, opacity: .5, color: '#edf5fa', transform: [{scale: 2}], source: 'private.png',
    backgroundColor: 'url(https://example.invalid/private.jpg)', fontFamily: '/private/font.ttf', password: 'never-read'};
  Object.defineProperty(raw, 'fontWeight', {get() {throw new Error('getter was read');}});
  assert.deepEqual(curateFlattenedStyle(raw), {fontSize: 24, color: '#edf5fa', opacity: .5});
  assert.deepEqual(curateFlattenedStyle([raw]), {});
});

test('complete declared style is explicit current registration evidence, never inferred from a partial style', () => {
  const registry = createNativeMeasurementRegistry(), node = {};
  const first = registry.register(node, {kind: 'view', flattenedStyle: {opacity: .5}});
  assert.equal(registry.read(node)?.declaredStyleComplete, false);
  first.update({kind: 'view', flattenedStyle: {}, declaredStyleComplete: true});
  assert.equal(registry.read(node)?.declaredStyleComplete, true);
  first.update({kind: 'view', flattenedStyle: {opacity: .75}});
  assert.equal(registry.read(node)?.declaredStyleComplete, false);
  for (const flattenedStyle of [undefined, null, [], 'background']) {
    first.update({kind: 'view', flattenedStyle, declaredStyleComplete: true});
    assert.equal(registry.read(node)?.declaredStyleComplete, false);
  }
  const unsafe: Record<string, unknown> = {backgroundColor: 'url(https://example.invalid/private.png)'};
  first.update({kind: 'view', flattenedStyle: unsafe, declaredStyleComplete: true});
  assert.equal(registry.read(node)?.declaredStyleComplete, false);
  Object.defineProperty(unsafe, 'opacity', {get() {throw new Error('Style getter was read.');}});
  first.update({kind: 'view', flattenedStyle: unsafe, declaredStyleComplete: true});
  assert.equal(registry.read(node)?.declaredStyleComplete, false);
  const current = registry.register(node, {kind: 'view', flattenedStyle: {}, declaredStyleComplete: true});
  first.update({kind: 'view', flattenedStyle: {opacity: 0}});
  first.unregister();
  assert.equal(registry.read(node)?.declaredStyleComplete, true);
  current.unregister();
  assert.equal(registry.read(node), undefined);
});

test('declared shadow offsets curate bounded scalars without retaining objects or reading accessors', () => {
  const raw = {shadowOffset: {width: -2, height: 3.5}, textShadowOffset: {width: 0, height: 2},
    textShadowColor: '#07101880', textShadowRadius: 30, textDecorationLine: 'underline'};
  assert.deepEqual(curateFlattenedStyle(raw), {shadowOffsetX: -2, shadowOffsetY: 3.5,
    textShadowOffsetX: 0, textShadowOffsetY: 2, textShadowColor: '#07101880', textShadowRadius: 30, textDecorationLine: 'underline'});
  const offset = {height: 2};
  Object.defineProperty(offset, 'width', {get() {throw new Error('Offset getter was read.');}});
  const registry = createNativeMeasurementRegistry(), node = {};
  registry.register(node, {kind: 'text', flattenedStyle: {textShadowOffset: offset}, declaredStyleComplete: true});
  assert.deepEqual(registry.read(node)?.flattenedStyle, {});
  assert.equal(registry.read(node)?.declaredStyleComplete, false);
});

test('diagnostics cannot supply measured paint/font facts and invalidation does not invent defaults', () => {
  const registry = createNativeMeasurementRegistry(), node = {};
  const lease = registry.register(node, {kind: 'text', flattenedStyle: {opacity: .5, fontFamily: 'Input Font', fontSize: 24}});
  assert.deepEqual(registry.read(node)?.measuredStyle, {});
  assert.equal(registry.read(node)?.effectiveOpacity, undefined);
  lease.recordResolvedStyle(paint);
  assert.equal(registry.read(node)?.measuredStyle.fontSize, '24px');
  lease.recordTextLayout([{x: 1, y: 2, width: 30, height: 20, text: 'Visible'}]);
  lease.update({kind: 'text', flattenedStyle: {fontSize: 30}});
  assert.equal(registry.read(node)?.lines, undefined);
  assert.equal(registry.read(node)?.effectiveOpacity, undefined);
});

test('capture marks complete current declarations without projecting them over observed facts', async () => {
  const child = host('RN:View', {x: 10, y: 20, width: 100, height: 40});
  const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844}, [child]);
  const registry = createNativeMeasurementRegistry();
  setNativeObservation(child, {style: {backgroundColor: 'rgba(1, 2, 3, 1)'}, effectiveOpacity: .9});
  for (const complete of [true, false, undefined]) {
    registry.register(child, {kind: 'view', flattenedStyle: {backgroundColor: '#edf5fa', opacity: .25},
      declaredStyleComplete: complete});
    const measured = await collectTestScene({root, registry, platform: 'android',
      viewport: {width: 390, height: 844, deviceScaleFactor: 3}});
    const node = measured.elements.find(element => element.id === 'root.0')!;
    const diagnostics = node.diagnostics as Record<string, unknown>;
    assert.deepEqual(diagnostics.flattenedStyle, {backgroundColor: '#edf5fa', opacity: .25});
    assert.deepEqual(diagnostics.declaredStyle, complete ? {source: 'theme-resolved-stylesheet', complete: true} : undefined);
    assert.equal((node.style as Record<string, unknown>).backgroundColor, 'rgba(1, 2, 3, 1)');
    assert.equal(node.effectiveOpacity, .9);
    assert.deepEqual(node.box, {x: 10, y: 20, width: 100, height: 40});
  }
});

test('actual native focus events are lease/epoch bound; stale blur never clears a newer host', () => {
  const registry = createNativeMeasurementRegistry(), first = {}, second = {}, mounted = new Set([first, second]);
  const a = registry.register(first, {kind: 'view'}), b = registry.register(second, {kind: 'view'});
  assert.equal(registry.actualFocus(first, mounted), undefined);
  a.recordNativeFocus(true);
  assert.equal(registry.actualFocus(first, mounted), true);
  b.recordNativeFocus(true);
  a.recordNativeFocus(false);
  assert.equal(registry.actualFocus(second, mounted), true);
  b.unregister();
  assert.equal(registry.actualFocus(first, mounted), undefined);
  const replacement = registry.register(first, {kind: 'view'});
  replacement.recordNativeFocus(true);
  a.unregister(); a.recordNativeFocus(false);
  assert.equal(registry.actualFocus(first, mounted), true);
  registry.retireEpoch();
  replacement.recordNativeFocus(true);
  assert.equal(registry.actualFocus(first, mounted), undefined);
});

test('inputs never accept text layout or export native input internals', async () => {
  const registry = createNativeMeasurementRegistry();
  const input = host('RN:AndroidTextInput', {x: 10, y: 20, width: 100, height: 40});
  for (const key of ['childNodes', 'value', 'textContent', 'props']) Object.defineProperty(input, key, {get() {throw new Error('private field was read');}});
  const lease = registry.register(input, {kind: 'input'});
  lease.recordTextLayout(new Proxy([], {get() {throw new Error('input text was read');}}));
  lease.recordMaskLength(4);
  setNativeObservation(input, {textRects: [{x: 10, y: 20, width: 100, height: 40}], textRectsSource: 'android.Layout.linesToWindowDp'});
  const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844}, [input]);
  const measured = await collectTestScene({root, viewport: {width: 390, height: 844, deviceScaleFactor: 3}, platform: 'android', registry});
  assert.equal(measured.viewport.deviceScaleFactor, 3);
  assert.equal(measured.elements[1].ownText, '');
  assert.deepEqual(measured.elements[1].input, {value: null, redacted: true, maskedLength: 4});
  assert.equal(measured.elements[1].textRects, undefined);
  assert.equal((measured.elements[1].diagnostics as {nativeWindowTextRects?: unknown}).nativeWindowTextRects, undefined);
  assert.deepEqual(measured.strings, []);
});

test('only observed native window Text rectangles become text ranges; host boxes never substitute', async () => {
  const registry = createNativeMeasurementRegistry();
  const text = host('RN:Text', {x: 20, y: 40, width: 100, height: 30});
  const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844}, [text]);
  const textLease = registry.register(text, {kind: 'text', semanticId: 'public-title', role: 'header'});
  textLease.recordTextLayout([{x: 2, y: 3, width: 70, height: 24, text: 'Public title', capHeight: 17}]);
  textLease.recordResolvedStyle(paint);
  let registryReads = 0;
  textLease.observeTextRectangles(async () => {registryReads++; return [{x: 999, y: 999, width: 70, height: 24}];});
  textLease.recordNativeFocus(false);
  setNativeObservation(text, {style: {resolvedFontFamily: 'Public Test Font', fontSize: '24px', fontWeight: '700', color: paint.color},
    effectiveOpacity: 1, effectiveBackgroundColor: paint.effectiveBackgroundColor, focused: true,
    textRects: [{x: 22, y: 43, width: 70, height: 24}], textRectsSource: 'android.TextView.Layout.linesToWindowDp'});
  registry.register(root, {kind: 'view'}).recordResolvedStyle({effectiveOpacity: 1, effectiveBackgroundColor: 'rgb(7, 16, 24)'});
  const options = {root, viewport: {width: 390, height: 844, deviceScaleFactor: 3}, platform: 'android' as const, registry, anchors: {'public-title': 'title'}};
  const measured = await collectTestScene(options);
  assert.equal(measured.fullViewport, true);
  assert.equal(measured.elements[1].focused, true);
  assert.deepEqual(measured.elements[1].textRects, [{x: 22, y: 43, width: 70, height: 24}]);
  assert.equal(registryReads, 0, 'another observer is not part of the admitted native batch');
  assert.equal(measured.elements[1].frame, 'title');
  assert.equal(measured.strings[0].text, 'Public title');
  setNativeObservation(text, {textRects: undefined});
  const transformed = await collectTestScene(options);
  assert.equal(transformed.elements[1].textRects, undefined);
  assert.ok(transformed.unavailable.some(fact => fact.element === 'root.0' && fact.property === 'textRects'));
});

test('an attached image box does not imply loaded pixels or a fit mode', async () => {
  const registry = createNativeMeasurementRegistry();
  const image = host('RN:Image', {x: 20, y: 30, width: 100, height: 24});
  for (const key of ['childNodes', 'source', 'props', 'asset']) {
    Object.defineProperty(image, key, {get() {throw new Error('Image internals were read.');}});
  }
  registry.register(image, {kind: 'image'});
  setNativeObservation(image, {imageHasImage: true, imageScaleType: 'fit-center'});
  const root = host('RN:View', {x: 0, y: 0, width: 390, height: 844}, [image]);
  const measured = await collectTestScene({root, registry, platform: 'android', viewport: {width: 390, height: 844, deviceScaleFactor: 3}});
  const record = measured.elements.find(element => element.tag === 'RN:Image')!;
  assert.deepEqual(record.box, {x: 20, y: 30, width: 100, height: 24});
  assert.deepEqual(record.image, {kind: 'image'});
  assert.equal(JSON.stringify(measured).includes('imageHasImage'), false);
  assert.equal(JSON.stringify(measured).includes('fit-center'), false);
  for (const property of ['image.loaded', 'image.fit']) {
    assert.ok(measured.unavailable.some(fact => fact.element === record.id && fact.property === property));
  }
});

test('late window Text observers are retired on unregister or cancellation', async () => {
  const registry = createNativeMeasurementRegistry(), node = {};
  const lease = registry.register(node, {kind: 'text'});
  let resolve: ((value: unknown) => void) | undefined;
  lease.observeTextRectangles(() => new Promise(done => {resolve = done;}));
  const controller = new AbortController();
  const pending = registry.measureTextRectangles(node, controller.signal);
  await Promise.resolve();
  controller.abort();
  assert.equal(await pending, undefined);
  resolve!([{x: 1, y: 2, width: 3, height: 4}]);
  const again = registry.measureTextRectangles(node);
  await Promise.resolve();
  lease.unregister();
  resolve!([{x: 1, y: 2, width: 3, height: 4}]);
  assert.equal(await again, undefined);
});

test('a retired capture never starts a queued native text observer', async () => {
  const registry = createNativeMeasurementRegistry(), node = {};
  const lease = registry.register(node, {kind: 'text'});
  let reads = 0;
  lease.observeTextRectangles(async () => {reads++; return [];});
  const controller = new AbortController();
  const pending = registry.measureTextRectangles(node, controller.signal);
  controller.abort();
  assert.equal(await pending, undefined);
  assert.equal(reads, 0);
});

test('unobserved paint hides text and never defaults focus or opacity; wrong viewport fails', async () => {
  const registry = createNativeMeasurementRegistry(), text = host('RN:Text', {x: 0, y: 0, width: 100, height: 30});
  registry.register(text, {kind: 'text'}).recordTextLayout([{x: 0, y: 0, width: 70, height: 24, text: 'Do not export without visibility'}]);
  const root = host('RN:View', {x: 0, y: 24, width: 390, height: 844}, [text]);
  const measured = await collectTestScene({root, viewport: {width: 390, height: 844, deviceScaleFactor: 2}, platform: 'ios', registry});
  assert.equal(measured.fullViewport, false);
  assert.equal(measured.elements[1].focused, null);
  assert.equal(measured.elements[1].effectiveOpacity, null);
  assert.equal(measured.elements[1].ownText, undefined);
  assert.deepEqual(measured.strings, []);
  assert.ok(measured.unavailable.some(fact => fact.property === 'focused'));
  assert.ok(measured.unavailable.some(fact => fact.property === 'ownText'));
});

test('DEV entry is inert in production and disposal refuses a late native capture', async () => {
  const globals = globalThis as typeof globalThis & {__DEV__?: boolean};
  const original = globals.__DEV__;
  try {
    globals.__DEV__ = false;
    assert.equal(registerNativeHost({}, {kind: 'view'}), undefined);
    assert.equal(installNativeLayoutDump({root() {throw new Error('production read a native root');}, viewport() {throw new Error('production read viewport');},
      diagnostics() {throw new Error('production read font scaling');}, platform: 'android'}), undefined);
    globals.__DEV__ = true;
    let callback: ((x: number, y: number, width: number, height: number) => void) | undefined;
    const root = {measureInWindow(value: typeof callback) {callback = value;}};
    const installed = installNativeLayoutDump({root: () => root, viewport: () => ({width: 390, height: 844, deviceScaleFactor: 3}), platform: 'android'})!;
    const pending = installed.capture();
    installed.dispose();
    await assert.rejects(pending, /cancelled|disposed/);
    callback!(0, 0, 390, 844);
  } finally {
    if (original === undefined) delete globals.__DEV__;
    else globals.__DEV__ = original;
  }
});
