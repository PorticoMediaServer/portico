// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {porticoPublicLinks} from '../../../packages/client-core/src/public-links';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

// This exercises the real phone presenter and its callbacks. AccountEntry invitation
// ownership/dispatch coverage is a separate required vector, not inferred here.
test('phone account presenter exposes only its offered password and provider capabilities', async () => {
  const previousRequire = Object.getOwnPropertyDescriptor(globalThis, 'require');
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {
    assert.equal(path, '../../../assets/wordmark.png'); return 'existing-wordmark';
  }});
  let host: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    const links: string[] = []; let linkFailure = false;
    const legal = await screenModule(new URL('../src-next/screens/accounts/LegalAgreement.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {Text: 'Text'},
      '../../../../../packages/i18n/src': {defaultI18n},
      '../../../../../packages/client-core/src/public-links': {porticoPublicLinks},
    });
    const {PhoneAccountSignIn} = await screenModule(new URL('../src-next/screens/accounts/PhoneAccountSignIn.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, 
      './LegalAgreement': legal, react: {...React, default: React},
      'react-native': {Image: 'Image', Keyboard: {isVisible: () => false, addListener: () => ({remove() {}})}, KeyboardAvoidingView: 'KeyboardAvoidingView', Linking: {openURL: async (url: string) => {links.push(url); if (linkFailure) throw new Error('browser unavailable');}}, Platform: {OS: 'ios'}, ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
      'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
      '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {ColourBackdrop: 'Backdrop'},
      '../../kit/controls': {Button: 'Button', TextField: 'Field'}, '../../kit/theme': {nativeType, useTheme: () => createTheme({isTV: false, width: 390})},
    });
    let tree: any, passwordEnabled = false, registration = true, recovery = true;
    const providers = [{id: 'google', enabled: true, native: false, nativeClientIds: []}], calls: string[] = [];
    host = createFabricHookHost(() => {tree = PhoneAccountSignIn({busy: false, providers, passwordEnabled, registration, recovery,
      username: 'account', password: 'password', onUsernameChange: () => {}, onPasswordChange: () => {},
      onContinue: () => calls.push('password'), onProvider: (id: string) => calls.push(id),
      onForgot: () => calls.push('forgot'), onCreate: () => calls.push('create'), onLocal: () => calls.push('local'), onOpenLink: (url: string) => {links.push(url);}});});
    await act(() => host!.mount());
    const agreement = nodes(tree).find(node => node.type === legal.LegalAgreement);
    const footer = agreement.type(agreement.props);
    const anchors = nodes(footer).filter(node => node.props.accessibilityRole === 'link');
    await act(() => anchors[0].props.onPress());
    linkFailure = true; await act(() => anchors[1].props.onPress());
    assert.deepEqual(links, [porticoPublicLinks.terms, porticoPublicLinks.privacy], 'phone legal links preserve their non-blocking failure behaviour');
    const byID = (id: string) => nodes(tree).find(node => node.props?.testID === id);
    assert.ok(byID('accounts-auth-google')); assert.equal(byID('accounts-auth-continue'), undefined);
    assert.equal(byID('accounts-auth-create'), undefined); assert.equal(byID('accounts-auth-forgot'), undefined);
    await act(() => byID('accounts-auth-google').props.onPress()); assert.deepEqual(calls, ['google']);
    passwordEnabled = true; await act(() => host!.rerender());
    assert.ok(byID('accounts-auth-continue')); assert.ok(byID('accounts-auth-create')); assert.ok(byID('accounts-auth-forgot'));
    await act(() => byID('accounts-auth-continue').props.onPress()); assert.deepEqual(calls, ['google', 'password']);
    registration = false; recovery = false; await act(() => host!.rerender());
    assert.equal(byID('accounts-auth-create'), undefined); assert.equal(byID('accounts-auth-forgot'), undefined);
  } finally {
    await host?.unmount();
    if (previousRequire) Object.defineProperty(globalThis, 'require', previousRequire); else Reflect.deleteProperty(globalThis, 'require');
  }
});
