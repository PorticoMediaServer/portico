// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

test('phone PIN keeps one real numeric input, strips non-digits and submits the fourth digit once', async () => {
  const {PhonePINEntry} = await screenModule(new URL('../src-next/screens/accounts/PhonePINEntry.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', TextInput: 'TextInput', StyleSheet: {absoluteFill: {position: 'absolute', left: 0, right: 0, top: 0, bottom: 0}}},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
  });
  const completed: string[] = [];
  let setValue: (value: string) => void = () => {}, tree: any;
  const host = createFabricHookHost(() => {
    const [value, set] = React.useState('');
    setValue = set;
    tree = PhonePINEntry({value, onChange: set, onComplete: (code: string) => completed.push(code)});
  });
  await act(() => host.mount());
  try {
    const input = () => nodes(tree).find(node => node.type === 'TextInput');
    assert.equal(input().props.keyboardType, 'number-pad');
    assert.equal(input().props.maxLength, 4);
    assert.equal(input().props.accessibilityLabel, defaultI18n.t('pin.fourDigit'));
    await act(() => input().props.onChangeText('1'));
    await act(() => input().props.onChangeText('12a'));
    assert.deepEqual(completed, [], 'a partial or non-numeric entry never submits');
    await act(() => input().props.onChangeText('1234'));
    assert.deepEqual(completed, ['1234'], 'the fourth digit submits once');
    await act(() => input().props.onChangeText('12345'));
    assert.deepEqual(completed, ['1234'], 'an overlong edit does not submit again');
    await act(() => setValue(''));
    await act(() => input().props.onChangeText('1234'));
    assert.deepEqual(completed, ['1234', '1234'], 'a cleared wrong PIN can be entered again');
  } finally {await host.unmount();}
});
