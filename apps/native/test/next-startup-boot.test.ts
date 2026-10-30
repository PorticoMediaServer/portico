// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {startupWait} from '../src-next/shell/startup-recovery.ts';

type Node = {type: any; props: Record<string, any>};
const all = (value: any): Node[] => Array.isArray(value) ? value.flatMap(all) : value && typeof value === 'object' && 'props' in value
  ? [value, ...all(value.props.children)] : [];

async function boot(options: {os?: string; platform?: string; isPad?: boolean; reduce?: boolean} = {}) {
  class Value {value: number; constructor(value: number) {this.value = value;} setValue(value: number) {this.value = value;} interpolate(value: unknown) {return value;}}
  const animations: {value: Value; config: any; complete?: (result: {finished: boolean}) => void; stopped: boolean}[] = [];
  const loops: any[] = [], announcements: string[] = [];
  const motion = {reduce: options.reduce === true, duration: (name: string) => options.reduce ? 160 : name === 'slow' ? 320 : name === 'quick' ? 120 : 180};
  const theme = {platform: options.platform ?? 'phone', isTV: options.platform === 'tv', textScale: 1,
    colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#bfcbd4', accentStrong: '#5cb3e2'}};
  const inert = {visible: true, interactive: false, root: true}, live = {...inert, interactive: true};
  const {StartupBoot} = await screenModule(new URL('../src-next/shell/StartupBoot.tsx', import.meta.url), {'./startup-recovery': {startupWait}, 
    react: {...React, default: React},
    'react-native': {Image: 'Image', Text: 'Text', View: 'View', Platform: {OS: options.os ?? 'ios', isPad: options.isPad},
      StyleSheet: {absoluteFillObject: {position: 'absolute', inset: 0}}, Easing: {linear: (value: number) => value, bezier: (...values: number[]) => values},
      AccessibilityInfo: {announceForAccessibility: (message: string) => {announcements.push(message);}},
      Animated: {Value, View: 'AnimatedView', timing: (value: Value, config: unknown) => {
        const animation = {value, config, complete: undefined as ((value: {finished: boolean}) => void) | undefined, stopped: false}; animations.push(animation);
        return {start: (complete?: (value: {finished: boolean}) => void) => {animation.complete = complete;}, stop: () => {animation.stopped = true;}};
      }, sequence: (steps: unknown[]) => ({steps}), loop: (animation: unknown) => {
        const loop = {animation, started: false, stopped: false}; loops.push(loop);
        return {start: () => {loop.started = true;}, stop: () => {loop.stopped = true;}};
      }}},
    'react-native-svg': {default: 'Svg', Circle: 'Circle'},
    // The boot exit runs on Reanimated: a shared value records each timing assigned to it.
    'react-native-reanimated': {default: {View: 'ReanimatedView'}, Easing: {linear: (value: number) => value, bezier: (...values: number[]) => values},
      useSharedValue: (initial: number) => React.useRef((() => {
        let current = initial; const box: any = {};
        Object.defineProperty(box, 'value', {get: () => current, set: (next: any) => {
          if (next && typeof next === 'object' && 'toValue' in next) {
            const animation = {value: box, config: {...next.config, toValue: next.toValue}, complete: next.callback ? (result: {finished: boolean}) => next.callback(result.finished) : undefined, stopped: false};
            animations.push(animation as any); box.running = animation; current = next.toValue;
          } else current = next;
        }});
        return box;
      })()).current,
      withTiming: (toValue: number, config: object, callback?: (finished: boolean) => void) => ({toValue, config, callback}),
      withDelay: (delay: number, animation: any) => ({...animation, config: {...animation.config, delay}}),
      cancelAnimation: (box: any) => {if (box.running) box.running.stopped = true;},
      runOnJS: (fn: (...args: unknown[]) => unknown) => fn,
      useAnimatedStyle: (fn: () => object) => fn()},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: {server: string}) => key === 'accounts.brand' ? 'Portico' : key === 'status.loading' ? 'Loading' : key === 'next.boot.connecting' ? 'Connecting' : key === 'next.boot.stillConnecting' ? 'Still connecting' : `Connecting to ${values!.server}`}},
    '../kit/artwork': {ColourBackdrop: 'ColourBackdrop'},
    '../kit/theme': {useTheme: () => theme, nativeType: (value: unknown) => value}, '../kit/useMotion': {useMotion: () => motion},
    '../kit/overlays/OverlayPaintContext': {OverlayPaintProvider: 'OverlayPaintProvider', OVERLAY_PAINT_INERT: inert, OVERLAY_PAINT_LIVE: live},
  });
  let props: any = {active: true, waiting: startupWait(0, true), serverName: 'Test server', children: React.createElement('Destination'), onHidden: () => {hidden++;}};
  let tree: any, hidden = 0;
  const previousRequire = Object.getOwnPropertyDescriptor(globalThis, 'require');
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {assert.equal(path, '../../assets/wordmark.png'); return 'wordmark';}});
  const restoreRequire = () => {if (previousRequire) Object.defineProperty(globalThis, 'require', previousRequire); else Reflect.deleteProperty(globalThis, 'require');};
  const host = createFabricHookHost(() => {tree = StartupBoot(props);});
  try {await act(() => host.mount());} catch (error) {await host.unmount(); restoreRequire(); throw error;}
  return {host: {unmount: async () => {await host.unmount(); restoreRequire();}}, animations, loops, announcements, hidden: () => hidden, tree: () => tree, nodes: () => all(tree),
    async update(next: Record<string, unknown>) {props = {...props, ...next}; await act(() => host.rerender());}};
}

