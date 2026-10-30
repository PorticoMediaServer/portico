import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as menuKey from '../src-next/platform/menu-key.ts';

const menu = {eventType: 'menu', eventKeyAction: 1};
const dispatch = (event: any = menu, os: Parameters<typeof menuKey.dispatchMenuKey>[1] = 'android', isTV = true) => menuKey.dispatchMenuKey(event, os, isTV);

// The real hook runs on the installed Fabric renderer: React owns effect
// replacement, cleanup on blur and unmount and the captured action. The
// driver's recreated setters and shared effect phase cannot prove that the
// broker never retains an action its control no longer owns.
async function useFocusedMenu() {
  return (await screenModule(new URL('../src-next/kit/controls/useFocusedMenu.ts', import.meta.url), {
    react: {...React, default: React}, '../../platform/menu-key': menuKey,
  })).useFocusedMenu;
}

async function mountHooks(use: (isTV: boolean, focused: boolean, disabled: any, action: (() => void) | undefined) => void, body: () => void) {
  const host = await fabricHost();
  const Hooks = () => {body(); return null;};
  const render = () => host.render(React.createElement(Hooks));
  await render();
  return {rerender: render, unmount: () => host.unmount(), use};
}

test('only the real focused enabled TV control owns Menu-up; Select and Menu-down stay native', async () => {
  const use = await useFocusedMenu();
  let focused = false, disabled = false, isTV = true;
  let count = 0;
  const action = () => {count++;};
  const session = await mountHooks(use, () => use(isTV, focused, disabled, action));
  assert.equal(dispatch(), false);
  focused = true; await session.rerender();
  assert.equal(dispatch({eventType: 'select', eventKeyAction: 1}), false);
  assert.equal(dispatch({eventType: 'menu', eventKeyAction: 0}), false);
  assert.equal(dispatch(menu, 'ios'), false);
  assert.equal(dispatch(), true); assert.equal(count, 1);
  disabled = true; await session.rerender();
  assert.equal(dispatch(), false);
  disabled = false; focused = false; await session.rerender();
  assert.equal(dispatch(), false);
  focused = true; isTV = false; await session.rerender();
  assert.equal(dispatch(), false);
  await session.unmount();
});

test('replacement actions, blur, and unmount cannot leave a stale context action', async () => {
  const use = await useFocusedMenu();
  const calls: string[] = [];
  let action: (() => void) | undefined = () => calls.push('old');
  let focused = true;
  const session = await mountHooks(use, () => use(true, focused, false, action));
  dispatch();
  action = () => calls.push('new'); await session.rerender(); dispatch();
  assert.deepEqual(calls, ['old', 'new']);
  action = undefined; await session.rerender(); assert.equal(dispatch(), false);
  action = () => calls.push('restored'); await session.rerender();
  focused = false; await session.rerender(); assert.equal(dispatch(), false);
  focused = true; await session.rerender();
  await session.unmount(); assert.equal(dispatch(), false);
});

test('a late blur cleanup cannot unregister the next focused control', async () => {
  const use = await useFocusedMenu();
  const calls: string[] = [];
  let firstFocused = true;
  const firstAction = () => calls.push('first');
  const secondAction = () => calls.push('second');
  const host = await fabricHost();
  const First = () => {use(true, firstFocused, false, firstAction); return null;};
  const Second = () => {use(true, true, false, secondAction); return null;};
  const both = () => React.createElement(React.Fragment, null, React.createElement(First), React.createElement(Second));
  try {
    await host.render(both());
    firstFocused = false; await host.render(both());
    assert.equal(dispatch(), true); assert.deepEqual(calls, ['second']);
    await host.render(React.createElement(Second));
    assert.equal(dispatch(), true, 'removing the blurred control leaves the focused one registered');
    await host.render(null);
  } finally {await host.unmount();}
  assert.equal(dispatch(), false);
});

test('native nullable disabled state retains an enabled Menu owner and still cleans up on denial', async () => {
  const use = await useFocusedMenu();
  let disabled: boolean | null | undefined = null;
  let calls = 0;
  const action = () => {calls++;};
  const session = await mountHooks(use, () => use(true, true, disabled, action));
  assert.equal(dispatch(), true); assert.equal(calls, 1);
  disabled = true; await session.rerender(); assert.equal(dispatch(), false);
  disabled = undefined; await session.rerender(); assert.equal(dispatch(), true); assert.equal(calls, 2);
  await session.unmount(); assert.equal(dispatch(), false);
});
