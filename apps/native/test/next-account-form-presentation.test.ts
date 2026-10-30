// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as resetPaths from '../src-next/screens/accounts/reset-file-paths';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flatten = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
const copy = {t: (key: string) => key, list: defaultI18n.list};
const theme = {platform: 'phone', isTV: false, textScale: 1, type: {body: {fontSize: 15}, caption: {}, captionStrong: {}},
  colors: {text: '#edf5fa', textSecondary: '#afc4d2', textTertiary: '#7995a6', ink: '#071018', danger: '#f38b88', amber: '#e8bc6a', healthy: '#86cfb3',
    accent: '#2f86bb', focus: '#b8dbe8', lineControl: '#29414f', raised: '#0e1b24', raisedHigh: '#1c3341', placeholder: '#7995a6'}};
const nativeType = (value: any, scale: number) => ({fontSize: value.size * scale, lineHeight: value.lineHeight * scale});
const react = {...React, default: React};

test('compact auth form keeps Back above its left-aligned heading, with actions reachable above the keyboard', async context => {
  const listeners = new Map<string, () => void>(); let back = 0, tree: any;
  const {PhoneAccountForm} = await screenModule(new URL('../src-next/screens/accounts/PhoneAccountForm.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'FormScrollView'},
    react, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView', KeyboardAvoidingView: 'KeyboardAvoidingView', Platform: {OS: 'android'},
      Keyboard: {isVisible: () => false, addListener: (name: string, handler: () => void) => {listeners.set(name, handler); return {remove: () => listeners.delete(name)};}}},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: unknown) => React.useRef({value}).current, useAnimatedStyle: (work: () => unknown) => work(), withTiming: (value: unknown) => value},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../kit/artwork': {ColourBackdrop: 'Backdrop'}, '../../kit/theme': {useTheme: () => theme, nativeType},
    '../../kit/useMotion': {useMotion: () => ({timing: () => ({duration: 160})})}, '../../kit/states/StateActionButton': {StateActionButton: 'Action'},
  });
  const host = createFabricHookHost(() => {tree = PhoneAccountForm({title: 'Create account', body: 'Account details', testID: 'form',
    beforeActions: React.createElement('TextInput', {testID: 'draft'}), primaryAction: {id: 'continue', label: 'Continue', onPress() {}},
    secondaryAction: {id: 'back', label: 'Back', onPress: () => {back++;}}});});
  context.after(() => host.unmount()); await act(() => host.mount());
  let all = nodes(tree); const actions = all.filter(node => node.type === 'Action');
  assert.deepEqual(actions.map(node => node.props.action.id), ['back', 'continue']);
  assert.ok(all.indexOf(actions[0]) < all.findIndex(node => node.props.accessibilityRole === 'header'));
  assert.equal(flatten(all.find(node => node.props.accessibilityRole === 'header').props.style).fontSize, 34);
  assert.equal(flatten(all.find(node => node.props.accessibilityRole === 'header').props.style).textAlign, 'left');
  const scroll = all.find(node => node.type === 'FormScrollView');
  assert.equal(scroll.props.contentContainerStyle.paddingHorizontal, 22);
  assert.equal(actions[1].props.style.minHeight, 48); assert.equal(actions[1].props.style.height, undefined);
  // The keyboard controller keeps the focused field and the next action in view on both platforms.
  assert.equal(scroll.props.bottomOffset, 80); assert.equal(scroll.props.keyboardShouldPersistTaps, 'handled');
  assert.equal(all.some(node => node.type === 'KeyboardAvoidingView'), false);
  assert.equal(all.filter(node => node.props.testID === 'draft').length, 1);
  actions[0].props.action.onPress(); assert.equal(back, 1);
  await host.unmount(); assert.equal(listeners.size, 0);
});

test('new-password strength uses all four shared-policy segments and the exact label', async context => {
  const {PasswordStrengthMeter} = await screenModule(new URL('../src-next/screens/accounts/PasswordStrengthMeter.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', Text: 'Text'}, '../../../../../packages/i18n/src': {defaultI18n: copy},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy, '../../kit/theme': {useTheme: () => theme},
  });
  let value = '', tree: any; const host = createFabricHookHost(() => {tree = PasswordStrengthMeter({password: value});});
  context.after(() => host.unmount()); await act(() => host.mount()); const rendered = () => tree; assert.equal(rendered(), null);
  for (const [password, count, label] of [['short', 0, 'services.password.tooShort'], ['password', 1, 'accounts.password.weak'], ['correct horse battery staple', 4, 'services.password.strong']] as const) {
    value = password; await act(() => host.rerender());
    assert.equal(tree.props.accessibilityLabel, label);
    const segments = nodes(tree).filter(node => node.props.style?.height === 4);
    assert.equal(segments.length, 4);
    assert.equal(segments.filter(node => node.props.style.backgroundColor !== theme.colors.lineControl).length, count);
  }
});

