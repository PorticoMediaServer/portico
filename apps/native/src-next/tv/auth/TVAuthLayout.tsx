import React from 'react';
import {Image, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop, ProfileFace, type ProfileOption} from '../../kit/artwork';
import {nativeType, useTheme} from '../../kit/theme';
import {shellLayout} from '../../shell/model';

/** Auth8/9's column and colour-only backdrop; composition is authorized by ruling28. */
export function TVAuthLayout({heading, eyebrow, body, children, portico, aside, testID}: {
  heading: string; eyebrow?: string; body: React.ReactNode; children: React.ReactNode;
  portico: boolean; aside?: React.ReactNode; testID: string;
}) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ColourBackdrop shape="side" fill/>
      <View style={{position: 'absolute', left: 120, top: 0, bottom: 0, width: 900, justifyContent: 'center'}}>
        <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')}
          resizeMode="contain" style={{position: 'absolute', left: 0, top: 72, width: 174, height: 40}}/>
        {eyebrow && <Text style={[nativeType({size: 22, weight: 700, tracking: .1, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginBottom: 14}]}>{eyebrow}</Text>}
        <Text accessibilityRole="header" style={[nativeType({size: 72, weight: 800, tracking: -.045, lineHeight: 72}, theme.textScale), {color: theme.colors.text, marginBottom: 22}]}>{heading}</Text>
        <Text style={[nativeType({size: 26, weight: 400, tracking: 0, lineHeight: 37.7}, theme.textScale), {color: theme.colors.textSecondary, maxWidth: 760, marginBottom: 48}]}>{body}</Text>
        {children}
        {portico && <View style={{position: 'absolute', left: 0, bottom: 64}}>
          <Text style={[nativeType({size: 20, weight: 400, tracking: 0, lineHeight: 30}, theme.textScale), {color: theme.colors.textTertiary}]}>{defaultI18n.t('activation.termsNote').split('\n')[0]}</Text>
          <View style={{flexDirection: 'row', gap: 28}}>{(['accounts.legal.termsAddress', 'accounts.legal.privacyAddress'] as const).map(key => <Text key={key}
            style={[nativeType({size: 20, weight: 400, tracking: 0, lineHeight: 30}, theme.textScale), {color: theme.colors.textTertiary}]}>{defaultI18n.t(key)}</Text>)}</View>
        </View>}
      </View>
      {aside}
    </View>
  </View>;
}

/** Auth17's profile identity column; the account owner supplies every visible profile fact. */
export function TVProfilePINLayout({profile, error, children, pad, hints, onBack}: {
  profile: ProfileOption; error?: React.ReactNode; children: React.ReactNode; pad: React.ReactNode; hints?: React.ReactNode; onBack: () => void;
}) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  return <View testID="tv-profile-pin" onAccessibilityEscape={onBack} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ColourBackdrop shape="round" fill/>
      <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')}
        resizeMode="contain" style={{position: 'absolute', left: 120, top: 72, width: 174, height: 40}}/>
      <View style={{position: 'absolute', left: 240, top: 0, bottom: 0, width: 640, alignItems: 'center', justifyContent: 'center'}}>
        <ProfileFace {...profile} size={196} scaleOnFocus={false}/>
        <Text accessibilityRole="header" style={[nativeType({size: 52, weight: 800, tracking: -.04, lineHeight: 'normal'}, theme.textScale),
          {color: theme.colors.text, marginTop: 30, marginBottom: 6}]}>{defaultI18n.t('auth.enterProfilePin')}</Text>
        <Text style={[nativeType({size: 28, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, marginBottom: 40}]}>{profile.name}</Text>
        {error && <View accessibilityRole="alert" style={{marginBottom: 16}}>{error}</View>}
        {children}
      </View>
      {pad}
      {hints && <View style={{position: 'absolute', left: 0, right: 0, bottom: 72, flexDirection: 'row', justifyContent: 'center', gap: 44}}>{hints}</View>}
    </View>
  </View>;
}
