import React, {useRef, useState} from 'react';
import {Platform, View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useTheme} from '../theme';
import {listEdges, noListEdges, type ListEdges} from './list-edges';

/**
 * The one drawn edge that says a list continues past it (live-owner frames 21 and 27, tv-detail frame 5l): ink fading in
 * over `length` points to `opacity` at the list's top or bottom edge. It never takes focus, a press or a screen reader's
 * attention, and over an empty page it fades ink into ink and disappears. The list's own window places it.
 */
export function ListEdgeFade({edge, length, opacity, color, testID}: {edge: 'top' | 'bottom'; length: number; opacity: number; /** The surface the list sits on (a panel's raised fill); ink when it is the page. */ color?: string; testID?: string}) {
  const theme = useTheme(), id = `list-edge-fade-${edge}`, fill = color ?? theme.colors.ink;
  return <View testID={testID} pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
    style={{position: 'absolute', left: 0, right: 0, height: length, ...(edge === 'top' ? {top: 0} : {bottom: 0})}}>
    <Svg accessible={false} width="100%" height="100%"><Defs><LinearGradient id={id} x1="0" y1={edge === 'top' ? '1' : '0'} x2="0" y2={edge === 'top' ? '0' : '1'}>
      <Stop offset="0" stopColor={fill} stopOpacity={0}/><Stop offset="1" stopColor={fill} stopOpacity={opacity}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${id})`}/></Svg>
  </View>;
}

/** A vertical ScrollView's `listEdges`, kept current from its own scroll, layout and content size: spread `scroll` on it.
 * On Android the platform draws the edge itself (`fadingEdgeLength`), which also keeps a row that takes focus that far
 * from the edge instead of under the fade; `edges` then stays empty and the caller draws nothing. */
type EdgeScrollProps = Readonly<{fadingEdgeLength?: number; scrollEventThrottle?: number; onScroll?: (event: NativeSyntheticEvent<NativeScrollEvent>) => void;
  onLayout?: (event: LayoutChangeEvent) => void; onContentSizeChange?: (width: number, height: number) => void}>;
export function useListEdges(length: number): {edges: ListEdges; scroll: EdgeScrollProps} {
  const [edges, setEdges] = useState<ListEdges>(noListEdges), at = useRef({offset: 0, content: 0, viewport: 0});
  const show = () => {const next = listEdges(at.current.offset, at.current.content, at.current.viewport); setEdges(now => now.top === next.top && now.bottom === next.bottom ? now : next);};
  if (Platform.OS === 'android') return {edges: noListEdges, scroll: {fadingEdgeLength: length}};
  return {edges, scroll: {
    scrollEventThrottle: 64,
    onScroll: (event: NativeSyntheticEvent<NativeScrollEvent>) => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
      at.current = {offset: contentOffset.y, content: contentSize.height, viewport: layoutMeasurement.height}; show();},
    onLayout: (event: LayoutChangeEvent) => {at.current.viewport = event.nativeEvent.layout.height; show();},
    onContentSizeChange: (_width: number, height: number) => {at.current.content = height; show();},
  }};
}