test('owner reset disclosure expands the other systems without replacing the returned server path', async context => {
  const {PhoneOwnerReset} = await screenModule(new URL('../src-next/screens/accounts/PhoneOwnerReset.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', Text: 'Text'}, '../../../../../packages/i18n/src': {defaultI18n: copy},
    '../../kit/states': {StateLayout: 'State'}, '../../kit/controls': {TextField: 'Field', Button: 'Button'}, '../../kit/theme': {useTheme: () => theme},
    './PhoneAccountForm': {PhoneAccountForm: 'Form'}, './reset-file-paths': resetPaths,
  });
  let tree: any; const host = createFabricHookHost(() => {tree = PhoneOwnerReset({phase: 'code', serverName: 'Server', code: '', onCodeChange() {}, busy: false,
    location: {systemKind: 'docker', dataDirectory: '/actual', fileDirectory: '/actual', fileName: 'reset-code.txt', retryAfterSeconds: 0}, onBack() {}, onWrite() {}, onVerify() {}, onContinue() {}});});
  context.after(() => host.unmount()); await act(() => host.mount());
  const all = () => nodes(tree.props.beforeActions);
  assert.equal(tree.type, 'Form'); assert.ok(all().some(node => node.props.children === '/actual/reset-code.txt'));
  assert.equal(all().find(node => node.props.testID === 'accounts-owner-reset-other-paths'), undefined);
  await act(() => all().find(node => node.props.testID === 'accounts-owner-reset-systems').props.onPress());
  assert.equal(all().find(node => node.props.testID === 'accounts-owner-reset-systems').props.accessibilityState.expanded, true);
  const disclosure = all().find(node => node.props.testID === 'accounts-owner-reset-other-paths');
  assert.deepEqual(disclosure.props.children.map((node: any) => node.key), ['linux', 'windows', 'macos']);
  assert.ok(all().some(node => node.props.children === '/actual/reset-code.txt'));
});

for (const os of ['ios', 'android'] as const) {
  test(`auth TextField keeps autofill and native input inside the ${os} glass treatment`, async context => {
    const changed: string[] = []; let tree: any, nativeBlur = true, appearance: 'auth' | 'default' = 'auth';
    const spec = {height: {phone: 44}, radius: {phone: 8}, paddingX: {phone: 12}, borderWidth: {phone: 1}, search: {focusAlpha: .2}};
    const {TextField} = await screenModule(new URL('../src-next/kit/controls/TextField.tsx', import.meta.url), {
      react, 'react-native': {View: 'View', Text: 'Text', TextInput: 'TextInput', Platform: {OS: os}, StyleSheet: {create: (value: unknown) => value, flatten, absoluteFill: {position: 'absolute', inset: 0}}},
      '@portico/design': {fieldSpec: spec}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => theme, fontFamilies: {400: 'regular', 500: 'medium', 600: 'semibold'}},
      './control-model': {colorAlpha: (color: string, alpha: number) => color + ':' + alpha},
      '../focus': {useFocusTarget: () => ({waiting: false, ref: {current: null}, testID: 'credential', onFocus() {}, onBlur() {}})},
      '../overlays/OverlayPaintContext': {useOverlayPaintMode: () => ({interactive: true})},
      '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur, backgroundColor: '#1c3341', backgroundAlpha: .8})},
    });
    const host = createFabricHookHost(() => {tree = TextField.render({appearance, label: 'Password', value: 'value', secureTextEntry: true,
      autoComplete: 'new-password', textContentType: 'newPassword', onChangeText: (value: string) => changed.push(value)}, null);});
    context.after(() => host.unmount()); await act(() => host.mount());
    const input = nodes(tree).find(node => node.type === 'TextInput');
    assert.equal(input.props.autoComplete, 'new-password'); assert.equal(input.props.textContentType, 'newPassword'); assert.equal(input.props.secureTextEntry, true);
    assert.equal(flatten(input.props.style).fontSize, 16); assert.equal(flatten(input.props.style).fontFamily, 'semibold');
    const face = nodes(tree).find(node => flatten(node.props.style).backgroundColor === 'rgba(7,16,24,.62)');
    assert.ok(face); assert.equal(flatten(face.props.style).minHeight, 48); assert.equal(flatten(face.props.style).borderRadius, 11); assert.equal(flatten(face.props.style).paddingHorizontal, 14);
    const blur = nodes(tree).find(node => node.type === 'BlurView'); assert.equal(blur.props.blurAmount, 16);
    if (os === 'android') assert.ok(nodes(blur).includes(input), 'the native input is excluded from Android backdrop capture');
    await act(() => input.props.onChangeText('new value')); assert.deepEqual(changed, ['new value']);
    nativeBlur = false; await act(() => host.rerender());
    assert.equal(nodes(tree).some(node => node.type === 'BlurView'), false);
    assert.ok(nodes(tree).some(node => flatten(node.props.style).backgroundColor === '#1c3341:0.8'), 'auth respects the existing glass fallback policy');
    appearance = 'default'; await act(() => host.rerender());
    const defaultFace = nodes(tree).find(node => flatten(node.props.style).backgroundColor === theme.colors.raised);
    assert.equal(flatten(defaultFace.props.style).height, 44); assert.equal(flatten(defaultFace.props.style).borderRadius, 8);
    assert.equal(flatten(defaultFace.props.style).paddingHorizontal, 12);
    const defaultInput = nodes(tree).find(node => node.type === 'TextInput');
    assert.equal(flatten(defaultInput.props.style).fontSize, 15); assert.equal(flatten(defaultInput.props.style).fontFamily, 'regular');
  });
}
