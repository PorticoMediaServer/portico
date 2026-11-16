// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {fieldSpec} from '@portico/design';
import {createTheme} from '../src-next/kit/theme.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

type Element = React.ReactElement<Record<string, any>>;
function nodes(value: any): Element[] {
  return Array.isArray(value) ? value.flatMap(nodes) : React.isValidElement<Record<string, any>>(value) ? [value, ...nodes(value.props.children)] : [];
}

test('TV text entry draws one box, opens the keyboard only on Select, and edits the existing value once', async () => {
  const theme = createTheme({isTV: true, width: 1920});
  const {TVTextField} = await screenModule(new URL('../src-next/kit/controls/TVTextField.tsx', import.meta.url), {
    react: {...React, default: React}, '@portico/design': {fieldSpec},
    'react-native': {View: 'View', Text: 'Text', TextInput: 'TextInput', StyleSheet: {create: (value: any) => value, flatten: (value: any) => value}},
    './Focusable': {Focusable: 'Focusable'}, './FormError': {FormError: 'FormError'}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => theme}, '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive: true})},
  });
  let tree: Element, value = 'https://demo.getportico.tv', focused = 0, returned = 0, submitted = 0, secure = false;
  const host = createFabricHookHost(() => {tree = TVTextField.render({label: 'Server address', value, secureTextEntry: secure,
    onChangeText: (next: string) => {value = next;}, onSubmitEditing: () => {submitted++;}}, null);});
  const field = () => nodes(tree).find(node => node.type === 'Focusable')!;
  const keyboard = () => nodes(tree).find(node => node.type === 'TextInput')!;
  try {
    await host.mount();
    keyboard().props.ref.current = {focus() {focused++;}, blur() {}};
    field().props.ref.current = {requestTVFocus() {returned++;}};
    assert.equal(focused, 0);
    assert.equal(keyboard().props.focusable, false);
    assert.equal(keyboard().props.style.opacity, 0);
    assert.equal(keyboard().props.style.position, 'absolute');
    assert.equal(nodes(field()).filter(node => node.type === 'TextInput').length, 0, 'no native input chrome in the visible box');
    assert.equal(nodes(field()).find(node => node.type === 'Text')!.props.ellipsizeMode, 'head');
    field().props.onPress(); await host.flush();
    assert.equal(focused, 1);
    assert.equal(keyboard().props.value, value, 'Select starts from the existing value');
    keyboard().props.onChangeText('https://demo.getportico.tv/edited'); await host.rerender();
    assert.equal(keyboard().props.value, 'https://demo.getportico.tv/edited', 'a keyboard edit replaces the value; it is never concatenated');
    keyboard().props.onSubmitEditing({nativeEvent: {text: value}}); await host.flush();
    keyboard().props.onBlur(); await host.flush();
    assert.equal(submitted, 1);
    assert.equal(returned, 0, 'a caller moving to the next field is not overridden by keyboard dismissal');
    secure = true; await host.rerender();
    assert.equal(nodes(field()).find(node => node.type === 'Text')!.props.children, '•'.repeat(value.length));
    assert.equal(field().props.accessibilityValue.text.includes('https'), false, 'the spoken value is masked too');
  } finally {await host.unmount();}
});
