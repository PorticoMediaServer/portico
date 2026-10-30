import {headerWordmark} from '../kit/shell/brand';
import React from 'react';
import {Image, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src';
import {ProfileAvatar} from '../kit/shell';
import {Focusable} from '../kit/controls';
import {Icon} from '../kit/Icon';
import type {ProfileOption} from '../kit/artwork';
import {useTheme} from '../kit/theme';
import {BlurView} from '../platform/native-blur';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {phoneHeaderGeometry} from './model';
import type {NativeShellSnapshot} from './runtime';

export function PhoneHomeHeader({profile, unread, avatarRef, onProfile, viewerRole, serverAction, castControl, scrolled = false}: {
  profile: ProfileOption; unread: boolean; avatarRef: React.RefObject<View | null>; onProfile: () => void; castControl?: React.ReactNode; scrolled?: boolean;
  viewerRole: NativeShellSnapshot['account']['viewerRole']; serverAction?: NativeShellSnapshot['account']['serverAction'];
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const geometry = phoneHeaderGeometry(insets.top, scrolled);
  return <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, paddingTop: geometry.top, paddingHorizontal: 20, height: geometry.height, zIndex: theme.layers.navigation, flexDirection: 'row', alignItems: 'center', backgroundColor: scrolled ? `${theme.colors.ink}D1` : undefined, borderBottomWidth: scrolled ? 1 : 0, borderBottomColor: `${theme.colors.text}12`}}>
    {!scrolled && <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id="native-home-header" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.75}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#native-home-header)"/></Svg>}
    {scrolled && <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <Image accessible accessibilityLabel={defaultI18n.t('next.platform.appName')} source={require('../../assets/wordmark.png')} resizeMode="contain" style={{...headerWordmark.phone, flexShrink: 0}}/>
    <View style={{marginLeft: 'auto', flexDirection: 'row', gap: 16, alignItems: 'center'}}>{castControl}
      {viewerRole === 'owner' && serverAction && <Focusable accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.shell.activity')} onPress={serverAction.onPress} focusRadius={20}
        style={{width: 40, height: 40, borderRadius: 20}}>
        {({hovered}) => <View style={{width: 40, height: 40, borderRadius: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: hovered ? `${theme.colors.text}1A` : 'transparent'}}><Icon name="i-activity" size={22} color={hovered ? theme.colors.text : theme.colors.textSecondary}/></View>}
      </Focusable>}
      <ProfileAvatar ref={avatarRef} profile={profile} size={30} unread={unread} accessibilityLabel={defaultI18n.t('next.shell.settings')} onPress={onProfile}/></View>
  </View>;
}
