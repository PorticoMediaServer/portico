import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import Animated, {runOnJS} from 'react-native-reanimated';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';
import type {PinEditorRow} from '../../pages/shared/saved/usePinEditor';
import {useFrameMetrics} from '../frames/metrics';
import {nativePinDrop, type NativePinRowBounds} from '../../screens/destinations/pins-drag';
import {usePinDragPaint} from '../../screens/destinations/pins-drag-paint';

type Drag = Readonly<{id: string; translation: number; beforeId?: string; lineY: number}>;
const t = defaultI18n.t;

export function PinGroup({id, title, rows, busy, place, onDragging, empty}: {title: string; empty?: React.ReactNode; id: string; rows: readonly PinEditorRow[]; busy: boolean;
  place: (id: string, beforeId?: string) => void; onDragging: (value: boolean) => void}) {
  const theme = useTheme(), frame = useFrameMetrics(), positions = useRef(new Map<string, NativePinRowBounds>()), latest = useRef({rows, busy, place, onDragging});
  latest.current = {rows, busy, place, onDragging};
  const [drag, setDrag] = useState<Drag>(), actual = useRef<Drag | undefined>(undefined), paint = usePinDragPaint();
  const cancel = () => {if (!actual.current) return; actual.current = undefined; paint.end(); setDrag(undefined); latest.current.onDragging(false);};
  // A selected row that is removed, becomes unmoveable or busy retires the paint;
  // the group's own callbacks and placement stay untouched.
  useEffect(() => {if (drag && (busy || !rows.some(row => row.id === drag.id && row.moveable))) cancel();});
  const endPaint = paint.end;
  useEffect(() => () => endPaint(), [endPaint]);
  const bounds = () => latest.current.rows.filter(row => row.moveable).flatMap(row => {const value = positions.current.get(row.id); return value ? [value] : [];});
  const callbacks = {
    start: (picked: string) => {
      if (latest.current.busy || !latest.current.rows.some(row => row.id === picked && row.moveable)) return;
      const target = nativePinDrop(bounds(), picked, 0); if (!target) return;
      const measured = positions.current.get(picked);
      actual.current = {id: picked, translation: 0, ...target};
      paint.begin(measured?.y ?? target.lineY, measured?.height ?? frame.rowTwoLine);
      setDrag(actual.current); latest.current.onDragging(true);
    },
    move: (translation: number) => {
      const old = actual.current; if (!old) return;
      const target = nativePinDrop(bounds(), old.id, translation); if (!target) return;
      actual.current = {...old, translation, ...target};
      // The drop line is the only model-backed preview; re-render it when it
      // actually moves, never once per finger frame.
      if (target.beforeId !== old.beforeId || target.lineY !== old.lineY) setDrag(actual.current);
    },
    drop: (success: boolean) => {
      const picked = actual.current; cancel(); if (success && picked && !latest.current.busy) latest.current.place(picked.id, picked.beforeId);
    }, cancel,
  };
  const selected = drag && rows.find(row => row.id === drag.id), original = drag && positions.current.get(drag.id);
  return <View style={{gap: theme.space.xs, marginBottom: theme.space.sm}}>
    <Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{title}</Text>
    {rows.length ? <View testID={id} style={{position: 'relative', borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.raisedHigh}}>
      {rows.map((row, index) => <View key={row.id} testID={`${id}-row-${encodeURIComponent(row.id)}`} onLayout={event => {
        const {y, height} = event.nativeEvent.layout; positions.current.set(row.id, {id: row.id, y, height});
      }} style={drag?.id === row.id ? {height: positions.current.get(row.id)?.height, borderWidth: 2, borderColor: theme.colors.lineStrong, borderStyle: 'dashed', borderRadius: 10} : undefined}>
        {drag?.beforeId === row.id && <DropLine y={0}/>}
        {index > 0 && <View pointerEvents="none" style={{position: 'absolute', left: theme.targets.touch, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.line, .4)}}/>}
        <Animated.View accessibilityElementsHidden={drag?.id === row.id} importantForAccessibility={drag?.id === row.id ? 'no-hide-descendants' : 'auto'}
          style={[{minHeight: frame.rowTwoLine, paddingVertical: theme.space.xs, paddingHorizontal: theme.space.xs,
            flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}, drag?.id === row.id ? paint.sourceRowStyle : undefined]}>
          {row.moveable ? <PinHandle id={`${id}-${row.id}`} row={row} disabled={busy || !!drag && drag.id !== row.id}
            first={!rows.slice(0, index).some(value => value.moveable)} last={!rows.slice(index + 1).some(value => value.moveable)} callbacks={callbacks} paint={paint}/>
            : <View style={{width: theme.targets.touch}}/>}
          <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1, minWidth: 0}]}>{row.name}</Text>
          <Focusable testID={`${id}-toggle-${encodeURIComponent(row.id)}`} focusId={`${id}-toggle:${row.id}`} accessibilityRole="togglebutton"
            accessibilityState={{checked: row.pinned}} accessibilityLabel={t(row.pinned ? 'pins.unpinNamed' : 'pins.pinNamed', {name: row.name})}
            disabled={busy || !!drag} onPress={row.toggle} style={{width: theme.targets.touch, height: theme.targets.touch, alignItems: 'center', justifyContent: 'center'}}>
            <Icon name={row.pinned ? 'i-bookmark-f' : 'i-bookmark'} size={20} color={row.pinned ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          </Focusable>
        </Animated.View>
      </View>)}
      {drag && drag.beforeId === undefined && <DropLine y={drag.lineY}/>}
      {selected && original && <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
        style={[paint.overlayStyle, {position: 'absolute', top: 0, left: -theme.space.xs, right: -theme.space.xs, zIndex: 8,
          backgroundColor: theme.colors.raisedBright, borderRadius: 12, borderWidth: 2, borderColor: theme.colors.accent,
          paddingHorizontal: theme.space.xs, flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}]}>
        <View style={{width: theme.targets.touch, height: theme.targets.touch, justifyContent: 'center', alignItems: 'center'}}><Icon name="i-drag" size={20} color={theme.colors.text}/></View>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1, minWidth: 0}]}>{selected.name}</Text>
        <Icon name="i-bookmark-f" size={20} color={theme.colors.accentStrong}/>
      </Animated.View>}
    </View> : empty}
  </View>;
}

