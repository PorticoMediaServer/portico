import React from 'react';
import {Text, View} from 'react-native';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';
import {defaultI18n} from '../../../../../packages/i18n/src';

/**
 * "Remember this profile on this device" (auth.html frames 13–15): a checkbox, unticked until
 * chosen, under the faces on Who's watching and under the boxes on a profile's PIN. Callers show
 * it only where the server allows remembering (its "Remember PIN for" isn't Off).
 */
export function RememberProfileRow({checked, onChange, disabled = false, focusId, marginTop = 10}: {
  checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean; focusId: string; marginTop?: number;
}) {
  const theme = useTheme(), label = defaultI18n.t('pin.rememberOnDevice');
  return <Focusable focusId={focusId} accessibilityRole="checkbox" accessibilityState={{checked, disabled}} accessibilityLabel={label}
    disabled={disabled} onPress={() => onChange(!checked)} focusRadius={6} focusTreatment="none"
    style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', alignSelf: 'center', gap: 12, paddingVertical: 8, marginTop}}>
    <View style={{width: 24, height: 24, borderRadius: 7, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
      borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: checked ? theme.colors.accentStrong : 'transparent'}}>
      {checked && <Icon name="i-check" size={16} color={theme.colors.ink}/>}
    </View>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{label}</Text>
  </Focusable>;
}
