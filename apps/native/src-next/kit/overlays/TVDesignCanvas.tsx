import React from 'react';
import {StyleSheet, View, useWindowDimensions} from 'react-native';
import {tvCanvasFrame} from '../../platform/layout/tv-canvas';
import {useTheme} from '../theme';

/** A native Modal/portal escapes the shell's transformed page, so scales once here.
 * Vega's window contract already reports canvas points and its Modal supplies the
 * physical scale; its 1920 × 1080 window therefore stays at scale 1 here. */
export function TVDesignCanvas({children}: {children: React.ReactNode}) {
  const {isTV} = useTheme();
  const window = useWindowDimensions();
  if (!isTV) return <>{children}</>;
  const frame = tvCanvasFrame(window.width, window.height);
  return <View pointerEvents="box-none" style={StyleSheet.absoluteFill}>
    <View pointerEvents="box-none" style={{position: 'absolute', width: frame.width, height: frame.height, left: frame.left, top: frame.top, transformOrigin: 'top left', transform: [{scale: frame.scale}]}}>{children}</View>
  </View>;
}
