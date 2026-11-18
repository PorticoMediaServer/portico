import React from 'react';
import {Text, View} from 'react-native';
import {Chip} from '../controls/Chip';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';

/** The guide's quiet pickers (phone-guide g1, final3-phone-07): a glyph, a strong label, its value and a chevron, drawn as text,
 * not as a button: "Today  Wed, Oct 21 ⌄" and "By number ⌄". The control is the whole line. */
export function GuidePicker({icon, label, value, onPress, testID}: Readonly<{icon?: IconName; label: string; value?: string; onPress: () => void; testID?: string}>) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={value ? `${label}, ${value}` : label} onPress={onPress} focusRadius={8}
    style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, minHeight: theme.targets.touch, flexShrink: 0}}>
    {icon && <Icon name={icon} size={18} color={theme.colors.textTertiary}/>}
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{label}</Text>
    {value !== undefined && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{value}</Text>}
    <Icon name="i-chdown" size={16} color={theme.colors.textTertiary}/>
  </Focusable>;
}

export type GuideGroup = Readonly<{id: string; label: string; count?: number; icon?: IconName}>;
/** The channel groups as chips: All, Favorites, News … A phone draws their names only (phone-guide g1); from tablet width each
 * says its count, Favorites with its star (tablet-play 6, desktop-live live-guide). */
export function GuideGroups({groups, value, onValueChange}: Readonly<{groups: readonly GuideGroup[]; value: string; onValueChange: (id: string) => void}>) {
  const phone = useTheme().form === 'phone';
  return <>{groups.map(group => <Chip key={group.id} selected={group.id === value} count={phone ? undefined : group.count} icon={phone ? undefined : group.icon} onPress={() => onValueChange(group.id)}>{group.label}</Chip>)}</>;
}
