import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {useTheme} from '../theme';
import {alpha} from '../shell/visual';

/** TV: what the remote's keys do here, as a row of key caps each followed by its action ("OK  Play from 27:45").
 * A hint may name more than one key ("Up  Down  Choose a page"). Words only; the page handles the keys. The row wraps
 * to a second line in a narrow column (tv-more 22) before anything is cut. */
export function KeyHints({hints}: {hints: readonly Readonly<{key: string | readonly string[]; label: string}>[]}) {
  const theme = useTheme(), cap = theme.targets.pointer;
  return <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.xl, rowGap: theme.space.xs}}>
    {hints.map(hint => {
      const keys = typeof hint.key === 'string' ? [hint.key] : hint.key;
      return <View key={keys.join(' ')} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
        {keys.map(key => <View key={key} style={{height: cap, minWidth: cap, paddingHorizontal: theme.space.controlGap, borderRadius: theme.baseRadii.control, borderWidth: theme.space.hairline * 1.5, borderColor: alpha(theme.colors.text, .3), alignItems: 'center', justifyContent: 'center'}}>
          <Text style={[theme.type.badge, {includeFontPadding: false, color: theme.colors.textSecondary}]}>{key}</Text></View>)}
        <Text style={[theme.type.playerTime, {includeFontPadding: false, color: theme.colors.textSecondary}]}>{hint.label}</Text>
      </View>;
    })}
  </View>;
}

/** R11: the hint every TV overlay that the remote's Back closes carries at its foot ("Back  Close"). */
export function BackCloseHints() {
  return <KeyHints hints={[{key: defaultI18n.t('action.back'), label: defaultI18n.t('action.close')}]}/>;
}
