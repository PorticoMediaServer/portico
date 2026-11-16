import React from 'react';
import {Text, View} from 'react-native';
import {rowSpec} from '@portico/design';
import {Focusable, type FocusableProps} from './Focusable';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';

type TVAccountRowProps = Pick<FocusableProps, 'focusId' | 'preferredFocus' | 'disabled' | 'accessibilityLabel' | 'accessibilityState' | 'onPress'> & {
  title: string; detail: string; icon: IconName; badge?: string; children?: React.ReactNode;
};

/** connect24's server/certificate row: the icon inset belongs to the row's own focus face. */
export function TVAccountRow({title, detail, icon, badge, children, onPress, ...props}: TVAccountRowProps) {
  const theme = useTheme();
  const style = {flex: 1, minHeight: rowSpec.minHeight.withSubtitle.tv, paddingHorizontal: rowSpec.paddingX.tv,
    paddingVertical: rowSpec.paddingY.tv, flexDirection: 'row' as const, alignItems: 'center' as const, gap: rowSpec.leadingGap.tv};
  const content = <>
    <Icon name={icon} size={rowSpec.leadingIcon.tv} color={theme.colors.accentStrong}/>
    <View style={{flex: 1, minWidth: 0, gap: 6}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{detail}</Text>
    </View>
    {badge && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{badge}</Text>}
    {onPress && !props.disabled && <Icon name="i-right" size={26} color={theme.colors.textTertiary}/>}
  </>;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: rowSpec.leadingGap.tv,
    backgroundColor: theme.colors.raised, borderRadius: rowSpec.group.radius.tv,
    borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    {onPress ? <Focusable {...props} accessibilityRole="button" onPress={onPress} focusKind="row" focusRadius={rowSpec.group.radius.tv} style={style}>{content}</Focusable>
      : <View style={style}>{content}</View>}
    {children && <View style={{flexDirection: 'row', alignItems: 'center', gap: rowSpec.leadingGap.tv, paddingRight: rowSpec.paddingX.tv}}>{children}</View>}
  </View>;
}
