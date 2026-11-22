import React from 'react';
import {Text, View} from 'react-native';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

/** One row of a profile's actions: its label, an optional line under it, and a chevron. */
export function ProfileActionRow({label, description, onPress, testID}: {label: string; description?: string; onPress: () => void; testID?: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} accessibilityHint={description} onPress={onPress} focusKind="row" focusRadius={0}
    style={{minHeight: 48, paddingHorizontal: 14, flexDirection: 'row', alignItems: 'center', gap: 12, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
    <View style={{flex: 1}}><Text style={[theme.type.body, {color: theme.colors.text}]}>{label}</Text>{description ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{description}</Text> : null}</View>
    <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
  </Focusable>;
}
