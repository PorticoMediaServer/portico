import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {passwordStrength} from '../../../../../packages/client-core/src/password-strength';
import {useTheme} from '../../kit/theme';

/** Advisory only. The shared password policy, not this meter, decides validity. */
export function PasswordStrengthMeter({password}: {password: string}) {
  const theme = useTheme(), t = defaultI18n.t, strength = passwordStrength(password);
  if (!password) return null;
  const label = t(strength.label === 'Too short' ? 'services.password.tooShort' : strength.label === 'Too long' ? 'accounts.password.tooLong'
    : strength.label === 'Weak' ? 'accounts.password.weak' : strength.label === 'Fair' ? 'accounts.password.fair'
      : strength.label === 'Good' ? 'accounts.password.good' : 'services.password.strong');
  const color = strength.level >= 3 ? theme.colors.healthy : strength.level === 2 ? theme.colors.amber : theme.colors.danger;
  return <View testID="accounts-password-strength" accessible accessibilityLabel={label} accessibilityLiveRegion="polite"
    style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 6}}>
    <View accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{flexDirection: 'row', gap: 6}}>
      {[1, 2, 3, 4].map(level => <View key={level} style={{width: 40, height: 4, borderRadius: 2, backgroundColor: level <= strength.level ? color : theme.colors.lineControl}}/>)}
    </View>
    <Text style={[theme.type.captionStrong, {fontSize: 13 * theme.textScale, color, marginLeft: 6}]}>{label}</Text>
  </View>;
}
