import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {rowSpec, tvAccountSpec} from '@portico/design';
import {Focusable, type FocusableProps} from './Focusable';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';

type TVAccountRowProps = Pick<FocusableProps, 'focusId' | 'preferredFocus' | 'disabled' | 'accessibilityLabel' | 'accessibilityState' | 'onPress'> & {
  title: string; detail: string; icon: IconName; badge?: string; children?: React.ReactNode;
  /** The server this device is on: its tile takes the brand gradient, as connect24 draws it. */
  active?: boolean;
};

/** connect24's server/certificate row: the icon inset belongs to the row's own focus face. */
export function TVAccountRow({title, detail, icon, badge, children, onPress, active = false, ...props}: TVAccountRowProps) {
  const theme = useTheme(), tile = tvAccountSpec.rowIcon;
  const style = {flex: 1, minHeight: rowSpec.minHeight.withSubtitle.tv, paddingHorizontal: rowSpec.paddingX.tv,
    paddingVertical: rowSpec.paddingY.tv, flexDirection: 'row' as const, alignItems: 'center' as const, gap: rowSpec.leadingGap.tv};
  const content = <>
    <View style={{width: tile.box, height: tile.box, borderRadius: tile.radius, overflow: 'hidden', alignItems: 'center', justifyContent: 'center',
      backgroundColor: theme.colors.raisedBright}}>
      {active && <Svg pointerEvents="none" style={StyleSheet.absoluteFill} width="100%" height="100%"><Defs><LinearGradient id="tv-account-row-tile" x1="0" y1="0" x2="1" y2="1">
        <Stop offset="0" stopColor={theme.colors.accentDeep}/><Stop offset="1" stopColor={theme.colors.generatedGreen}/></LinearGradient></Defs>
        <Rect width="100%" height="100%" fill="url(#tv-account-row-tile)"/></Svg>}
      <Icon name={icon} size={tile.glyph} color={active ? theme.colors.text : theme.colors.textSecondary}/>
    </View>
    <View style={{flex: 1, minWidth: 0, gap: 6}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type[rowSpec.subtitle.role], {color: theme.colors[rowSpec.subtitle.color]}]}>{detail}</Text>
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
