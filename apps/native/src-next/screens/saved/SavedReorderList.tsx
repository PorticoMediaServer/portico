import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import Animated, {runOnJS, useSharedValue} from 'react-native-reanimated';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {MediaArt} from '../../kit/artwork';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {usePinDragPaint} from '../destinations/pins-drag-paint';
import {createSavedReorderGesture, savedReorderTarget} from './paging';

const t = defaultI18n.t, rowHeight = 80;
type Row = Readonly<{id: string; media: ContentEntry}>;
type Move = 'up' | 'down' | 'top' | 'bottom';
type Drag = Readonly<{row: Row; ids: readonly string[]; index: number; target?: ReturnType<typeof savedReorderTarget>}>;

/** Manual membership order only. Paint moves on the UI thread; the drop commits one
 * existing occurrence and insertion neighbor through the page's guarded command. */
export function SavedReorderList({rows, runtime, disabled, onMove, onPlace, onDragging}: {
  rows: readonly Row[]; runtime: NativeShellRuntime; disabled: boolean;
  onMove: (row: Row, where: Move) => void;
  onPlace: (row: Row, afterEntryId: string, ids: readonly string[]) => void;
  onDragging: (value: boolean) => void;
}) {
  const theme = useTheme(), paint = usePinDragPaint(), [drag, setDrag] = useState<Drag>();
  const actual = useRef<Drag | undefined>(undefined), alive = useRef(true), latest = useRef({rows, disabled, onMove, onPlace, onDragging});
  latest.current = {rows, disabled, onMove, onPlace, onDragging};
  const cancel = () => {if (!actual.current) return; actual.current = undefined; paint.end(); setDrag(undefined); latest.current.onDragging(false);};
  useEffect(() => {
    const at = actual.current;
    if (at && (disabled || at.ids.length !== rows.length || rows.some((row, index) => row.id !== at.ids[index] || row.id === at.row.id && row.media !== at.row.media))) cancel();
  });
  const endPaint = paint.end;
  useLayoutEffect(() => {alive.current = true; return () => {alive.current = false; actual.current = undefined; endPaint(); latest.current.onDragging(false);};}, [endPaint]);
  const callbacks = {
    start: (id: string) => {
      if (!alive.current || latest.current.disabled || actual.current) return false;
      const index = latest.current.rows.findIndex(row => row.id === id), row = latest.current.rows[index];
      if (!row) return false;
      actual.current = {row, index, ids: latest.current.rows.map(value => value.id)};
      paint.begin(index * rowHeight, rowHeight); setDrag(actual.current); latest.current.onDragging(true); return true;
    },
    move: (translation: number) => {
      const at = actual.current; if (!alive.current || !at) return;
      const target = savedReorderTarget(at.ids, at.row.id, translation, rowHeight);
      if (target?.afterEntryId === at.target?.afterEntryId) return;
      actual.current = {...at, target}; setDrag(actual.current);
    },
    drop: (success: boolean) => {
      const at = actual.current; cancel();
      if (alive.current && success && at?.target && !latest.current.disabled) latest.current.onPlace(at.row, at.target.afterEntryId, at.ids);
    }, cancel,
  };
  const targetIndex = drag?.target?.index ?? drag?.index ?? 0;
  const lineY = (drag && targetIndex > drag.index ? targetIndex + 1 : targetIndex) * rowHeight;
  return <View testID="saved-reorder-list" style={{position: 'relative', marginHorizontal: theme.gutter}}>
    {rows.map((row, index) => <View key={row.id} style={{height: rowHeight, borderRadius: 12,
      ...(drag?.row.id === row.id ? {borderWidth: 1, borderStyle: 'dashed', borderColor: theme.colors.lineStrong} : {})}}>
      <SavedDragRow row={row} index={index} last={rows.length - 1} runtime={runtime} callbacks={callbacks} paint={paint}
        disabled={disabled || !!drag && drag.row.id !== row.id} lifted={drag?.row.id === row.id}
        onMove={where => {if (alive.current && !latest.current.disabled) latest.current.onMove(row, where);}}/>
    </View>)}
    {drag && <>
      <View pointerEvents="none" testID="saved-reorder-drop-line" style={{position: 'absolute', left: 4, right: 4, top: lineY - 2, height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong, zIndex: 10}}/>
      <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[paint.overlayStyle, {
        position: 'absolute', top: 0, left: -6, right: -6, borderRadius: 12, zIndex: 9, backgroundColor: theme.colors.raisedBright,
        borderWidth: 2, borderColor: theme.colors.accent, shadowColor: theme.colors.ink, shadowOpacity: .6, shadowRadius: 20,
        shadowOffset: {width: 0, height: 24}, elevation: 12,
      }]}><ReorderFace row={drag.row} runtime={runtime}/></Animated.View>
    </>}
  </View>;
}

