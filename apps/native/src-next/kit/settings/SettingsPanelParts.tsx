import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {shellType} from '../shell/visual';
import {useTheme} from '../theme';

/** Privacy's quiet note that the owner sees what is played (tv-more 30 on a TV). */
export function SettingsPrivacyNote({children}: {children: string}) {
  const theme = useTheme();
  return <View style={theme.isTV ? {paddingLeft: 24} : {paddingHorizontal: 14, paddingVertical: 12}}>
    <Text testID="settings-panel-privacy-note" style={[shellType(theme.isTV ? 22 : 13, 400, theme.isTV ? 31.9 : 'normal'), {color: theme.colors.textTertiary}]}>{children}</Text>
  </View>;
}

/** What this profile is playing elsewhere: its rows, then a quiet line saying what the controls do. */
export function SettingsRemoteControls({children, lede}: {children: ReactNode; lede: string}) {
  const theme = useTheme();
  return <View testID="settings-panel-remote-controls">
    {children}
    <Text style={[shellType(12.5, 400, 'normal'), {color: theme.colors.textTertiary, paddingHorizontal: 14, paddingVertical: 10}]}>{lede}</Text>
  </View>;
}
