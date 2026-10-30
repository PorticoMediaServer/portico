import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';

async function fixture(dev: boolean) {
  const state = hooks(), pending: (() => void)[] = [], effects = new Set<{deps?: readonly unknown[]; cleanup?: () => void}>();
  const react = {...state.react, useLayoutEffect: (run: () => void | (() => void), deps?: readonly unknown[]) => {
    const slot = state.react.useRef({} as {deps?: readonly unknown[]; cleanup?: () => void}).current;
    effects.add(slot);
    if (!deps || !slot.deps || deps.some((value, index) => value !== slot.deps![index])) pending.push(() => {
      slot.cleanup?.(); slot.deps = deps; slot.cleanup = run() || undefined;
    });
  }};
  const observations: {host: object; active: boolean; lines?: unknown; focus?: boolean; facts?: object; invalidations: number}[] = [];
  let imports = 0;
  const originalDev = Reflect.get(globalThis, '__DEV__'), originalRequire = Reflect.get(globalThis, 'require');
  Reflect.set(globalThis, '__DEV__', dev);
  Reflect.set(globalThis, 'require', (name: string) => {
    assert.equal(name, '../dev/layout-dump/index'); imports++;
    return {registerNativeHost(host: object, facts: object) {
      const record = {host, facts, active: true, invalidations: 0} as typeof observations[number]; observations.push(record);
      return {update(next: object) {assert.ok(record.active); record.facts = next;}, invalidateTextLayout() {record.invalidations++;},
        recordTextLayout(lines: unknown) {assert.ok(record.active); record.lines = lines;},
        recordNativeFocus(value: boolean) {assert.ok(record.active); record.focus = value;},
        recordResolvedStyle() {throw new Error('input styles must not become resolved paint');},
        unregister() {record.active = false;}};
    }};
  });
  const host = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react});
  const module = await screenModule(new URL('../src-next/kit/dev-observation.ts', import.meta.url), {
    react, 'react-native': {findNodeHandle: (node: {handle: number}) => node.handle}, './focus/useHostRef': host,
  });
  return {observations, imports: () => imports,
    render: (facts: object, ref?: unknown, revision?: unknown) => {const result = state.render(() => module.useNativeObservation(facts, ref, revision)); while (pending.length) pending.shift()!(); return result;},
    dispose() {for (const effect of effects) effect.cleanup?.();
      if (originalDev === undefined) Reflect.deleteProperty(globalThis, '__DEV__'); else Reflect.set(globalThis, '__DEV__', originalDev);
      if (originalRequire === undefined) Reflect.deleteProperty(globalThis, 'require'); else Reflect.set(globalThis, 'require', originalRequire);
    }};
}

test('actual text events preserve forwarded ref cleanup and retire replaced/unmounted leases', async () => {
  const f = await fixture(true), forwarded: object[] = []; let cleanups = 0;
  try {
    const binding = f.render({kind: 'text', semanticId: 'public-label', flattenedStyle: {fontSize: 24}}, (node: object) => {forwarded.push(node); return () => {cleanups++;};}, 'Public label');
    const a = {handle: 11}, b = {handle: 22}, lines = [{text: 'Public label', x: 0, y: 0, width: 90, height: 24}];
    binding.ref(a); binding.textProps.onTextLayout({nativeEvent: {target: 11, lines}});
    assert.deepEqual(f.observations[0].lines, lines);
    const cleanup = binding.ref(b); assert.equal(f.observations[0].active, false); assert.equal(cleanups, 1);
    binding.textProps.onTextLayout({nativeEvent: {target: 11, lines}}); assert.equal(f.observations[1].lines, undefined);
    binding.textProps.onTextLayout({nativeEvent: {target: 22, lines}}); assert.deepEqual(f.observations[1].lines, lines);
    cleanup(); assert.equal(f.observations[1].active, false); assert.equal(cleanups, 2); assert.deepEqual(forwarded, [a, b]);
  } finally {f.dispose();}
});
test('actual Pressable declaration updates are current, separate from measured paint and retired with the host', async () => {
  const f = await fixture(true);
  try {
    const binding = f.render({kind: 'view', semanticId: 'public-action'});
    const cleanup = binding.ref({handle: 11});
    binding.recordDeclaredStyle({backgroundColor: '#fff', opacity: .5});
    assert.deepEqual(f.observations[0].facts, {kind: 'view', semanticId: 'public-action',
      flattenedStyle: {backgroundColor: '#fff', opacity: .5}, declaredStyleComplete: true});
    binding.recordDeclaredStyle({backgroundColor: '#000', opacity: 1});
    assert.equal((f.observations[0].facts as any).flattenedStyle.opacity, 1);
    cleanup();
    binding.recordDeclaredStyle({opacity: .2});
    assert.equal((f.observations[0].facts as any).flattenedStyle.opacity, 1);
  } finally {f.dispose();}
});
test('focus observations come only from the current native event target', async () => {
  const f = await fixture(true);
  try {
    const binding = f.render({kind: 'view', semanticId: 'public-action'}); const cleanup = binding.ref({handle: 11});
    binding.recordFocus({nativeEvent: {target: 99}}, true); assert.equal(f.observations[0].focus, undefined);
    binding.recordFocus({nativeEvent: {target: 11}}, true); assert.equal(f.observations[0].focus, true);
    binding.recordFocus({nativeEvent: {target: 11}}, false); assert.equal(f.observations[0].focus, false);
    cleanup(); binding.recordFocus({nativeEvent: {target: 11}}, true); assert.equal(f.observations[0].focus, false);
  } finally {f.dispose();}
});
test('production preserves the original host ref and exposes no measurement text props or collector import', async () => {
  const f = await fixture(false), ref = () => {};
  try {
    const binding = f.render({kind: 'text', semanticId: 'public-label'}, ref, 'Public label');
    assert.equal(binding.ref, ref); assert.deepEqual(binding.textProps, {}); assert.equal(f.imports(), 0); assert.deepEqual(f.observations, []);
  } finally {f.dispose();}
});


test('image host observation keeps the forwarded ref lifecycle and never reads asset props or records text', async () => {
  const f = await fixture(true), forwarded: unknown[] = [];
  try {
    const first = {handle: 11, get props(): never {throw new Error('asset props must not be read');}}, second = {handle: 22};
    const binding = f.render({kind: 'image', semanticId: 'public-wordmark'}, (host: unknown) => {forwarded.push(host);});
    binding.ref(first);
    assert.equal(f.observations[0].host, first);
    binding.textProps.onTextLayout({nativeEvent: {target: 11, lines: []}});
    assert.equal(f.observations[0].lines, undefined);
    const cleanup = binding.ref(second);
    assert.equal(f.observations[0].active, false);
    assert.equal(f.observations[1].active, true);
    cleanup();
    assert.equal(f.observations[1].active, false);
    assert.deepEqual(forwarded, [first, null, second, null]);
  } finally {f.dispose();}
});
