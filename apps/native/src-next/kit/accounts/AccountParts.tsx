import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {LegalAgreement} from '../../screens/accounts/LegalAgreement';
import {useTheme} from '../theme';

export function AccountFields({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{alignSelf: 'stretch', gap: theme.space.controlGap}}>{children}</View>;
}

export function AccountLinks({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{alignSelf: 'stretch', flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center',
    alignItems: 'center', gap: theme.space.controlGap}}>{children}</View>;
}

export function AccountDivider({children}: {children: ReactNode}) {
  const theme = useTheme();
  const rule = {flex: 1, height: theme.space.hairline, backgroundColor: theme.colors.line};
  return <View style={{alignSelf: 'stretch', flexDirection: 'row', gap: theme.space.controlGap, alignItems: 'center'}}>
    <View style={rule}/>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{children}</Text>
    <View style={rule}/>
  </View>;
}

export function AccountHint({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{children}</Text>;
}

export function AccountTerms({open}: {open: (url: string) => void}) {
  const theme = useTheme();
  return <LegalAgreement style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}
    linkStyle={{color: theme.colors.accentStrong}} onOpenLink={open}/>;
}

export const profileFaceSize = {pin: 92};
