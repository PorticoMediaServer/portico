import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FocusProvider} from '../../kit/focus';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';

/** "Remember this profile on this device" at ten feet (auth.html frame 17): a focusable checkbox with the focus ring. Shown only where the server allows remembering. */
export function TVRememberRow({checked, onChange, busy, focusId = 'tv.auth.profile.remember', marginTop = 40}: {
  checked: boolean; onChange: (checked: boolean) => void; busy: boolean; focusId?: string; marginTop?: number;
}) {
  const theme = useTheme(), label = defaultI18n.t('pin.rememberOnDevice');
  return <Focusable focusId={focusId} accessibilityRole="checkbox" accessibilityState={{checked, disabled: busy}} accessibilityLabel={label}
    disabled={busy} onPress={() => onChange(!checked)} focusRadius={18}
    style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop, paddingVertical: 14, paddingHorizontal: 26, borderRadius: 18}}>
    <View style={{width: 40, height: 40, borderRadius: 11, borderWidth: 3, alignItems: 'center', justifyContent: 'center',
      borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: checked ? theme.colors.accentStrong : 'transparent'}}>
      {checked && <Icon name="i-check" size={26} color={theme.colors.ink}/>}
    </View>
    <Text style={[nativeType({size: 28, weight: 600, lineHeight: 'normal', tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text>
  </Focusable>;
}

/** The same row under the faces of the TV Who's watching, which has no pad of its own (frame 16 doesn't draw it; it takes frame 17's control). */
export function TVPickerRememberRow(props: {checked: boolean; onChange: (checked: boolean) => void; busy: boolean}) {
  return <FocusProvider><TVRememberRow {...props} focusId="tv.auth.picker.remember" marginTop={56}/></FocusProvider>;
}
