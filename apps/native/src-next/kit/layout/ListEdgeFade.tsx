import React from 'react';
import {View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useTheme} from '../theme';

/**
 * The one drawn edge that says a list continues past it (live-owner frames 21 and 27, tv-detail frame 5l): ink fading in
 * over `length` points to `opacity` at the list's top or bottom edge. It never takes focus, a press or a screen reader's
 * attention, and over an empty page it fades ink into ink and disappears. The list's own window places it.
 */
export function ListEdgeFade({edge, length, opacity, testID}: {edge: 'top' | 'bottom'; length: number; opacity: number; testID?: string}) {
  const theme = useTheme(), id = `list-edge-fade-${edge}`;
  return <View testID={testID} pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
    style={{position: 'absolute', left: 0, right: 0, height: length, ...(edge === 'top' ? {top: 0} : {bottom: 0})}}>
    <Svg accessible={false} width="100%" height="100%"><Defs><LinearGradient id={id} x1="0" y1={edge === 'top' ? '1' : '0'} x2="0" y2={edge === 'top' ? '0' : '1'}>
      <Stop offset="0" stopColor={theme.colors.ink} stopOpacity={0}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={opacity}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${id})`}/></Svg>
  </View>;
}
