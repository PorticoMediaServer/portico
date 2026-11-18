import React from 'react';
import {Text, View} from 'react-native';
import {useTheme} from '../theme';
import {alpha} from '../shell/visual';

/** TV: what the remote's keys do here, as a row of key caps each followed by its action ("OK  Play from 27:45").
 * Words only; the page handles the keys. */
export function KeyHints({hints}: {hints: readonly Readonly<{key: string; label: string}>[]}) {
  const theme = useTheme(), cap = theme.targets.pointer;
  return <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', alignItems: 'center', columnGap: theme.space.xl}}>
    {hints.map(hint => <View key={hint.key} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
      <View style={{height: cap, minWidth: cap, paddingHorizontal: theme.space.controlGap, borderRadius: theme.baseRadii.control, borderWidth: theme.space.hairline * 1.5, borderColor: alpha(theme.colors.text, .3), alignItems: 'center', justifyContent: 'center'}}>
        <Text style={[theme.type.badge, {includeFontPadding: false, color: theme.colors.textSecondary}]}>{hint.key}</Text></View>
      <Text style={[theme.type.playerTime, {includeFontPadding: false, color: theme.colors.textSecondary}]}>{hint.label}</Text>
    </View>)}
  </View>;
}
