// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {fieldSpec, typography} from '@portico/design';
import {createTheme} from '../src-next/kit/theme.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

type Element = React.ReactElement<Record<string, any>>;
function nodes(value: any): Element[] {
  return Array.isArray(value) ? value.flatMap(nodes) : React.isValidElement<Record<string, any>>(value) ? [value, ...nodes(value.props.children)] : [];
}

test('TV text entry draws one box, opens the keyboard only on Select, and edits the existing value once', async () => {
  const theme = createTheme({isTV: true, width: 1920});
  let keyboardDidHide: (() => void) | undefined;
  const {TVTextField} = await screenModule(new URL('../src-next/kit/controls/TVTextField.tsx', import.meta.url), {
    react: {...React, default: React}, '@portico/design': {fieldSpec},
    'react-native': {View: 'View', Text: 'Text', TextInput: 'TextInput', Platform: {OS: 'android'}, useTVEventHandler: () => {},
      Keyboard: {addListener(event: string, callback: () => void) {
        assert.equal(event, 'keyboardDidHide');
        keyboardDidHide = callback;
        return {remove: () => {keyboardDidHide = undefined;}};
      }}, StyleSheet: {create: (value: any) => value, flatten: (value: any) => value}},
    './Focusable': {Focusable: 'Focusable'}, './FormError': {FormError: 'FormError'}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => theme}, '../overlays/OverlayHost': {useInTopLayer: () => true}, '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive: true})},
  });
  let tree!: Element, value = 'https://server.example', returned = 0, submitted = 0, secure = false, cleared = 0;
  const forwarded = {current: null as any};
  const host = createFabricHookHost(() => {tree = TVTextField.render({label: 'Server address', value, secureTextEntry: secure,
    nativeID: 'server-address-editor', onChangeText: (next: string) => {value = next;}, onSubmitEditing: () => {submitted++;}}, forwarded);});
  const field = () => nodes(tree).find(node => node.type === 'Focusable')!;
  const keyboard = () => nodes(tree).find(node => node.type === 'TextInput')!;
  try {
    await host.mount();
    keyboard().props.ref.current = new class {blur() {} clear() {cleared++;} isFocused() {return true;}}();
    field().props.ref.current = {requestTVFocus() {returned++;}};
    const label = nodes(tree).find(node => node.type === 'Text' && node.props.children === 'Server address')!;
    assert.equal(label.props.style[0].fontSize, 22);
    assert.equal(label.props.style[1].color, theme.colors.textTertiary);
    assert.equal(field().props.focusKind, 'field');
    const paint = (focused: boolean) => Object.assign({}, ...field().props.style({focused}).flat());
    assert.equal(paint(false).minHeight, 84);
    assert.equal(paint(false).borderRadius, 16);
    assert.equal(paint(false).paddingHorizontal, 28);
    assert.equal(paint(false).backgroundColor, '#0710189e');
    assert.equal(paint(true).backgroundColor, theme.colors.raisedHigh);
    assert.equal(paint(true).borderColor, theme.colors.accent);
    const valueStyle = Object.assign({}, ...nodes(field()).find(node => node.type === 'Text')!.props.style.flat(2));
    assert.equal(valueStyle.fontSize, 32); assert.equal(valueStyle.fontFamily, typography.semibold);
    const idleKey = keyboard().key;
    assert.equal(keyboard().props.autoFocus, false);
    assert.equal(keyboard().props.focusable, false);
    assert.equal(keyboard().props.nativeID, 'server-address-editor');
    assert.equal(keyboard().props.accessibilityLabel, 'Server address');
    forwarded.current.clear(); assert.equal(cleared, 1, 'ref forwards prototype methods instead of spreading a native instance');
    assert.equal(forwarded.current.isFocused(), true);
    assert.equal(keyboard().props.style.opacity, 0);
    assert.equal(keyboard().props.style.position, 'absolute');
    assert.equal(nodes(field()).filter(node => node.type === 'TextInput').length, 0, 'no native input chrome in the visible box');
    assert.equal(nodes(field()).find(node => node.type === 'Text')!.props.ellipsizeMode, 'head');
    field().props.onPress(); await host.flush();
    assert.notEqual(keyboard().key, idleKey, 'Select mounts an input that can autofocus in its native window');
    assert.equal(keyboard().props.autoFocus, true);
    assert.equal(keyboard().props.value, value, 'Select starts from the existing value');
    keyboard().props.onChangeText('https://server.example/edited'); await host.rerender();
    assert.equal(keyboard().props.value, 'https://server.example/edited', 'a keyboard edit replaces the value; it is never concatenated');
    keyboard().props.onSubmitEditing({nativeEvent: {text: value}}); await host.flush();
    keyboard().props.onBlur(); await host.flush();
    assert.equal(submitted, 1);
    assert.equal(returned, 0, 'a caller moving to the next field is not overridden by keyboard dismissal');
    field().props.onAccessibilityTap(); await host.flush();
    assert.equal(keyboard().props.autoFocus, true);
    assert.ok(keyboardDidHide);
    keyboardDidHide(); await host.flush();
    assert.equal(keyboard().props.autoFocus, false);
    assert.equal(returned, 1);
    secure = true; await host.rerender();
    assert.equal(nodes(field()).find(node => node.type === 'Text')!.props.children, '•'.repeat(value.length));
    assert.equal(field().props.accessibilityValue.text.includes('https'), false, 'the spoken value is masked too');
  } finally {await host.unmount();}
});

test('Apple TV: while the drawn field holds focus, a Select opens it even when tvOS names another view (the sign-in page\'s Server address)', async () => {
  const theme = createTheme({isTV: true, width: 1920});
  let handler: ((event: {eventType: string; tag?: number}) => void) | undefined;
  const {TVTextField} = await screenModule(new URL('../src-next/kit/controls/TVTextField.tsx', import.meta.url), {
    react: {...React, default: React}, '@portico/design': {fieldSpec},
    'react-native': {View: 'View', Text: 'Text', TextInput: 'TextInput', Platform: {OS: 'ios'}, useTVEventHandler: (next: typeof handler) => {handler = next;},
      Keyboard: {addListener: () => ({remove() {}})}, StyleSheet: {create: (value: any) => value, flatten: (value: any) => value}},
    './Focusable': {Focusable: 'Focusable'}, './FormError': {FormError: 'FormError'}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => theme}, '../overlays/OverlayHost': {useInTopLayer: () => true}, '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive: true})},
  });
  let tree!: Element;
  const host = createFabricHookHost(() => {tree = TVTextField.render({label: 'Server address', value: '', onChangeText: () => {}}, {current: null});});
  const field = () => nodes(tree).find(node => node.type === 'Focusable')!;
  const editor = () => nodes(tree).find(node => node.type === 'TextInput');
  try {
    await host.mount();
    assert.equal(editor(), undefined, 'no editor until Select');
    handler!({eventType: 'select', tag: 11}); await host.flush();
    assert.equal(editor(), undefined, 'a Select elsewhere, while the field does not hold focus, opens nothing');
    field().props.onFocus({}); handler!({eventType: 'select', tag: 11}); await host.flush();
    assert.ok(editor(), 'the field holds focus: Select opens its editor and so the keyboard');
  } finally {await host.unmount();}
});
