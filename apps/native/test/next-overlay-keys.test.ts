import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {Keyboard, NativeModules, Platform, emit} from './react-native-stub.mjs';
import {dispatchRegisteredOverlayKey, registerOverlayKeys} from '../src-next/platform/overlay-keys.ts';
import {dispatchMenuKey, registerFocusedMenu, registerTVMenuFallback} from '../src-next/platform/menu-key.ts';

const modules = NativeModules as unknown as Record<string, unknown>;
function installBridge() {
  const oldOS = Platform.OS;
  const previous = modules.PorticoOverlayKeys;
  Platform.OS = 'android';
  const enabled: boolean[] = [];
  const claims: string[] = [];
  const resolved: [string, boolean][] = [];
  let available = true;
  modules.PorticoOverlayKeys = {
    setEnabled: (value: boolean) => enabled.push(value),
    claimKeyEvent: (id: string) => {claims.push(id); return available;},
    resolveKeyEvent: (id: string, handled: boolean) => resolved.push([id, handled]),
    addListener() {}, removeListeners() {},
  };
  return {enabled, claims, resolved, expire: () => {available = false;}, restore: () => {Platform.OS = oldOS; if (previous === undefined) delete modules.PorticoOverlayKeys; else modules.PorticoOverlayKeys = previous;}};
}

test('only the top callback runs; false does not dismiss a lower overlay, and cleanup reveals it', () => {
  const calls: string[] = [];
  const lower = registerOverlayKeys((key, shift) => {calls.push(`lower:${key}:${shift}`); return true;});
  const upper = registerOverlayKeys((key, shift) => {calls.push(`upper:${key}:${shift}`); return false;});
  try {
    assert.equal(dispatchRegisteredOverlayKey('escape'), false);
    assert.deepEqual(calls, ['upper:escape:false']);
    upper(); upper();
    assert.equal(dispatchRegisteredOverlayKey('tab', true), true);
    assert.deepEqual(calls, ['upper:escape:false', 'lower:tab:true']);
    lower();
    assert.equal(dispatchRegisteredOverlayKey('escape'), false);
  } finally {upper(); lower();}
});

test('cleanup of an older token cannot erase a replacement callback', () => {
  let calls = 0;
  const old = registerOverlayKeys(() => {throw new Error('Retired callback');});
  const current = registerOverlayKeys(() => {calls++; return true;});
  try {old(); old(); assert.equal(dispatchRegisteredOverlayKey('menu'), true); assert.equal(calls, 1);}
  finally {old(); current();}
});

test('Android Escape and Tab/Shift-Tab reach the real callback and acknowledge its handled result', () => {
  const bridge = installBridge();
  const calls: [string, boolean][] = [];
  const remove = registerOverlayKeys((key, shift) => {calls.push([key, shift]); return key === 'escape';});
  try {
    emit('PorticoOverlayKey', {id: '1', key: 'escape', shift: false});
    emit('PorticoOverlayKey', {id: '2', key: 'tab', shift: false});
    emit('PorticoOverlayKey', {id: '3', key: 'tab', shift: true});
    assert.deepEqual(calls, [['escape', false], ['tab', false], ['tab', true]]);
    assert.deepEqual(bridge.claims, ['1', '2', '3']);
    assert.deepEqual(bridge.resolved, [['1', true], ['2', false], ['3', false]]);
    assert.deepEqual(bridge.enabled, [true]);
  } finally {remove(); bridge.restore();}
});

test('one native listener survives lower/upper removals and stops on final unmount without stale dispatch', () => {
  const bridge = installBridge();
  let calls = 0;
  const lower = registerOverlayKeys(() => {calls++; return true;});
  const upper = registerOverlayKeys(() => {calls++; return true;});
  try {
    lower();
    emit('PorticoOverlayKey', {id: '1', key: 'escape', shift: false});
    assert.equal(calls, 1);
    assert.deepEqual(bridge.enabled, [true]);
    upper(); upper();
    emit('PorticoOverlayKey', {id: '2', key: 'escape', shift: false});
    assert.equal(calls, 1);
    assert.deepEqual(bridge.claims, ['1']);
    assert.deepEqual(bridge.enabled, [true, false]);
    assert.equal(dispatchRegisteredOverlayKey('menu'), false);
  } finally {lower(); upper(); bridge.restore();}
});

test('a native-expired key never invokes the renderer or acknowledges a different event', () => {
  const bridge = installBridge();
  let calls = 0;
  const remove = registerOverlayKeys(() => {calls++; return true;});
  try {
    bridge.expire();
    emit('PorticoOverlayKey', {id: 'expired', key: 'escape', shift: false});
    assert.equal(calls, 0);
    assert.deepEqual(bridge.claims, ['expired']);
    assert.deepEqual(bridge.resolved, []);
  } finally {remove(); bridge.restore();}
});

test('a callback failure releases the native key as unhandled', () => {
  const bridge = installBridge();
  const failure = new Error('Failed handler');
  const remove = registerOverlayKeys(() => {throw failure;});
  try {
    assert.throws(() => emit('PorticoOverlayKey', {id: 'failed', key: 'escape', shift: false}), error => error === failure);
    assert.deepEqual(bridge.resolved, [['failed', false]]);
  } finally {remove(); bridge.restore();}
});

