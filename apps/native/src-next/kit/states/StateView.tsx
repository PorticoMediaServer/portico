import React from 'react';
import {Text, View} from 'react-native';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';
import type {StateContentProps} from './types';
import {StateActionButton} from './StateActionButton';

/** A caller that draws a state at authored geometry, such as connect.html 10's 88 circle,
 * 40 glyph and 34 under it. Everything else keeps the component's own sizes. */
export type StateViewProps = StateContentProps & Readonly<{variant?: 'inline' | 'centered' | 'panel' | 'search';
  iconMetrics?: Readonly<{size: number; glyphSize: number; marginBottom?: number}>}>;

/** Small inline empty/error state. The surrounding page and its data remain caller-owned. */
export function StateView({title, body, icon, tone = 'neutral', primaryAction, secondaryAction, additionalActions, children, help, iconMetrics, variant = 'inline', style, testID}: StateViewProps) {
  const theme = useTheme();
  const tv = theme.isTV;
  const centered = variant === 'centered';
  const panel = variant === 'panel';
  const search = variant === 'search' && !tv;
  const phoneSearch = search && theme.platform === 'phone';
  const headingSize = tv ? centered ? 54 : panel ? 34 : 44 : search ? phoneSearch ? 24 : 22 : centered ? 30 : 26;
  const bodySize = tv ? centered ? 26 : panel ? 22 : 24 : search ? phoneSearch ? 14.5 : 14 : 15;
  const iconSize = iconMetrics?.size ?? (tv ? centered ? 112 : 88 : 54);
  const color = tone === 'warning' ? theme.colors.amber : tone === 'error' ? theme.colors.record : tone === 'accent' ? theme.colors.accentStrong : theme.colors.text;
  return <View testID={testID} style={[{alignItems: search ? 'stretch' : 'center', justifyContent: 'center', paddingHorizontal: search ? 0 : panel ? tv ? 48 : 24 : theme.gutter, paddingVertical: search ? 0 : tv ? panel ? 40 : 48 : 28,
    backgroundColor: panel ? theme.colors.raised : undefined, borderRadius: panel ? tv ? 26 : 14 : undefined}, style]}>
    {icon && !search && <View style={{width: iconSize, height: iconSize, borderRadius: panel ? tv ? 24 : 14 : theme.baseRadii.pill, backgroundColor: panel ? theme.colors.raisedHigh : `${theme.colors.text}14`, alignItems: 'center', justifyContent: 'center', marginBottom: iconMetrics?.marginBottom ?? (tv ? centered ? 30 : 22 : 18)}}>
      <Icon name={icon} size={iconMetrics?.glyphSize ?? (tv ? centered ? 54 : panel ? 44 : 38 : 26)} color={color} />
    </View>}
    <Text accessibilityRole="header" style={[phoneSearch ? {includeFontPadding: false} : undefined, nativeType({size: headingSize, lineHeight: headingSize * (phoneSearch ? 1.15 : search ? 1.2 : 1.08), weight: 800, tracking: panel ? -.02 : centered ? -.035 : -.03}, theme.textScale), {color: theme.colors.text, textAlign: search ? 'left' : 'center', marginBottom: search ? 6 : tv ? centered ? 14 : 10 : 10}]}>{title}</Text>
    {body != null && <Text style={[phoneSearch ? {includeFontPadding: false} : undefined, nativeType({size: bodySize, lineHeight: bodySize * (panel ? 1.45 : 1.5), weight: 400, tracking: 0}, theme.textScale), {color: panel ? theme.colors.textTertiary : theme.colors.textSecondary, textAlign: search ? 'left' : 'center', maxWidth: tv ? panel ? 560 : 900 : 440, marginBottom: phoneSearch ? 0 : tv ? centered ? 40 : 26 : 20}]}>{body}</Text>}
    {children}
    <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', gap: tv ? 18 : 10}}>
      {primaryAction && <StateActionButton testID={testID} action={primaryAction} size="hero" variant={panel ? 'glass' : 'primary'} />}
      {secondaryAction && <StateActionButton testID={testID} action={secondaryAction} size="hero" variant="glass" />}
      {additionalActions?.map(action => <StateActionButton testID={testID} key={action.id} action={action} size="compact" variant="text" />)}
    </View>
    {help != null && <Text style={[nativeType({size: tv ? 22 : 12.5, lineHeight: tv ? 33 : 18.75, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textTertiary, textAlign: 'center', maxWidth: tv ? 880 : 440, marginTop: tv ? 36 : 18}]}>{help}</Text>}
  </View>;
}
