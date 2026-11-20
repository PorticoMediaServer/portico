import React, {type ReactNode} from 'react';
import {StyleSheet, View} from 'react-native';
import {SidePanel, type SidePanelProps} from '../overlays';
import {Layer} from '../shell/Layer';
import type {DockOccupant} from '../dock/types';
import type {shellLayout} from '../../shell/model';
import {TVPageGroup} from '../../tv/layout/TVPageMotion';

type Canvas = ReturnType<typeof shellLayout>;

export function TVAudioSurface({ready, ambient, testID, children}: {
  ready: boolean; ambient: DockOccupant['presentation']['ambient']; testID: string; children: ReactNode;
}) {
  return <View testID={testID} pointerEvents={ready ? 'auto' : 'none'} accessibilityElementsHidden={!ready}
    importantForAccessibility={ready ? 'auto' : 'no-hide-descendants'} style={StyleSheet.absoluteFill}>
    <Layer kind="audio" art={ambient} style={StyleSheet.absoluteFill}/>
    {children}
  </View>;
}

export function TVAudioCanvas({canvas, children}: {canvas: Canvas; children: ReactNode}) {
  return <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
    transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
    <TVPageGroup index={0} style={{flex: 1}}>{children}</TVPageGroup>
  </View>;
}

export function TVAudioEqualizer({canvas, children}: {canvas: Canvas; children: ReactNode}) {
  return <Layer style={StyleSheet.absoluteFill}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>{children}</View>
  </Layer>;
}

export function TVAudioLyrics(props: SidePanelProps) {
  return <SidePanel {...props} width={640} topInset={0}/>;
}
