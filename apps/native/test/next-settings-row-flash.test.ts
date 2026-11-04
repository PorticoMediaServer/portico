import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
// Sol 8's controls on #823, on the shared hook renderer (it has no imports of its own).
import {createRenderer} from '../../../web/test/helpers/interaction-harness.mjs';

async function fixture(reduced = false) {
  const host = createRenderer(); let reveals = 0, animations = 0; const motion = {timing: () => ({})};
  const {SettingsRowFlash} = await screenModule(new URL('../src-next/kit/shell/SettingsRowTarget.tsx', import.meta.url), {
    react: host.react, 'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: (fn: () => unknown) => fn(), useSharedValue: (value: unknown) => host.react.useRef({value}).current,
      withTiming: (value: unknown) => {animations++; return value;}},
    '../useMotion': {useMotion: () => motion, useReducedMotion: () => reduced}, '../theme': {useTheme: () => ({colors: {accent: '#409fd4'}})}, './visual': {alpha: () => '#409fd4'},
  });
  let serial = 1;
  const render = () => host.render(() => host.react.createElement(SettingsRowFlash, {serial, reveal: () => reveals++}));
  return {host, render, setSerial: (value: number) => {serial = value;}, get reveals() {return reveals;}, get animations() {return animations;}};
}

test('a row flash measures once however often it lays out, and with reduced motion reveals without flashing', async () => {
  const f = await fixture(true);
  try {const row: any = f.render(); row.props.onLayout(); row.props.onLayout(); assert.equal(f.reveals, 1); assert.equal(f.animations, 0);}
  finally {f.host.dispose();}
});

test('a new opening serial reveals the same mounted tablet row even when its layout is unchanged (Sol 8 on #823)', async () => {
  const f = await fixture();
  try {
    let row: any = f.render(); row.props.onLayout(); assert.equal(f.reveals, 1);
    f.setSerial(2); row = f.render();
    assert.equal(f.reveals, 2, 'a new opening reveals without waiting for another native layout event');
  } finally {f.host.dispose();}
});