test('a synchronous overlay unmount acknowledges its key before disabling native capture', () => {
  const bridge = installBridge();
  const order: string[] = [];
  const native = modules.PorticoOverlayKeys as {setEnabled: (value: boolean) => void; resolveKeyEvent: (id: string, handled: boolean) => void};
  native.setEnabled = value => order.push(`enabled:${value}`);
  native.resolveKeyEvent = (_id, handled) => order.push(`handled:${handled}`);
  let remove = () => {};
  remove = registerOverlayKeys(() => {remove(); return true;});
  try {
    emit('PorticoOverlayKey', {id: 'close', key: 'escape', shift: false});
    assert.deepEqual(order, ['enabled:true', 'handled:true', 'enabled:false']);
    assert.equal(dispatchRegisteredOverlayKey('escape'), false);
  } finally {remove(); bridge.restore();}
});

test('Menu owns keyboard, then top overlay, then focused Hold OK, then the shell rail fallback', () => {
  const calls: string[] = [];
  const fallback = registerTVMenuFallback(() => calls.push('rail'));
  const focus = registerFocusedMenu({}, () => calls.push('focused'));
  const overlay = registerOverlayKeys(key => {calls.push(key); return false;});
  const up = {eventType: 'menu', eventKeyAction: 1};
  const visible = Keyboard.isVisible, dismiss = Keyboard.dismiss;
  try {
    Keyboard.isVisible = () => true; Keyboard.dismiss = () => {calls.push('keyboard');};
    assert.equal(dispatchMenuKey(up, 'android', true), true);
    assert.deepEqual(calls, ['keyboard']);
    Keyboard.isVisible = () => false;
    assert.equal(dispatchMenuKey(up, 'android', true), true, 'inert top overlay blocks underlying actions');
    assert.deepEqual(calls, ['keyboard', 'menu']);
    overlay();
    assert.equal(dispatchMenuKey(up, 'android', true), true);
    assert.deepEqual(calls, ['keyboard', 'menu', 'focused']);
    focus();
    assert.equal(dispatchMenuKey({eventType: 'menu'}, 'android', true), true, 'Vega translates one completed Menu without an action field');
    assert.deepEqual(calls, ['keyboard', 'menu', 'focused', 'rail']);
    for (const event of [{eventType: 'menu', eventKeyAction: 0}, {eventType: 'select', eventKeyAction: 1}, {eventType: 'back', eventKeyAction: 1}, {eventType: 'playPause', eventKeyAction: 1}]) assert.equal(dispatchMenuKey(event, 'android', true), false);
    assert.equal(dispatchMenuKey(up, 'ios', true), false, 'Apple Menu remains Back');
    assert.equal(dispatchMenuKey(up, 'android', false), false);
    fallback();
    assert.equal(dispatchMenuKey(up, 'android', true), false);
  } finally {Keyboard.isVisible = visible; Keyboard.dismiss = dismiss; focus(); overlay(); fallback();}
});

test('a retired shell fallback cannot remove the current owner', () => {
  const calls: string[] = [];
  const old = registerTVMenuFallback(() => calls.push('old'));
  const current = registerTVMenuFallback(() => calls.push('current'));
  try {old(); dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', true); assert.deepEqual(calls, ['current']);}
  finally {old(); current();}
});

test('missing native bridge leaves callback registration and Menu usable without pretending hardware capture exists', () => {
  const previous = modules.PorticoOverlayKeys;
  const oldOS = Platform.OS;
  Platform.OS = 'android'; delete modules.PorticoOverlayKeys;
  const remove = registerOverlayKeys(key => key === 'menu');
  try {assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', true), true);}
  finally {remove(); Platform.OS = oldOS; if (previous !== undefined) modules.PorticoOverlayKeys = previous;}
});

test('native interception is narrow, preserves ordinary fallback and leaves native modal/Menu handling in place', () => {
  const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');
  const activity = source('android/app/src/main/java/tv/getportico/portico/MainActivity.kt');
  const bridge = source('android/app/src/main/java/tv/getportico/portico/native/PorticoOverlayKeys.kt');
  assert.match(activity, /hasWindowFocus\(\).*PorticoOverlayKeys\.dispatch\(event\).*super\.dispatchKeyEvent\(it\)/);
  assert.match(activity, /window\.superDispatchKeyEvent\(event\)/);
  assert.match(bridge, /KEYCODE_ESCAPE && event\.keyCode != KeyEvent\.KEYCODE_TAB/);
  assert.match(bridge, /if \(!handled\) stroke\.replay\(event\)/);
  assert.match(bridge, /isBlockingSynchronousMethod = true/);
  assert.match(bridge, /onlyUnclaimed && stroke\.claimed/);
  assert.doesNotMatch(bridge, /KEYCODE_(BACK|MENU|DPAD_CENTER|MEDIA_)/);
  assert.match(source('android/app/src/main/java/tv/getportico/portico/native/PorticoPackage.kt'), /PorticoOverlayKeys\(reactContext\)/);
});
