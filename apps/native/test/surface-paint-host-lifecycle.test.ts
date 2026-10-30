/** Authored source only. Real installed Fabric reconciliation with controlled
 * native hosts/clocks; no device Modal, focus, IME or animation acceptance. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as geometry from '../src-next/kit/overlays/geometry.ts';
import * as overlayStack from '../src-next/kit/overlays/overlay-stack.ts';
// The real registry is pure: it only calls the host's own focus command, so the
// lifecycle proof below keeps the actual request instead of a local imitation.
import {requestNativeFocus} from '../src-next/kit/focus/registry.ts';

async function fixture(t: any, omitBusiness = false) {
  const react = {...React, default: React};
  const context = await screenModule(new URL('../src-next/kit/overlays/OverlayPaintContext.tsx', import.meta.url), {react});
  const model = await screenModule(new URL('../src-next/kit/overlays/overlay-paint-model.ts', import.meta.url), {react});
  const {useOverlayLifecycle} = await screenModule(new URL('../src-next/kit/overlays/OverlayHost.tsx', import.meta.url), {
    react, './OverlayPaintContext': context, './geometry': geometry, './overlay-stack': overlayStack,
    'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, StyleSheet: {create: (value: any) => value}},
    '../../platform/overlay-keys': {registerOverlayKeys: () => () => {}}, '../../platform/tv-menu': {captureTVMenuKey: () => () => {}},
    '../focus': {requestNativeFocus, useOptionalFocusRegistry: () => undefined}, '../theme': {},
  });
  const host = await screenModule(new URL('../src-next/kit/overlays/SurfacePaintHost.tsx', import.meta.url), {
    react, './OverlayPaintContext': context, './overlay-paint-model': model,
    '../theme': {useTheme: () => ({isTV: false})}, '../useMotion': {useMotion: () => ({reduce: false})},
  });
  const renderer = await fabricHost();
  t.mock.timers.enable({apis: ['setTimeout']});
  const frames = new Map<number, () => void>(); let nextFrame = 0;
  const oldFrame = globalThis.requestAnimationFrame, oldCancel = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = callback => {const id = ++nextFrame; frames.set(id, () => callback(0)); return id;};
  globalThis.cancelAnimationFrame = id => {frames.delete(id);};
  let current = true, writes = 0, disposals = 0, restores = 0, inputMounts = 0, nestedDisposals = 0;
  const subscribers = new Set<() => void>();
  const permit = host.overlayPaintPermit(() => current, [(listener: () => void) => {subscribers.add(listener); return () => subscribers.delete(listener);}]);
  const opener = {current: {focus: () => {restores++;}}};
  function Input({value, onChange}: any) {
    React.useLayoutEffect(() => {inputMounts++;}, []);
    const mode = context.useOverlayPaintMode();
    return React.createElement('RCTTextInput', {testID: 'input', value, onChange: mode.interactive ? onChange : undefined, editable: mode.interactive});
  }
  function Surface({open, returnFocusRef, children, testID}: any) {
    const mode = context.useOverlayPaintMode();
    // The real kit keeps its lifecycle until its exit completes. Here the host
    // deadline controls removal, while the real shared lifecycle owns restoration.
    useOverlayLifecycle(mode.visible, true, () => {}, undefined, returnFocusRef, 0, testID);
    return mode.visible ? React.createElement('RCTView', {testID, open, pointerEvents: mode.interactive ? 'auto' : 'none'}, children) : null;
  }
  const types = [Surface, Input];
  function NestedBusiness() {
    React.useLayoutEffect(() => () => {nestedDisposals++;}, []);
    return React.createElement('RCTView', {testID: 'nested-business'});
  }
  const omitted = omitBusiness ? [NestedBusiness] : undefined;
  function Business({label}: any) {
    React.useLayoutEffect(() => () => {disposals++;}, []);
    return React.createElement(host.RetainedOverlay, {permit, allowedTypes: types, omitTypes: omitted},
      React.createElement(Surface, {open: true, returnFocusRef: opener, testID: label}, React.createElement(Input, {value: label, onChange: () => {writes++;}}), omitBusiness ? React.createElement(NestedBusiness) : null));
  }
  const render = (label?: string) => renderer.render(React.createElement(host.SurfacePaintHost, null, label ? React.createElement(Business, {key: label, label}) : null));
  const nodes = (): any[] => {
    const visit = (items: any[]): any[] => items.flatMap(node => [node, ...visit(node.children)]);
    return visit(renderer.root);
  };
  const input = () => nodes().find(node => node.props.testID === 'input');
  t.after(async () => {await renderer.unmount(); t.mock.timers.reset(); globalThis.requestAnimationFrame = oldFrame; globalThis.cancelAnimationFrame = oldCancel;});
  return {render, nodes, input, renderer, frames,
    counts: () => ({writes, disposals, restores, inputMounts, nestedDisposals}),
    invalidateWithoutNotification: () => {current = false;},
    retireScope: async () => {current = false; for (const notify of subscribers) notify(); await renderer.settle();},
    advance: async (ms: number) => {t.mock.timers.tick(ms); await renderer.settle();},
  };
}

test('business disposal is immediate, while the same committed host tag leaves with inert frozen values', async t => {
  const f = await fixture(t);
  await f.render('original');
  const tag = f.input().tag;
  f.input().props.onChange(); assert.equal(f.counts().writes, 1);
  await f.render();
  assert.equal(f.counts().disposals, 1);
  assert.equal(f.input().tag, tag); assert.equal(f.counts().inputMounts, 1);
  assert.equal(f.input().props.value, 'original'); assert.equal(f.input().props.editable, false);
  assert.equal(f.input().props.onChange, undefined);
  assert.equal(f.nodes().find(node => node.props.testID === 'original').props.pointerEvents, 'none');
  await f.advance(499); assert.equal(f.input().tag, tag); assert.equal(f.counts().restores, 0);
  await f.advance(1); assert.equal(f.input(), undefined); assert.equal(f.counts().restores, 1);
});

test('original scope retirement hides exit paint immediately and suppresses return focus', async t => {
  const f = await fixture(t);
  await f.render('retiring'); await f.render();
  await f.retireScope();
  assert.equal(f.input(), undefined); assert.equal(f.counts().restores, 0);
  await f.advance(500); assert.equal(f.input(), undefined); assert.equal(f.counts().restores, 0);
});

test('a same-value new opening gets its own host and supersedes old paint without stealing focus', async t => {
  const f = await fixture(t);
  await f.render('same'); const originalTag = f.input().tag;
  await f.render(); const callbacks = [...f.frames.values()]; await f.render('same');
  assert.notEqual(f.input().tag, originalTag);
  assert.equal(f.counts().inputMounts, 2); assert.equal(f.counts().restores, 0);
  assert.equal(f.nodes().filter(node => node.props.testID === 'input').length, 1);
  // A retired frame callback must not remove or mutate the successor.
  assert.ok(callbacks.length > 0); for (const callback of callbacks) callback();
  await f.advance(500); assert.equal(f.input().props.value, 'same'); assert.equal(f.input().props.editable, true);
  assert.equal(f.counts().restores, 0);
});

test('whole-shell removal checks the original permit even without a final HIDDEN render', async t => {
  const f = await fixture(t);
  await f.render('old-authority');
  f.invalidateWithoutNotification();
  await f.renderer.unmount();
  assert.equal(f.counts().disposals, 1);
  assert.equal(f.counts().restores, 0);
});


test('explicit business omissions dispose with their publisher while audited sibling paint retains its host', async t => {
  const f = await fixture(t, true);
  await f.render('original');
  const tag = f.input().tag;
  assert.ok(f.nodes().find(node => node.props.testID === 'nested-business'));
  await f.render();
  assert.equal(f.counts().nestedDisposals, 1);
  assert.equal(f.nodes().find(node => node.props.testID === 'nested-business'), undefined);
  assert.equal(f.input().tag, tag);
  assert.equal(f.input().props.editable, false);
  assert.equal(f.counts().disposals, 1);
});
