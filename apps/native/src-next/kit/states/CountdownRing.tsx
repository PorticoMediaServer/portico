import React from 'react';
import {View} from 'react-native';
import Svg, {Circle} from 'react-native-svg';
import {useTheme} from '../theme';
import {ringProgress} from './model';

export type CountdownRingProps = Readonly<{
  progress?: number | null;
  size?: number;
  accessibilityLabel?: string;
  testID?: string;
}>;

/** A still arc. Unknown progress is the empty track, never an invented spinner. */
export function CountdownRing({progress, size, accessibilityLabel, testID}: CountdownRingProps) {
  const theme = useTheme();
  const value = ringProgress(progress);
  const diameter = size ?? (theme.isTV ? 34 : 20);
  const circumference = 2 * Math.PI * 9;
  return <View testID={testID} accessible={Boolean(accessibilityLabel)} accessibilityLabel={accessibilityLabel}
    accessibilityRole={accessibilityLabel ? 'progressbar' : undefined}
    accessibilityValue={value === null ? undefined : {min: 0, max: 100, now: Math.round(value * 100)}}
    style={{width: diameter, height: diameter, flexShrink: 0}}>
    <Svg width={diameter} height={diameter} viewBox="0 0 24 24" accessible={false} focusable={false}>
      <Circle cx={12} cy={12} r={9} fill="none" stroke={theme.colors.text} strokeOpacity={.16} strokeWidth={2.6} />
      {value !== null && value > 0 && <Circle cx={12} cy={12} r={9} fill="none" stroke={theme.colors.accentStrong} strokeWidth={2.6}
        strokeLinecap="round" strokeDasharray={`${circumference} ${circumference}`} strokeDashoffset={circumference * (1 - value)} transform="rotate(-90 12 12)" />}
    </Svg>
  </View>;
}