for (const [name, options, width, shape, spinner] of [
  ['phone', {os: 'ios'}, 176, 'top', 28],
  ['iPad', {os: 'ios', platform: 'tablet', isPad: true}, 224, 'side', 32],
  ['Android tablet', {os: 'android', platform: 'tablet'}, 176, 'side', 32],
  ['Apple TV', {os: 'ios', platform: 'tv'}, 640, 'side', 64],
  // The 1920 design canvas: 640 units are 320 dp (640 px at 1080p).
  ['Android TV', {os: 'android', platform: 'tv'}, 640, 'side', 64],
] as const)
test(`${name} boot uses the bound wordmark and backdrop geometry without moving the wordmark for its spinner`, async () => {
  const f = await boot(options);
  try {
    const image = f.nodes().find(node => node.type === 'Image')!;
    assert.equal(image.props.style.width, width); assert.equal(image.props.style.height, width * 405 / 1774);
    assert.equal(f.nodes().find(node => node.type === 'ColourBackdrop')!.props.shape, shape);
    const before = {...image.props.style}; await f.update({waiting: startupWait(1_000, true)});
    assert.deepEqual(f.nodes().find(node => node.type === 'Image')!.props.style, before);
    assert.equal(f.nodes().find(node => node.type?.name === 'BootSpinner')!.props.size, spinner);
    const overlay = f.nodes().find(node => node.props.testID === 'native-startup-boot')!;
    assert.equal(overlay.props.focusable, false); assert.equal(overlay.props.accessibilityLabel, 'Portico, Loading');
    assert.equal(f.nodes().find(node => node.type === 'OverlayPaintProvider')!.props.mode.interactive, false);
  } finally {await f.host.unmount();}
});

for (const reduce of [false, true])
test(`${reduce ? 'reduced' : 'ordinary'} handoff keeps content fixed, blocks input until the layer leaves, and focuses only after commit`, async () => {
  const f = await boot({reduce});
  try {
    await f.update({active: false});
    // The overlay's fade carries the completion; the content's entrance is the delayed timing.
    const fade = f.animations.find(animation => animation.config.toValue === 0 && animation.complete)!;
    const enter = f.animations.find(animation => animation.config.delay !== undefined)!;
    assert.equal(fade.config.duration, reduce ? 160 : 320);
    assert.equal(enter.config.delay, reduce ? 0 : 60); assert.equal(enter.config.duration, reduce ? 160 : 320);
    assert.equal(f.animations.some(animation => animation.config.toValue === 1.05), !reduce);
    assert.equal(f.hidden(), 0); assert.equal(f.nodes()[1]!.props.importantForAccessibility, 'no-hide-descendants');
    await act(() => fade.complete!({finished: true}));
    assert.equal(f.nodes().some(node => node.props.testID === 'native-startup-boot'), false);
    assert.equal(f.nodes().find(node => node.type === 'OverlayPaintProvider')!.props.mode.interactive, true);
    assert.equal(f.nodes()[1]!.props.importantForAccessibility, 'auto'); assert.equal(f.hidden(), 1);
  } finally {await f.host.unmount();}
});

test('a retired fade completion cannot hide or focus a later boot opening', async () => {
  const f = await boot();
  try {
    await f.update({active: false}); const old = f.animations.find(animation => animation.complete)!;
    await f.update({active: true}); assert.equal(old.stopped, true);
    await act(() => old.complete!({finished: true}));
    assert.equal(f.hidden(), 0); assert.ok(f.nodes().find(node => node.props.testID === 'native-startup-boot'));
  } finally {await f.host.unmount();}
});

test('boot announces its short waiting messages without changing the deadline or server facts', async () => {
  const f = await boot();
  try {
    await f.update({waiting: startupWait(3_000, true)}); await f.update({waiting: startupWait(8_000, true)});
    assert.deepEqual(f.announcements, ['Connecting to Test server', 'Still connecting']);
  } finally {await f.host.unmount();}
});
