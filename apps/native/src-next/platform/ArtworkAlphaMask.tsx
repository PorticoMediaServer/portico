import React from 'react';
import {StyleSheet} from 'react-native';
import MaskedView from '@react-native-masked-view/masked-view';
import Svg, {Defs, LinearGradient, Mask, Rect, Stop} from 'react-native-svg';

/** A stationary alpha mask; the authenticated Image and its drift stay inside. */
export function ArtworkAlphaMask({leftOpaqueAt, bottomOpaqueAt, children}: {
  leftOpaqueAt: number; bottomOpaqueAt: number; children: React.ReactNode;
}) {
  return <MaskedView style={StyleSheet.absoluteFillObject} androidRenderingMode="hardware" maskElement={
    <Svg accessible={false} pointerEvents="none" width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none">
      <Defs>
        <LinearGradient id="alpha-left" x1="0%" y1="0%" x2="100%" y2="0%">
          <Stop offset={0} stopColor="#fff" stopOpacity={0}/><Stop offset={leftOpaqueAt} stopColor="#fff" stopOpacity={1}/>
        </LinearGradient>
        <LinearGradient id="alpha-bottom" x1="0%" y1="100%" x2="0%" y2="0%">
          <Stop offset={0} stopColor="#fff" stopOpacity={0}/><Stop offset={bottomOpaqueAt} stopColor="#fff" stopOpacity={1}/>
        </LinearGradient>
        <Mask id="alpha-intersection" x="0" y="0" width="1000" height="1000" maskUnits="userSpaceOnUse" maskType="alpha">
          <Rect width="1000" height="1000" fill="url(#alpha-left)"/>
        </Mask>
      </Defs>
      <Rect width="1000" height="1000" fill="url(#alpha-bottom)" mask="url(#alpha-intersection)"/>
    </Svg>
  }>{children}</MaskedView>;
}
