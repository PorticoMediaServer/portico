import React, {forwardRef} from 'react';
import {StyleSheet, Text, View, type StyleProp, type TextStyle} from 'react-native';
import {chipSpec} from '@portico/design';
import {Icon, type IconName} from '../Icon';
import {fontFamilies, useTheme} from '../theme';
import {Focusable, type FocusableProps} from './Focusable';
import {chipGeometry, colorAlpha, minimumHitSlop, type ChipVariant} from './control-model';
import {useControlValue} from './useControlValue';

export type ChipProps = Omit<FocusableProps, 'children'> & {
  children?: React.ReactNode;
  variant?: ChipVariant;
  selected?: boolean;
  defaultSelected?: boolean;
  onSelectedChange?: (selected: boolean) => void;
  count?: React.ReactNode;
  countStyle?: 'text' | 'badge';
  dim?: boolean;
  retry?: boolean;
  prefix?: React.ReactNode;
  icon?: IconName;
  trailingIcon?: IconName;
  iconSize?: number;
  textStyle?: StyleProp<TextStyle>;
};

export const Chip = forwardRef<View, ChipProps>(function Chip({
  children, variant = 'default', selected, defaultSelected, onSelectedChange, count, countStyle = 'text', dim = false, retry = false, prefix,
  icon, trailingIcon, iconSize, disabled, accessibilityState, style, textStyle, hitSlop, onPress, ...props
}, ref) {
  const theme = useTheme();
  const geometry = chipGeometry(variant, theme.platform);
  const isFacet = variant === 'facet' || variant === 'pill' || variant === 'quick';
  const applied = variant === 'applied';
  const [checked, setChecked] = useControlValue(selected, defaultSelected ?? false, onSelectedChange);
  const checkbox = props.accessibilityRole === 'checkbox' || variant === 'facet';
  const toggle = checkbox || selected !== undefined || defaultSelected !== undefined || onSelectedChange !== undefined;
  const slop = minimumHitSlop(geometry.height, theme.isTV);
  const desktop = theme.platform === 'web', desktopChoice = desktop && (variant === 'facet' || variant === 'quick');
  const border = dim ? theme.colors.line : desktopChoice ? colorAlpha(checked ? theme.colors.accentStrong : theme.colors.text, checked ? .62 : .16) : checked ? colorAlpha(theme.isTV ? theme.colors.accentStrong : theme.colors.accent, theme.isTV ? .7 : 1) : isFacet || retry ? theme.colors.lineStrong : 'transparent';
  const color = dim ? theme.colors.textTertiary : checked || !theme.isTV && !isFacet ? theme.colors.text : theme.colors.textSecondary;
  const labelWeight = !theme.isTV && (variant === 'facet' || variant === 'pill' || theme.platform === 'web' && variant === 'quick') ? 600 : 700;
  const glyphSize = iconSize ?? chipSpec.icon[theme.platform];
  return <Focusable {...props} ref={ref} disabled={disabled} focusKind="chip" focusRadius={geometry.radius}
    accessibilityRole={checkbox ? 'checkbox' : props.accessibilityRole ?? 'button'} accessibilityState={{...accessibilityState, ...(checkbox ? {checked} : toggle ? {selected: checked} : {})}}
    onPress={event => {if (!disabled) {onPress?.(event); if (toggle && !event.defaultPrevented) setChecked(!checked);}}}
    hitSlop={hitSlop ?? {top: slop, bottom: slop, left: slop, right: slop}}
    style={state => [styles.chip, {height: geometry.height, borderRadius: geometry.radius, paddingHorizontal: geometry.paddingX, paddingRight: geometry.paddingRight,
      gap: geometry.gap, borderWidth: theme.isTV ? 1.5 : 1, borderColor: border,
      opacity: disabled ? .45 : dim ? .5 : 1,
      backgroundColor: dim ? 'transparent' : desktop && variant === 'pill' ? colorAlpha(theme.colors.text, .1) : desktop && applied ? theme.colors.raisedBright : checked ? colorAlpha(theme.colors.accent, theme.isTV ? .24 : desktopChoice ? variant === 'facet' ? .2 : .18 : .28) : isFacet ? 'transparent' : theme.isTV && !applied ? colorAlpha(theme.colors.text, .08) : theme.colors.raisedHigh},
      typeof style === 'function' ? style(state) : style]}>
    {state => <>
    {state.hovered && !disabled && !dim && !checked ? <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: geometry.radius, backgroundColor: desktop && variant === 'pill' ? colorAlpha(theme.colors.text, .1) : isFacet ? colorAlpha(theme.colors.text, desktop && variant === 'facet' ? .06 : .07) : theme.colors.raisedBright}]} /> : null}
    {icon ? <Icon name={icon} size={glyphSize} color={checked ? theme.colors.accentStrong : color} /> : null}
    {prefix !== undefined ? <Text numberOfLines={1} style={[theme.type.chip, {color: theme.colors.textTertiary, fontFamily: fontFamilies[600], fontSize: geometry.fontSize * theme.textScale}]}>{prefix}</Text> : null}
    <Text numberOfLines={1} ellipsizeMode="tail" style={[theme.type.chip, styles.label, {color, fontFamily: fontFamilies[labelWeight], fontSize: geometry.fontSize * theme.textScale}, textStyle]}>{children}</Text>
    {count !== undefined && count !== null ? <Text numberOfLines={1} style={[theme.type.captionStrong, {color: theme.isTV ? color : checked ? theme.colors.textSecondary : theme.colors.textTertiary, opacity: theme.isTV && countStyle === 'text' ? .7 : 1, fontSize: (theme.isTV ? geometry.fontSize : theme.platform === 'web' ? 13 : 12) * theme.textScale, fontFamily: fontFamilies[theme.isTV || theme.platform === 'web' ? 600 : 700]},
      countStyle === 'badge' ? {color: theme.colors.actionInk, backgroundColor: theme.colors.accentStrong, minWidth: theme.isTV ? 30 : 21, height: theme.isTV ? 30 : 21, borderRadius: theme.isTV ? 15 : 11, paddingHorizontal: 6, textAlign: 'center', textAlignVertical: 'center', fontFamily: fontFamilies[800]} : undefined]}>{count}</Text> : null}
    {retry ? <Icon name="i-retry" size={theme.isTV ? 24 : 16} color={theme.colors.accentStrong} /> : null}
    {trailingIcon ? <Icon name={trailingIcon} size={glyphSize} color={theme.colors.textTertiary} /> : null}
    </>}
  </Focusable>;
});

const styles = StyleSheet.create({chip: {flexDirection: 'row', alignItems: 'center', alignSelf: 'flex-start', flexShrink: 1}, label: {flexShrink: 1}});
