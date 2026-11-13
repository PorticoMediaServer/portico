// UIKit drops a Modal presented while another is still being dismissed: a menu item that opens a confirmation sheet
// opened nothing on iPhone (Settings › Switch server › ⋯ › Sign out). A new presentation waits for every leaving one.
import test from 'node:test';
import assert from 'node:assert/strict';
import {React} from './helpers/fabric-host.mjs';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

async function presence(os: 'ios' | 'android', wait = 60_000) {
  const {useNativeModalPresence} = await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: os}}, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: wait},
  });
  // One probe per overlay, each with its own present/shown and the latest presence it reported.
  const overlay = () => {
    const view = {present: false, shown: false, closing: false, now: undefined as any};
    const host = createFabricHookHost(() => {view.now = useNativeModalPresence(view.present, view.shown, wait, view.closing);});
    return {view, host, set: async (present: boolean, shown = present, closing = false) => {view.present = present; view.shown = shown; view.closing = closing; await act(() => host.rerender());}};
  };
  return {menu: overlay(), sheet: overlay()};
}

test('on iOS a sheet opened from a menu item waits until the menu has finished leaving, then shows', async () => {
  const {menu, sheet} = await presence('ios');
  await menu.host.mount(); await sheet.host.mount();
  await menu.set(true); assert.equal(menu.view.now.visible, true);
  // The item is chosen: the menu starts leaving and the confirmation asks to present in the same moment.
  await menu.set(true, false); await sheet.set(true);
  assert.equal(menu.view.now.visible, false);
  assert.equal(sheet.view.now.visible, false, 'not presented while the menu is still being dismissed');
  await act(() => menu.view.now.onDismiss());
  await menu.set(false);
  assert.equal(sheet.view.now.visible, true, 'presented once the menu has gone');
});

test('a dismissal that is never reported is given up on after the wait, and then blocks nothing', async () => {
  const {menu, sheet} = await presence('ios', 30);
  await menu.host.mount(); await sheet.host.mount();
  await menu.set(true); await menu.set(true, false); await sheet.set(true);
  assert.equal(sheet.view.now.visible, false);
  await act(() => new Promise(resolve => setTimeout(resolve, 80)));
  await act(() => sheet.host.rerender());
  assert.equal(sheet.view.now.visible, true);
});

test('Android dialogs go with their element: nothing waits', async () => {
  const {menu, sheet} = await presence('android');
  await menu.host.mount(); await sheet.host.mount();
  await menu.set(true); await menu.set(true, false); await sheet.set(true);
  assert.equal(sheet.view.now.visible, true);
});

test('an overlay already shown is never hidden by another leaving', async () => {
  const {menu, sheet} = await presence('ios');
  await menu.host.mount(); await sheet.host.mount();
  await sheet.set(true); assert.equal(sheet.view.now.visible, true);
  await menu.set(true); await menu.set(true, false);
  assert.equal(sheet.view.now.visible, true);
});

test('a sheet that is still running its exit counts as leaving: a confirmation waits for it, not just for its native dismissal', async () => {
  const {menu, sheet} = await presence('ios');
  await menu.host.mount(); await sheet.host.mount();
  await menu.set(true);
  // The menu sheet animates out with its Modal still shown; the confirmation asks to present meanwhile.
  await menu.set(true, true, true); await sheet.set(true);
  assert.equal(menu.view.now.visible, true, 'the exit runs on screen');
  assert.equal(sheet.view.now.visible, false, 'never presented over a sheet that is about to be dismissed');
  await menu.set(true, false); await act(() => menu.view.now.onDismiss()); await menu.set(false);
  assert.equal(sheet.view.now.visible, true);
});
