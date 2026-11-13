import React from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';
import {Focusable} from './Focusable';

const rows = [['1', '2', '3'], ['4', '5', '6'], ['7', '8', '9'], ['', '0', 'delete']] as const;

/** The phone's own number pad for a four-digit code (design/ui-v1 final3-auth-05, frames 14 and 15):
 * a band across the bottom of the screen. While a wrong-PIN wait or a check runs it is drawn dimmed and
 * takes no presses. A hardware keyboard still types into the screen's own field. */
export function NumberPad({onDigit, onDelete, disabled = false, testID = 'number-pad'}: {
  onDigit: (digit: string) => void; onDelete: () => void; disabled?: boolean; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <View testID={testID} style={{backgroundColor: theme.colors.raised, paddingHorizontal: 6, paddingTop: 8, paddingBottom: Math.max(insets.bottom, 8), gap: 6}}>
    {rows.map(row => <View key={row.join()} style={{flexDirection: 'row', gap: 6}}>
      {row.map((key, index) => key === ''
        ? <View key={`blank-${index}`} style={{flex: 1}}/>
        : <Focusable key={key} testID={`${testID}-${key}`} accessibilityRole="button" disabled={disabled}
          accessibilityLabel={key === 'delete' ? defaultI18n.t('action.delete') : key}
          onPress={() => {if (!disabled) {if (key === 'delete') onDelete(); else onDigit(key);}}}
          style={state => ({flex: 1, minHeight: 52, borderRadius: 8, alignItems: 'center', justifyContent: 'center', opacity: disabled ? 0.45 : 1,
            backgroundColor: key === 'delete' ? 'transparent' : state.pressed ? theme.colors.lineStrong : theme.colors.raisedHigh})}>
          {key === 'delete' ? <Icon name="i-backspace" size={26} color={theme.colors.text}/>
            : <Text style={[nativeType({size: 26, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{key}</Text>}
        </Focusable>)}
    </View>)}
  </View>;
}
