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

test('on iOS a picker mounted in the same commit as the menu that opened it closing still waits for the menu to leave', async () => {
  const {menu, sheet} = await presence('ios');
  await menu.host.mount(); await sheet.host.mount();
  await menu.set(true); assert.equal(menu.view.now.visible, true);
  // Search › a song's ⋯ › Add to playlist…: the item closes the menu and opens the picker in one state update, so both
  // render before either's effects run (iPhone: the picker never appeared and its empty Modal blocked every tap).
  menu.view.shown = false; sheet.view.present = true; sheet.view.shown = true;
  await act(() => {menu.host.rerender(); sheet.host.rerender();});
  assert.equal(menu.view.now.visible, false);
  assert.equal(sheet.view.now.visible, false, 'not presented in the commit that starts the menu\'s dismissal');
  await act(() => menu.view.now.onDismiss());
  await menu.set(false);
  assert.equal(sheet.view.now.visible, true, 'presented once the menu has gone');
});

test('on iOS a picker opened as its menu is unmounted while shown waits until the menu has had time to leave (iPhone, #5012 look)', async () => {
  const {useNativeModalPresence, UNMOUNTED_DISMISS_MS} = await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: 'ios'}}, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: 60_000},
  });
  // The title menu is drawn only while its entry exists: its item closes it by unmounting it, with no onDismiss to come.
  const view = {menu: true, sheet: false, picker: undefined as any};
  const Menu = () => {useNativeModalPresence(true, true); return null;};
  const host = createFabricHookHost(() => {view.picker = useNativeModalPresence(view.sheet, view.sheet);});
  const menuHost = createFabricHookHost(() => {Menu();});
  await menuHost.mount(); await host.mount();
  try {
    await act(async () => {await menuHost.unmount(); view.sheet = true; host.rerender();});
    await act(() => host.rerender());
    assert.equal(view.picker.visible, false, 'not presented while UIKit may still be dismissing the unmounted menu');
    await act(() => new Promise(resolve => setTimeout(resolve, UNMOUNTED_DISMISS_MS + 50)));
    await act(() => host.rerender());
    assert.equal(view.picker.visible, true, 'presented once the menu has had time to go');
  } finally {await host.unmount();}
});

test('on iOS, in one tree and one update, a picker opened as its menu closes waits for the menu to leave (reviewer, #5012)', async () => {
  const {useNativeModalPresence} = await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: 'ios'}}, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: 60_000},
  });
  const view = {menuShown: true, sheet: false, menu: undefined as any, picker: undefined as any};
  // Both overlays' presences in one component: the item's single state update closes one and opens the other.
  const host = createFabricHookHost(() => {view.menu = useNativeModalPresence(true, view.menuShown); view.picker = useNativeModalPresence(view.sheet, view.sheet);});
  await host.mount();
  try {
    assert.equal(view.menu.visible, true);
    view.menuShown = false; view.sheet = true; await act(() => host.rerender());
    assert.equal(view.menu.visible, false);
    assert.equal(view.picker.visible, false, 'not presented while the menu in the same tree is being dismissed');
    await act(() => view.menu.onDismiss()); await act(() => host.rerender());
    assert.equal(view.picker.visible, true, 'presented once the menu has gone');
  } finally {await host.unmount();}
});

test('on iOS an overlay opened with nothing leaving is shown one commit later', async () => {
  const {menu} = await presence('ios');
  await menu.host.mount();
  await menu.set(true);
  if (!menu.view.now.visible) await act(() => menu.host.rerender());
  assert.equal(menu.view.now.visible, true);
});

test('on iOS a presentation UIKit never shows is withdrawn and asks again; one shown stays (iPhone probe, 21 Nov)', async () => {
  const mod = await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: 'ios'}}, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: 60_000},
  });
  const view = {now: undefined as any};
  const host = createFabricHookHost(() => {view.now = mod.useNativeModalPresence(true, true);});
  await host.mount();
  try {
    if (!view.now.visible) await act(() => host.rerender());
    const first = view.now.key;
    assert.equal(view.now.visible, true);
    // No onShow: the presentation was dropped. After the wait it is withdrawn and asks again with a new native host.
    await act(() => new Promise(resolve => setTimeout(resolve, mod.UNSEEN_MODAL_MS + 50)));
    await act(() => host.rerender()); await act(() => host.rerender());
    assert.equal(view.now.visible, true); assert.notEqual(view.now.key, first, 'a fresh presentation');
    // Seen this time: it stays, however long.
    await act(() => view.now.onShow());
    const seen = view.now.key;
    await act(() => new Promise(resolve => setTimeout(resolve, mod.UNSEEN_MODAL_MS + 50)));
    await act(() => host.rerender());
    assert.equal(view.now.visible, true); assert.equal(view.now.key, seen);
  } finally {await host.unmount();}
});

test('the unseen net gives up after its retries and leaves the presentation as it is', async () => {
  const mod = await screenModule(new URL('../src-next/kit/overlays/native-dismissal.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: 'ios'}}, '../focus/layer-stack': {LAYER_FOCUS_WAIT_MS: 60_000},
  });
  const view = {now: undefined as any};
  const host = createFabricHookHost(() => {view.now = mod.useNativeModalPresence(true, true);});
  await host.mount();
  try {
    const keys = new Set<number>();
    for (let n = 0; n < mod.UNSEEN_RETRIES + 3; n++) {
      await act(() => host.rerender()); await act(() => host.rerender());
      if (view.now.visible) keys.add(view.now.key);
      await act(() => new Promise(resolve => setTimeout(resolve, mod.UNSEEN_MODAL_MS + 30)));
    }
    assert.equal(keys.size, mod.UNSEEN_RETRIES + 1, 'the first ask and its retries, then no more');
    assert.equal(view.now.visible, true);
  } finally {await host.unmount();}
});
