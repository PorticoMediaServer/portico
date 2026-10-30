import React, {forwardRef, useState} from 'react';
import {View} from 'react-native';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import type {MenuRowProps} from '../overlays/types';
import {useTheme} from '../theme';
import {SelectionText} from './surface';

/** select.html's phone srow; focus and press state come from the actual control. */
export const SelectionActionRow = forwardRef<View, MenuRowProps>(function SelectionActionRow({label, icon, description, destructive, tone, style, onFocus, onBlur, onHoverIn, onHoverOut, ...props}, ref) {
  const {colors, platform, textScale} = useTheme();
  const phone = platform === 'phone';
  const [focused, setFocused] = useState(false), [hovered, setHovered] = useState(false);
  return <Focusable {...props} ref={ref} accessibilityRole="button" accessibilityLabel={props.accessibilityLabel ?? (typeof label === 'string' ? label : undefined)}
    onFocus={event => {setFocused(true); onFocus?.(event);}} onBlur={event => {setFocused(false); onBlur?.(event);}} onHoverIn={event => {setHovered(true); onHoverIn?.(event);}} onHoverOut={event => {setHovered(false); onHoverOut?.(event);}}
    focusKind="row" focusRadius={0} style={state => [{minHeight: 56, paddingVertical: 8, paddingHorizontal: 20, flexDirection: 'row', alignItems: 'center', gap: 14,
      opacity: props.disabled ? .5 : 1, backgroundColor: focused || hovered || state.pressed ? 'rgba(237,245,250,.06)' : 'transparent'}, typeof style === 'function' ? style(state) : style]}>
    {icon ? <Icon name={icon} size={24} color={destructive ? colors.record : tone === 'owner' ? colors.accentStrong : colors.textSecondary} /> : null}
    <View style={{flex: 1, minWidth: 0}}><SelectionText size={16} weight={600} style={{lineHeight: phone ? undefined : 24, ...(phone ? {includeFontPadding: false} : {}), color: destructive ? colors.record : colors.text}}>{label}</SelectionText>
      {description != null ? <SelectionText size={12.5} weight={500} secondary style={{marginTop: 2, lineHeight: phone ? 16.875 * textScale : 16.875, ...(phone ? {includeFontPadding: false} : {})}}>{description}</SelectionText> : null}
    </View>
  </Focusable>;
});
