// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {useControlValue} from '../src-next/kit/controls/useControlValue.ts';
import {enabledChoice, minimumHitSlop, progressFraction, buttonGeometry, chipGeometry, switchDragFraction, switchReleaseValue} from '../src-next/kit/controls/control-model.ts';

test('an uncontrolled control retains real input and reports only value changes', async () => {
  let value = false;
  let change: (next: boolean) => void = () => {throw new Error('Control not mounted.');};
  const calls: boolean[] = [];
  const host = createFabricHookHost(() => {[value, change] = useControlValue(undefined, false, next => calls.push(next));});
  await host.mount();
  assert.equal(value, false);
  await act(() => change(true));
  assert.equal(value, true);
  await act(() => change(true));
  assert.deepEqual(calls, [true]);
  await host.rerender();
  assert.equal(value, true);
  await act(() => change(false));
  assert.equal(value, false);
  assert.deepEqual(calls, [true, false]);
  await host.unmount();
});

test('controlled input requests a change without overriding the caller and reads its new callback', async () => {
  let authoritative = 'original';
  let current = '';
  let request: (next: string) => void = () => {throw new Error('Control not mounted.');};
  const oldCalls: string[] = [], newCalls: string[] = [];
  let callback = (next: string) => oldCalls.push(next);
  const host = createFabricHookHost(() => {[current, request] = useControlValue(authoritative, 'auto', callback);});
  await host.mount();
  await act(() => request('auto'));
  assert.equal(current, 'original');
  assert.deepEqual(oldCalls, ['auto']);
  authoritative = 'auto';
  callback = next => newCalls.push(next);
  await host.rerender();
  assert.equal(current, 'auto');
  await act(() => request('auto'));
  await act(() => request('720'));
  assert.deepEqual(newCalls, ['720']);
  assert.deepEqual(oldCalls, ['auto']);
  await host.unmount();
});

test('native picker initial focus retains an enabled selection and falls back around disabled or missing values', () => {
  const options = [{value: 'original'}, {value: '4k', disabled: true}, {value: '1080'}, {value: '720'}];
  assert.equal(enabledChoice(options, '4k'), 'original');
  assert.equal(enabledChoice(options, '1080'), '1080');
  assert.equal(enabledChoice(options, 'removed'), 'original');
  assert.equal(enabledChoice([]), undefined);
  assert.equal(enabledChoice([{value: 'only', disabled: true}], 'only'), undefined);
});

test('invalid progress never creates negative, overflowing, or nonfinite native width', () => {
  for (const [input, expected] of [[-1, 0], [0, 0], [.09, .09], [.67, .67], [1, 1], [1.01, 1], [NaN, 0], [Infinity, 0], [-Infinity, 0]]) {
    assert.equal(progressFraction(input!), expected);
  }
});

test('drawn utility controls extend their touch target without inflating their visible geometry', () => {
  const sizes = ['hero', 'compact', 'small', 'xs', 'episode', 'dock'] as const;
  for (const size of sizes) {
    const phone = buttonGeometry(size, 'phone'), tv = buttonGeometry(size, 'tv');
    assert.ok(phone.height + 2 * minimumHitSlop(phone.height, false) >= 44);
    assert.ok(tv.height + 2 * minimumHitSlop(tv.height, true) >= 64);
    assert.ok(phone.icon < phone.height);
  }
  for (const variant of ['default', 'quick', 'facet', 'applied', 'pill'] as const) {
    for (const platform of ['phone', 'tablet', 'tv'] as const) {
      const geometry = chipGeometry(variant, platform);
      assert.ok(geometry.height + 2 * minimumHitSlop(geometry.height, platform === 'tv') >= (platform === 'tv' ? 64 : 44));
    }
  }
  assert.equal(buttonGeometry('hero', 'phone').height, 46);
  assert.equal(buttonGeometry('hero', 'tv').height, 72);
  assert.equal(buttonGeometry('xs', 'phone').height, 34);
});

test('switch drags track the finger within the stops and release with the actual velocity', () => {
  assert.equal(switchDragFraction(0, 9.5, 19), .5);
  assert.ok(Math.abs(switchDragFraction(.7, -3.8, 19) - .5) < Number.EPSILON);
  assert.equal(switchDragFraction(0, -20, 19), 0);
  assert.equal(switchDragFraction(1, 20, 19), 1);
  assert.equal(switchReleaseValue(.49, 0, 19), false);
  assert.equal(switchReleaseValue(.51, 0, 19), true);
  assert.equal(switchReleaseValue(.2, 100, 19), true);
  assert.equal(switchReleaseValue(.8, -100, 19), false);
});

test('desktop controls retain their drawn geometry instead of using handheld sizes', () => {
  assert.deepEqual(buttonGeometry('hero', 'web'), {height: 54, radius: 12, paddingX: 26, gap: 10, icon: 22, fontSize: 17});
  assert.equal(buttonGeometry('compact', 'web').icon, 22);
  assert.equal(buttonGeometry('hero', 'web', 'icon').height, 40);
  assert.equal(buttonGeometry('hero', 'web', 'text').height, 32);
  assert.deepEqual(buttonGeometry('dock', 'web'), {height: 38, radius: 10, paddingX: 14, gap: 8, icon: 18, fontSize: 14});
  assert.equal(buttonGeometry('dock', 'phone').height, 32);
  assert.deepEqual(chipGeometry('default', 'web'), {height: 40, radius: 10, paddingX: 16, gap: 8, fontSize: 15});
  assert.deepEqual(chipGeometry('quick', 'web'), {height: 40, radius: 20, paddingX: 15, gap: 7, fontSize: 14.5});
  assert.deepEqual(chipGeometry('facet', 'web'), {height: 34, radius: 17, paddingX: 12, gap: 7, fontSize: 14});
  assert.equal(chipGeometry('applied', 'web').height, 34);
  assert.equal(chipGeometry('applied', 'phone').height, 30);
});
