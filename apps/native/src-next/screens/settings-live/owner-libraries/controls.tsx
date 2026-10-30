import React, {type ReactNode} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {Focusable} from '../../../kit/controls/Focusable';
import {Icon, type IconName} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';

/** The library page's white groups: one raised card per meaning, never one flat list. */
export function LibraryGroup({children, style}: {children: ReactNode; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme();
  return <View style={[{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}, style]}>{children}</View>;
}

export function SectionTitle({children, action}: {children: ReactNode; action?: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', marginTop: 22, marginBottom: 8, paddingHorizontal: 4}}>
    <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, flex: 1}]}>{children}</Text>
    {action}
  </View>;
}

export type LibraryRowProps = Readonly<{
  testID?: string;
  label: string;
  help?: string | readonly string[];
  value?: ReactNode;
  icon?: IconName;
  tone?: 'normal' | 'danger';
  onPress?: () => void;
  trailing?: ReactNode;
  first?: boolean;
  minHeight?: number;
  accessibilityLabel?: string;
}>;

/** One line of a group: a label and its value, a chevron when it opens something, or a control at the end. */
export function LibraryRow({testID, label, help, value, icon, tone = 'normal', onPress, trailing, first = false, minHeight = 52, accessibilityLabel}: LibraryRowProps) {
  const theme = useTheme();
  const color = tone === 'danger' ? theme.colors.record : theme.colors.text;
  const body = <>
    {icon && <View style={{width: 30, height: 30, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(tone === 'danger' ? theme.colors.record : theme.colors.text, .1)}}><Icon name={icon} size={17} color={tone === 'danger' ? theme.colors.record : theme.colors.textSecondary}/></View>}
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[shellType(15.5, 500), {color}]}>{label}</Text>
      {help !== undefined && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3, marginTop: 2}}>{(typeof help === 'string' ? [help] : help).map((part, index) => <Text key={index} style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, flexShrink: 1}]}>{part}</Text>)}</View>}
    </View>
    {value !== undefined && (typeof value === 'string' ? <Text numberOfLines={1} style={[shellType(14.5), {color: theme.colors.textSecondary, maxWidth: 170}]}>{value}</Text> : value)}
    {trailing}
    {onPress && !trailing && <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>}
  </>;
  const style: StyleProp<ViewStyle> = [{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 14, paddingVertical: 10, minHeight, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}];
  return onPress ? <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={accessibilityLabel ?? label} accessibilityHint={typeof help === 'string' ? help : help?.join(', ')} onPress={onPress} focusKind="row" focusRadius={10} style={style}>{body}</Focusable> : <View testID={testID} style={style}>{body}</View>;
}
