import React from 'react';
import {Pressable, Text, type StyleProp, type ViewStyle} from 'react-native';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

export type SeeAllLinkProps = Readonly<{label: string; onPress: () => void; accessibilityHint?: string; testID?: string;
  onFocus?: () => void; onBlur?: () => void; style?: StyleProp<ViewStyle>}>;

/** A section's "See all ›" at the end of its heading (phone-detail `.dh .a`, desktop-browse rows): the `seeAll` role in the
 * tertiary colour with its chevron, flush with the heading's end (no padding; the hit area is a slop). */
export function SeeAllLink({label, onPress, accessibilityHint, testID, onFocus, onBlur, style}: SeeAllLinkProps) {
  const theme = useTheme();
  return <Pressable testID={testID} accessibilityRole="button" accessibilityLabel={label} accessibilityHint={accessibilityHint} onPress={onPress} onFocus={onFocus} onBlur={onBlur} hitSlop={10}
    style={[{marginLeft: 'auto', minHeight: theme.scale.icon.tertiary.size, flexDirection: 'row', alignItems: 'center', gap: theme.space.hairline * 2}, style]}>
    <Text style={[theme.type.seeAll, {color: theme.colors.textTertiary, includeFontPadding: false}]}>{label}</Text>
    <Icon name="i-right" size={theme.scale.icon.tertiary.size} color={theme.colors.textTertiary}/>
  </Pressable>;
}
