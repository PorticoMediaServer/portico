import assert from 'node:assert/strict';
import test from 'node:test';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import * as focus from '../src-next/kit/focus/registry.ts';
import * as geometry from '../src-next/kit/overlays/geometry.ts';
import * as overlayStack from '../src-next/kit/overlays/overlay-stack.ts';
import {liveOverlayPaint, screenModule} from './helpers/screen-harness.mjs';

async function overlayHost(isTV: boolean) {
  const backHandlers = new Set<() => boolean>();
  const keyHandlers = new Set<unknown>();
  const mod = await screenModule(new URL('../src-next/kit/overlays/OverlayHost.tsx', import.meta.url), {
    './OverlayPaintContext': liveOverlayPaint,
    react: {...React, default: React},
    'react-native': {Platform: {OS: 'android', isTV}, StyleSheet: {create: (styles: unknown) => styles},
      BackHandler: {addEventListener: (_event: string, handler: () => boolean) => {
        backHandlers.add(handler); return {remove: () => backHandlers.delete(handler)};
      }}},
    '../../platform/tv-menu': {captureTVMenuKey: () => () => {}},
    '../../platform/overlay-keys': {registerOverlayKeys: (handler: unknown) => {
      keyHandlers.add(handler); return () => keyHandlers.delete(handler);
    }},
    '../theme': {}, '../focus': {...focus, useOptionalFocusRegistry: () => undefined},
    './geometry': geometry, './overlay-stack': overlayStack,
  });
  return {mod, backHandlers, keyHandlers};
}

/** The real lifecycle hook mounted as a component on the installed Fabric
 * renderer, so React owns layout-effect setup, cleanup on close and unmount. */
async function mountHook(body: () => void) {
  const host = await fabricHost();
  const Hook = () => {body(); return null;};
  const render = () => host.render(React.createElement(Hook));
  await render();
  return {rerender: render, unmount: () => host.unmount()};
}

test('the shared native request reports missing targets and retains registry restoration policy', () => {
  for (const target of [undefined, null, {}]) assert.equal(focus.requestNativeFocus(target), false);
  const calls: string[] = [];
  const view = {requestTVFocus() {assert.equal(this, view); calls.push('view');}, focus() {calls.push('generic');}};
  const input = {focus() {assert.equal(this, input); calls.push('input');}};
  assert.equal(focus.requestNativeFocus(view), true);
  assert.equal(focus.requestNativeFocus(input), true);
  const registry = focus.createFocusRegistry();
  registry.configure({id: 'surface', order: ['safe', 'input']});
  registry.attach({regionId: 'surface', childId: 'safe'}, view);
  registry.attach({regionId: 'surface', childId: 'input'}, input);
  registry.didFocus({regionId: 'surface', childId: 'safe'});
  assert.equal(registry.restore(registry.capture()), true);
  assert.equal(registry.focus({regionId: 'surface', childId: 'input'}), true);
  assert.deepEqual(calls, ['view', 'input', 'view', 'input']);
});

for (const isTV of [false, true]) {
  test(`Android ${isTV ? 'TV' : 'phone'} overlay requests the available native View command and preserves TextInput focus`, async () => {
    const {mod} = await overlayHost(isTV), calls: string[] = [];
    const view = {requestTVFocus() {assert.equal(this, view); calls.push('native');}, focus() {calls.push('generic');}};
    const textInput = {focus() {assert.equal(this, textInput); calls.push('text-input');}};
    mod.focusTarget(view); mod.focusTarget(textInput);
    mod.focusTarget(null); mod.focusTarget(undefined); mod.focusTarget({});
    assert.deepEqual(calls, ['native', 'text-input']);
  });

  test(`Android ${isTV ? 'TV' : 'phone'} overlay opening and closing use the actual safe ref and opener without claiming Tab`, async t => {
    t.mock.timers.enable({apis: ['setTimeout']});
    const {mod, backHandlers, keyHandlers} = await overlayHost(isTV), calls: string[] = [];
    const safe = {requestTVFocus() {calls.push('cancel');}, focus() {calls.push('generic-cancel');}};
    const opener = {requestTVFocus() {calls.push('opener');}, focus() {calls.push('generic-opener');}};
    const reasons: string[] = [];
    let open = true;
    const session = await mountHook(() => mod.useOverlayLifecycle(open, true, (reason: string) => reasons.push(reason),
      {current: safe}, {current: opener}));
    try {
      assert.deepEqual(calls, [], 'focus waits for the mounted surface');
      t.mock.timers.tick(0);
      assert.deepEqual(calls, ['cancel']);
      assert.equal(mod.dispatchOverlayKey('tab'), false);
      assert.equal(mod.dispatchOverlayKey('tab', true), false);
      assert.deepEqual(reasons, []);
      assert.equal([...backHandlers][0]!(), true);
      assert.deepEqual(reasons, ['back']);
      open = false; await session.rerender();
      assert.deepEqual(calls, ['cancel', 'opener']);
      assert.equal(backHandlers.size, 0); assert.equal(keyHandlers.size, 0);
      t.mock.timers.tick(0); assert.deepEqual(calls, ['cancel', 'opener']);
    } finally {await session.unmount(); t.mock.timers.reset();}
  });
}
