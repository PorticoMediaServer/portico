import test from 'node:test';
import assert from 'node:assert/strict';
import {enUS} from '../../../packages/i18n/src/index.ts';
import {controlsNativeMessages} from '../../../packages/i18n/src/catalog/en-US/next/controls-native.ts';
import {nativeControlsFixtureMessages} from '../../../packages/i18n/src/catalog/en-US/next/dev/controls-native-fixtures.ts';

test('native controls register their real catalogue keys and keep media samples out of the production catalogue', () => {
  for (const [key, value] of Object.entries(controlsNativeMessages)) {
    assert.ok(key.startsWith('next.controls.native.'));
    assert.ok(!key.startsWith('next.controls.native.fixture.'));
    assert.equal(enUS[key as keyof typeof enUS], value);
  }
  for (const key of Object.keys(nativeControlsFixtureMessages)) {
    assert.ok(key.startsWith('next.controls.native.fixture.'));
    assert.equal(Object.hasOwn(enUS, key), false);
  }
  assert.equal(Object.hasOwn(enUS, 'next.controls.native.longTitle'), false);
});