function SavedDragRow({row, index, last, runtime, callbacks, paint, disabled, lifted, onMove}: {
  row: Row; index: number; last: number; runtime: NativeShellRuntime; disabled: boolean; lifted: boolean; onMove: (where: Move) => void;
  callbacks: {start: (id: string) => boolean; move: (translation: number) => void; drop: (success: boolean) => void; cancel: () => void};
  paint: ReturnType<typeof usePinDragPaint>;
}) {
  const latest = useRef(callbacks); latest.current = callbacks;
  const session = useMemo(() => createSavedReorderGesture({start: id => latest.current.start(id),
    move: y => latest.current.move(y), drop: success => latest.current.drop(success)}), []);
  const startJS = useCallback((owner: 'row' | 'handle', id: string) => session.begin(owner, id), [session]);
  const moveJS = useCallback((owner: 'row' | 'handle', y: number) => session.move(owner, y), [session]);
  const finishJS = useCallback((owner: 'row' | 'handle', success: boolean) => session.finish(owner, success), [session]);
  const movePaint = paint.move, gestureOwner = useSharedValue(0);
  const gesture = useMemo(() => Gesture.Pan().enabled(!disabled).activateAfterLongPress(300)
    .onStart(() => {if (gestureOwner.value === 0) {gestureOwner.value = 1; runOnJS(startJS)('row', row.id);}})
    .onUpdate(event => {if (gestureOwner.value === 1) {movePaint(event.translationY); runOnJS(moveJS)('row', event.translationY);}})
    .onFinalize((_event, success) => {if (gestureOwner.value === 1) {gestureOwner.value = 0; runOnJS(finishJS)('row', success);}}),
    [disabled, row.id, gestureOwner, movePaint, startJS, moveJS, finishJS]);
  const handle = useMemo(() => Gesture.Pan().enabled(!disabled).activeOffsetY([-2, 2]).simultaneousWithExternalGesture(gesture)
    .onStart(() => {if (gestureOwner.value === 0) {gestureOwner.value = 2; runOnJS(startJS)('handle', row.id);}})
    .onUpdate(event => {if (gestureOwner.value === 2) {movePaint(event.translationY); runOnJS(moveJS)('handle', event.translationY);}})
    .onFinalize((_event, success) => {if (gestureOwner.value === 2) {gestureOwner.value = 0; runOnJS(finishJS)('handle', success);}}),
    [disabled, row.id, gesture, gestureOwner, movePaint, startJS, moveJS, finishJS]);
  const accessibilityActions = [
    ...(index > 0 ? [{name: 'top', label: t('order.moveTop')}, {name: 'decrement', label: t('player.queueMoveUp')}] : []),
    ...(index < last ? [{name: 'increment', label: t('player.queueMoveDown')}, {name: 'bottom', label: t('order.moveBottom')}] : []),
  ];
  return <GestureDetector gesture={gesture}><Animated.View style={[{height: rowHeight}, lifted ? paint.sourceRowStyle : undefined]}>
    <Focusable testID={`saved-reorder-row-${encodeURIComponent(row.id)}`} focusId={`saved-reorder-row-${row.id}`} accessibilityRole="adjustable"
      accessibilityLabel={row.media.title} accessibilityActions={accessibilityActions} disabled={disabled}
      onAccessibilityAction={event => {const name = event.nativeEvent.actionName;
        if (index > 0 && (name === 'top' || name === 'decrement')) onMove(name === 'top' ? 'top' : 'up');
        if (index < last && (name === 'bottom' || name === 'increment')) onMove(name === 'bottom' ? 'bottom' : 'down');
      }} style={{height: rowHeight}}>
      <ReorderFace row={row} runtime={runtime} handle={<GestureDetector gesture={handle}>
        <View testID={`saved-reorder-handle-${encodeURIComponent(row.id)}`} style={{width: 36, height: rowHeight, alignItems: 'center', justifyContent: 'center'}}><DragIcon/></View>
      </GestureDetector>}/>
    </Focusable>
  </Animated.View></GestureDetector>;
}
function DragIcon() {const theme = useTheme(); return <Icon name="i-drag" size={20} color={theme.colors.textTertiary}/>;}
function ReorderFace({row, runtime, handle}: {row: Row; runtime: NativeShellRuntime; handle?: React.ReactNode}) {
  const theme = useTheme(), entry = row.media, square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  return <View style={{height: rowHeight, flexDirection: 'row', alignItems: 'center', gap: 12, paddingRight: 8}}>
    {handle ?? <View style={{width: 36, alignItems: 'center'}}><DragIcon/></View>}
    <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={48} height={square ? 48 : 72} style={{borderRadius: 6}}/>
    <View style={{flex: 1, minWidth: 0, gap: 3}}><Text numberOfLines={1} style={[browseType(theme, 15.5, 700, -.01), {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
      {!!entry.year && <Text style={[browseType(theme, 12.5), {color: theme.colors.textTertiary}]}>{entry.year}</Text>}</View>
    <View pointerEvents="none" style={{position: 'absolute', left: 48, right: 0, bottom: 0, height: 1, backgroundColor: alpha(theme.colors.line, .3)}}/>
  </View>;
}
