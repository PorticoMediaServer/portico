import React from 'react';
import {Text, View} from 'react-native';
import {Icon} from '../Icon';
import {Focusable} from '../controls';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

export function IntegrationValue({value}: {value: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text selectable style={[theme.type.rowHelp, {
    color: theme.colors.text, backgroundColor: theme.colors.ink, borderRadius: theme.baseRadii.control,
    padding: frame.cardPadding, flexShrink: 1,
  }]}>{value}</Text>;
}

export function SettingsCheckRow({label, description, checked, onPress, disabled}: {
  label: string; description?: string; checked: boolean; onPress: () => void; disabled?: boolean;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable accessibilityRole="checkbox" accessibilityLabel={label} accessibilityHint={description}
    accessibilityState={{checked, disabled}} disabled={disabled} onPress={onPress}
    style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, padding: frame.cardPadding, minHeight: frame.rowTwoLine}}>
    <View accessible={false} style={{width: theme.space.lg, height: theme.space.lg, borderRadius: theme.baseRadii.control,
      alignItems: 'center', justifyContent: 'center', borderWidth: 1, borderColor: theme.colors.lineStrong,
      backgroundColor: checked ? theme.colors.accentStrong : theme.colors.raised}}>
      {checked && <Icon name="i-check" size={theme.space.md} color={theme.colors.ink}/>}
    </View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
      {description && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{description}</Text>}
    </View>
  </Focusable>;
}

export function IntegrationNotice({text, error = false}: {text: string; error?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Text accessibilityRole={error ? 'alert' : undefined} accessibilityLiveRegion={error ? 'assertive' : 'polite'}
    style={[theme.type.rowHelp, {color: error ? theme.colors.record : theme.colors.accentStrong, marginHorizontal: frame.cardInset}]}>{text}</Text>;
}
