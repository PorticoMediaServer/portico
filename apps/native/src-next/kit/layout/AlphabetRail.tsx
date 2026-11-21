import React, {useCallback, useMemo, useRef, useState} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import {runOnJS} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {fontFamilies, useTheme} from '../theme';
import {alphabetAnchors, alphabetBand} from './model';
import {Focusable} from '../controls/Focusable';
import {FocusRegion} from '../focus/FocusRegion';
import type {FocusEdges} from '../focus/registry';
import {focusTestID} from '../focus/registry';

export type AlphabetRailProps = {
  positionIndex: Readonly<Record<string, number>>;
  total: number;
  firstVisible: number;
  lastVisible: number;
  currentKey?: string;
  onJump: (index: number, key: string) => void;
  height?: number;
  style?: StyleProp<ViewStyle>;
  testID?: string;
  /** TV: the rail is a focus region beside the grid; it fills the height it is given (`height` is ignored). */
  focus?: Readonly<{id: string; edges?: FocusEdges; enabled?: boolean}>;
};

export function AlphabetRail({positionIndex, total, firstVisible, lastVisible, currentKey, onJump, height: given = 514, style, testID, focus}: AlphabetRailProps) {
  const theme = useTheme(), [hovered, setHovered] = useState<string>(), [focused, setFocused] = useState<string>();
  const [measured, setMeasured] = useState<number>(), height = focus ? measured ?? 0 : given;
  const anchors = useMemo(() => alphabetAnchors(positionIndex, total), [positionIndex, total]);
  const band = alphabetBand(anchors, firstVisible, lastVisible), pitch = height / anchors.length;
  const current = currentKey ?? anchors.find(anchor => anchor.index !== undefined && anchor.index <= firstVisible && (anchor.end ?? anchor.index) >= firstVisible)?.key;
  const draggingKey = useRef<string | undefined>(undefined);
  const drag = useCallback((y: number) => {
    const anchor = anchors[Math.max(0, Math.min(anchors.length - 1, Math.floor(y / pitch)))];
    setHovered(anchor.key);
    if (anchor.index !== undefined && draggingKey.current !== anchor.key) {draggingKey.current = anchor.key; onJump(anchor.index, anchor.key);}
  }, [anchors, onJump, pitch]);
  const endDrag = useCallback(() => {draggingKey.current = undefined; setHovered(undefined);}, []);
  const pan = Gesture.Pan().enabled(!theme.isTV).activeOffsetY([-2, 2]).onStart(event => {runOnJS(drag)(event.y);})
    .onUpdate(event => {runOnJS(drag)(event.y);}).onFinalize(() => {runOnJS(endDrag)();});
  const bubble = hovered ?? focused, bubbleIndex = anchors.findIndex(anchor => anchor.key === bubble);
  // On TV a letter's box is as tall as its share of the rail allows, up to the full 44.
  const box = theme.isTV ? Math.min(44, Math.floor(pitch) - 2) : 16;
  // tvOS reports the old letter's blur after the new letter's focus: a blur clears only its own letter.
  const letters = anchors.map(anchor => {
      const selected = current === anchor.key, empty = anchor.index === undefined, isFocused = focused === anchor.key;
      return <Focusable focusId={focus && `letter:${anchor.key}`} testID={testID ? focusTestID(testID, JSON.stringify(['bucket', anchor.key])) : undefined} focusTreatment="none" key={anchor.key} disabled={empty} focusable={!empty} accessibilityRole="button"
        accessibilityState={{disabled: empty, selected}} accessibilityLabel={defaultI18n.t('web.filters.jumpToKey', {key: anchor.key})}
        onPress={() => {if (anchor.index !== undefined) onJump(anchor.index, anchor.key);}}
        onHoverIn={() => setHovered(anchor.key)} onHoverOut={() => setHovered(undefined)}
        onPressIn={() => setHovered(anchor.key)} onPressOut={() => setHovered(undefined)}
        onFocus={() => setFocused(anchor.key)} onBlur={() => setFocused(key => key === anchor.key ? undefined : key)}
        style={{width: theme.isTV ? 64 : 18, height: pitch, alignItems: 'center', justifyContent: 'center', zIndex: isFocused ? theme.layers.tvFocus : 1}}>
        <View style={{width: box, height: box, borderRadius: theme.isTV ? 14 : 8, alignItems: 'center', justifyContent: 'center',
          backgroundColor: selected ? theme.colors.accentStrong : undefined, borderWidth: isFocused ? theme.spec.focus.ringWidth.tv : 0, borderColor: theme.colors.focus}}>
          <Text style={{fontFamily: fontFamilies[700], fontSize: (theme.isTV ? 20 : 10) * theme.textScale, color: empty ? `${theme.colors.textTertiary}4D` : selected ? theme.colors.ink : theme.colors.textTertiary}}>{anchor.key}</Text>
        </View>
      </Focusable>;
    });
  // TV: the rail opens on the letter in view (as the Jump to sheet did), not on the letter last used.
  const order = anchors.filter(anchor => anchor.index !== undefined).map(anchor => `letter:${anchor.key}`);
  return <GestureDetector gesture={pan}><View testID={testID} accessibilityLabel={defaultI18n.t('web.filters.jumpTo')}
    onLayout={focus ? event => setMeasured(event.nativeEvent.layout.height) : undefined}
    style={[{width: theme.isTV ? 64 : 18, height: focus ? '100%' : height, position: 'relative', alignItems: 'center'}, style]}>
    {band && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: band.first * pitch, height: (band.last - band.first + 1) * pitch, borderRadius: 14, backgroundColor: `${theme.colors.text}13`}} />}
    {focus ? <FocusRegion id={focus.id} edges={focus.edges} enabled={focus.enabled} orientation="vertical" order={order} entryPolicy="entry"
      entry={current && order.includes(`letter:${current}`) ? `letter:${current}` : order[0]}>{letters}</FocusRegion> : letters}
    {bubble && <View pointerEvents="none" style={{position: 'absolute', right: theme.isTV ? 84 : 32, top: Math.max(0, Math.min(height - 58, bubbleIndex * pitch - 21)), width: 58, height: 58, borderRadius: 16, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}>
      <Text style={{fontFamily: fontFamilies[800], fontSize: 30 * theme.textScale, color: theme.colors.text}}>{bubble}</Text>
    </View>}
  </View></GestureDetector>;
}
