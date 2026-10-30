import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';

const load = (platform: {OS: string; isTV: boolean}, native: unknown) => screenModule(new URL('../src-next/screens/settings/top-shelf-locale.ts', import.meta.url), {
  react: {...React, default: React},
  'react-native': {Platform: platform, NativeModules: {PorticoTopShelf: native}},
});

test('the existing tvOS command receives the resolved locale initially and only when its confirmed source changes', async () => {
  let locale = 'en-GB', current = true, subscriptions = 0;
  const listeners = new Set<() => void>(), sent: string[] = [], errors: unknown[] = [];
  const native = {setLocale(tag: string) {assert.equal(this, native, 'preserve the actual native method receiver'); sent.push(tag);}};
  const {bindNativeTopShelfLocale} = await load({OS: 'ios', isTV: true}, native);
  const retire = bindNativeTopShelfLocale({current: () => current, subscribePreferences: (listener: () => void) => {
    subscriptions++; listeners.add(listener); return () => {listeners.delete(listener);};
  }}, {t: defaultI18n.t, get locale() {return locale;}}, (error: unknown) => {errors.push(error);});
  assert.equal(typeof retire, 'function'); assert.equal(subscriptions, 1);
  assert.deepEqual(sent, ['en-GB']);
  const queued = [...listeners][0]!;
  queued(); assert.deepEqual(sent, ['en-GB'], 'an unchanged confirmed locale is deduplicated');
  locale = 'en-CA'; queued(); assert.deepEqual(sent, ['en-GB', 'en-CA']);
  current = false; locale = 'en-US'; queued();
  assert.deepEqual(sent, ['en-GB', 'en-CA'], 'retired authority cannot send another profile locale');
  retire(); assert.equal(listeners.size, 0);
  current = true; queued(); retire();
  assert.deepEqual(sent, ['en-GB', 'en-CA'], 'cleanup is monotonic even for an already queued notification');
  assert.deepEqual(errors, []);
});

test('phones, Android TV and missing producers do not subscribe or invent a locale command', async () => {
  for (const [platform, native] of [
    [{OS: 'ios', isTV: false}, {}], [{OS: 'android', isTV: true}, {}], [{OS: 'ios', isTV: true}, undefined],
  ] as const) {
    const {bindNativeTopShelfLocale} = await load(platform, native);
    assert.equal(bindNativeTopShelfLocale({current: () => true, subscribePreferences() {throw new Error('unexpected_subscription');}},
      {t: defaultI18n.t, get locale() {throw new Error('unexpected_locale_read');}}, () => {throw new Error('unexpected_error');}), undefined);
  }
  const {bindNativeTopShelfLocale} = await load({OS: 'ios', isTV: true}, {setLocale() {throw new Error('unexpected_native_command');}});
  assert.equal(bindNativeTopShelfLocale({current: () => false, subscribePreferences() {throw new Error('unexpected_subscription');}},
    {t: defaultI18n.t, get locale() {throw new Error('unexpected_locale_read');}}, () => {throw new Error('unexpected_error');}), undefined, 'a retired bootstrap cannot install a new borrower');
});

test('native command refusal uses the same feedback and does not mark an undelivered locale as delivered', async () => {
  let listener: (() => void) | undefined, attempts = 0;
  const refused = new Error('native locale refused'), errors: unknown[] = [];
  const {bindNativeTopShelfLocale} = await load({OS: 'ios', isTV: true}, {setLocale() {if (++attempts === 1) throw refused;}});
  const retire = bindNativeTopShelfLocale({current: () => true, subscribePreferences: (next: () => void) => {listener = next; return () => {listener = undefined;};}},
    {t: defaultI18n.t, locale: 'en-GB'}, (error: unknown) => {errors.push(error);});
  assert.deepEqual(errors, [refused]); assert.equal(attempts, 1);
  listener!(); assert.equal(attempts, 2);
  listener!(); assert.equal(attempts, 2);
  retire(); assert.equal(listener, undefined);
});
