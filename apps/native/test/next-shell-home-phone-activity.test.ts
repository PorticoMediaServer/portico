import {headerWordmark} from '../src-next/kit/shell/brand';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {defaultI18n} from '../../../packages/i18n/src';
import {nativeAccountMenu} from '../src-next/shell/account';
import {phoneTabs} from '../src-next/shell/model';
import {createTheme} from '../src-next/kit/theme';
import {screenModule} from './helpers/screen-harness.mjs';

const elements = (node: any): React.ReactElement<any>[] => Array.isArray(node) ? node.flatMap(elements) : React.isValidElement<any>(node) ? [node, ...elements(node.props.children)] : [];

test('G14 renders only the owner glyph and invokes the genuine existing Dashboard action', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'require');
  Object.defineProperty(globalThis, 'require', {configurable: true, value: (path: string) => {assert.equal(path, '../../assets/wordmark.png'); return 1;}});
  try {
    const theme = createTheme({isTV: false, width: 390});
    const {PhoneHomeHeader} = await screenModule(new URL('../src-next/shell/PhoneHomeHeader.tsx', import.meta.url), {
      react: {...React, default: React}, 'react-native': {Image: 'Image', View: 'View'},
      'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 24})},
      '../../../../packages/i18n/src': {defaultI18n}, '../kit/shell': {ProfileAvatar: 'ProfileAvatar'},
      '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'},
      '../kit/shell/brand': {headerWordmark}, '../kit/theme': {useTheme: () => theme}, '../platform/native-blur': {BlurView: 'BlurView'},
      'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
      './model': {phoneHeaderGeometry: () => ({top: 54, height: 104})},
    });
    for (const role of ['owner', 'admin', 'member'] as const) {
      const opened: unknown[] = [];
      const account = nativeAccountMenu({account: {role, username: 'verified-user'}} as DirectSnapshot, route => opened.push(route));
      const tree = PhoneHomeHeader({profile: {}, unread: false, avatarRef: {current: null}, onProfile() {}, ...account});
      const glyphs = elements(tree).filter(node => node.type === 'Focusable');
      assert.equal(glyphs.length, role === 'owner' ? 1 : 0, role);
      assert.equal(!!account.serverAction, role !== 'member', 'administrators retain their account-menu Dashboard card');
      if (role === 'owner') {
        assert.equal(glyphs[0].props.onPress, account.serverAction!.onPress);
        const icon = elements(glyphs[0].props.children({hovered: false})).find(node => node.type === 'Icon')!;
        assert.equal(icon.props.name, 'i-activity'); assert.equal(icon.props.size, 22);
        assert.equal(icon.props.color, theme.colors.textSecondary);
        glyphs[0].props.onPress();
        assert.deepEqual(opened, [{kind: 'server-dashboard'}]);
      } else assert.deepEqual(opened, []);
      assert.equal(elements(tree).filter(node => node.type === 'ProfileAvatar').length, 1);
    }
    assert.equal(defaultI18n.t(phoneTabs(true).find(tab => tab.id === 'libraries')!.message), 'Libraries', 'G56 retains the plural phone label');
  } finally {
    if (previous) Object.defineProperty(globalThis, 'require', previous); else Reflect.deleteProperty(globalThis, 'require');
  }
});