function PinHandle({id, row, disabled, first, last, callbacks, paint}: {id: string; row: PinEditorRow; disabled: boolean; first: boolean; last: boolean;
  callbacks: {start: (id: string) => void; move: (y: number) => void; drop: (success: boolean) => void; cancel: () => void};
  paint: ReturnType<typeof usePinDragPaint>}) {
  const theme = useTheme(), latest = useRef(callbacks); latest.current = callbacks;
  // Stable JS wrappers: their bodies read the newest callbacks on the JS side,
  // so no React ref or non-shareable callback is ever captured by a worklet.
  const startJS = useCallback((id: string) => {latest.current.start(id);}, []);
  const moveJS = useCallback((y: number) => {latest.current.move(y);}, []);
  const dropJS = useCallback((success: boolean) => {latest.current.drop(success);}, []);
  const cancelJS = useCallback(() => {latest.current.cancel();}, []);
  // Only the pure displacement worklet and the wrappers cross the boundary.
  const moveWorklet = paint.move;
  const gesture = useMemo(() => Gesture.Pan().enabled(!disabled).activateAfterLongPress(300)
    .onStart(() => {runOnJS(startJS)(row.id);})
    .onUpdate(event => {moveWorklet(event.translationY); runOnJS(moveJS)(event.translationY);})
    .onEnd((_event, success) => {runOnJS(dropJS)(success);})
    .onFinalize(() => {runOnJS(cancelJS)();}), [disabled, row.id, moveWorklet, startJS, moveJS, dropJS, cancelJS]);
  return <GestureDetector gesture={gesture}><Focusable testID={`${id}-handle`} focusId={`${id}-handle`} accessibilityRole="adjustable"
    accessibilityLabel={row.name} disabled={disabled} accessibilityActions={[
      ...(!first ? [{name: 'decrement', label: t('pins.moveUp', {name: row.name})}] : []),
      ...(!last ? [{name: 'increment', label: t('pins.moveDown', {name: row.name})}] : []),
    ]} onAccessibilityAction={event => {
      if (!disabled && event.nativeEvent.actionName === 'decrement' && !first) row.move(-1);
      if (!disabled && event.nativeEvent.actionName === 'increment' && !last) row.move(1);
    }} style={{width: theme.targets.touch, height: theme.targets.touch, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-drag" size={20} color={theme.colors.textTertiary}/></Focusable></GestureDetector>;
}
function DropLine({y}: {y: number}) {
  const theme = useTheme();
  return <View pointerEvents="none" style={{position: 'absolute', top: y - 2, left: 6, right: 6, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong, zIndex: 9}}>
    <View style={{position: 'absolute', left: -6, top: -3, width: 10, height: 10, borderRadius: 5, backgroundColor: theme.colors.accentStrong}}/>
  </View>;
}
