import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import Animated, {runOnJS} from 'react-native-reanimated';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import type {Library} from '../../../../../packages/client-core/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Banner, BottomSheet} from '../../kit/overlays';
import {browseType, alpha} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import {NativeDestinationPins} from './pins';
import {nativePinDrop, type NativePinRowBounds} from './pins-drag';
import {usePinDragPaint} from './pins-drag-paint';

type PinRow = Readonly<{id: string; name: string; pinned: boolean; moveable: boolean; toggle: () => void; move: (by: -1 | 1) => void}>;
type Drag = Readonly<{id: string; translation: number; beforeId?: string; lineY: number}>;
const t = defaultI18n.t;

/** Ruling 39: frame25's grouped rows, genuine immediate pin writes, no footer. */
export function NativePinEditorSheet({controller, libraries, onClose}: {
  controller: NativeDestinationPins; libraries: readonly Library[]; onClose: () => void;
}) {
  const theme = useTheme(), state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [dragging, setDragging] = useState(false);
  useEffect(() => {void controller.load();}, [controller]);
  const byId = new Map(libraries.map(library => [library.id, library])), pinned = state.libraries?.pinnedLibraryIds ?? [];
  const ordered = state.libraries ? [...pinned.map(id => byId.get(id)).filter((value): value is Library => !!value), ...libraries.filter(library => !pinned.includes(library.id))] : [];
  const libraryRows = ordered.map(library => ({id: library.id, name: library.name, pinned: pinned.includes(library.id), moveable: pinned.includes(library.id),
    toggle: () => {void controller.pinLibrary(library.id, !pinned.includes(library.id));}, move: (by: -1 | 1) => {void controller.moveLibrary(library.id, by);}}));
  const resources = state.resources?.order ?? [], resourceKey = (entry: typeof resources[number]) => JSON.stringify([entry.kind, entry.id]);
  const resourceRows = resources.map(entry => ({id: resourceKey(entry), name: entry.name, pinned: true, moveable: true,
    toggle: () => {void controller.unpinResource(entry);}, move: (by: -1 | 1) => {void controller.moveResource(entry, by);}}));
  return <BottomSheet open testID="native-pin-editor" title={t('pins.editTitle')} description={t('pins.librariesHint')} showClose={false}
    dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy && !dragging} dismissOnGesture={!state.busy && !dragging}
    onOpenChange={open => {if (!open && !state.busy) onClose();}}>
    <View style={{gap: 16}}>
      {state.error !== undefined && <Banner testID="pins-error" variant="notice" tone="error" placement="inline" message={presentError(state.error, 'library', {operation: 'save'}).body}/>}
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('pins.libraries')}</Text>
      <PinGroup id="pin-libraries" rows={libraryRows} busy={state.busy || !state.libraries} onDragging={setDragging}
        place={(id, before) => {void controller.placeLibrary(id, before);}}/>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('pins.saved')}</Text>
      {resources.length ? <PinGroup id="pin-resources" rows={resourceRows} busy={state.busy} onDragging={setDragging} place={(id, before) => {
        const entry = resources.find(value => resourceKey(value) === id), target = before === undefined ? undefined : resources.find(value => resourceKey(value) === before);
        if (entry && (before === undefined || target)) void controller.placeResource(entry, target);
      }}/> : state.phase === 'ready' && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('pins.noneSaved')}</Text>}
    </View>
  </BottomSheet>;
}

function PinGroup({id, rows, busy, place, onDragging}: {id: string; rows: readonly PinRow[]; busy: boolean;
  place: (id: string, beforeId?: string) => void; onDragging: (value: boolean) => void}) {
  const theme = useTheme(), positions = useRef(new Map<string, NativePinRowBounds>()), latest = useRef({rows, busy, place, onDragging});
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
      paint.begin(measured?.y ?? target.lineY, measured?.height ?? 54);
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
  return <View testID={id} style={{position: 'relative', borderRadius: 14, backgroundColor: theme.colors.raisedHigh}}>
    {rows.map((row, index) => <View key={row.id} testID={`${id}-row-${encodeURIComponent(row.id)}`} onLayout={event => {
      const {y, height} = event.nativeEvent.layout; positions.current.set(row.id, {id: row.id, y, height});
    }} style={drag?.id === row.id ? {height: positions.current.get(row.id)?.height, borderWidth: 2, borderColor: theme.colors.lineStrong, borderStyle: 'dashed', borderRadius: 10} : undefined}>
      {drag?.beforeId === row.id && <DropLine y={0}/>}
      {index > 0 && <View pointerEvents="none" style={{position: 'absolute', left: 44, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.line, .4)}}/>}
      <Animated.View accessibilityElementsHidden={drag?.id === row.id} importantForAccessibility={drag?.id === row.id ? 'no-hide-descendants' : 'auto'}
        style={[{minHeight: 54, paddingVertical: 6, paddingLeft: 6, paddingRight: 12, flexDirection: 'row', alignItems: 'center', gap: 8}, drag?.id === row.id ? paint.sourceRowStyle : undefined]}>
        {row.moveable ? <PinHandle id={`${id}-${row.id}`} row={row} disabled={busy || !!drag && drag.id !== row.id}
          first={!rows.slice(0, index).some(value => value.moveable)} last={!rows.slice(index + 1).some(value => value.moveable)} callbacks={callbacks} paint={paint}/>
          : <View style={{width: 36, height: 40}}/>}
        <Text numberOfLines={1} style={[browseType(theme, 15.5, 600), {color: theme.colors.text, flex: 1}]}>{row.name}</Text>
        <Focusable testID={`${id}-toggle-${encodeURIComponent(row.id)}`} focusId={`${id}-toggle:${row.id}`} accessibilityRole="togglebutton"
          accessibilityState={{checked: row.pinned}} accessibilityLabel={t(row.pinned ? 'pins.unpinNamed' : 'pins.pinNamed', {name: row.name})}
          disabled={busy || !!drag} onPress={row.toggle} style={{width: 32, height: 40, alignItems: 'center', justifyContent: 'center'}}>
          <Icon name={row.pinned ? 'i-bookmark-f' : 'i-bookmark'} size={20} color={row.pinned ? theme.colors.accentStrong : theme.colors.textTertiary}/>
        </Focusable>
      </Animated.View>
    </View>)}
    {drag && drag.beforeId === undefined && <DropLine y={drag.lineY}/>}
    {selected && original && <Animated.View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
      style={[paint.overlayStyle, {position: 'absolute', top: 0, left: -6, right: -6, zIndex: 8,
        backgroundColor: theme.colors.raisedBright, borderRadius: 12, borderWidth: 2, borderColor: theme.colors.accent,
        paddingLeft: 12, paddingRight: 12, flexDirection: 'row', alignItems: 'center', gap: 8}]}>
      <View style={{width: 36, height: 40, justifyContent: 'center', alignItems: 'center'}}><Icon name="i-drag" size={20} color={theme.colors.text}/></View>
      <Text numberOfLines={1} style={[browseType(theme, 15.5, 600), {color: theme.colors.text, flex: 1}]}>{selected.name}</Text>
      <Icon name="i-bookmark-f" size={20} color={theme.colors.accentStrong}/>
    </Animated.View>}
  </View>;
}

function PinHandle({id, row, disabled, first, last, callbacks, paint}: {id: string; row: PinRow; disabled: boolean; first: boolean; last: boolean;
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
    }} style={{width: 36, height: 40, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-drag" size={20} color={theme.colors.textTertiary}/></Focusable></GestureDetector>;
}
function DropLine({y}: {y: number}) {
  const theme = useTheme();
  return <View pointerEvents="none" style={{position: 'absolute', top: y - 2, left: 6, right: 6, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong, zIndex: 9}}>
    <View style={{position: 'absolute', left: -6, top: -3, width: 10, height: 10, borderRadius: 5, backgroundColor: theme.colors.accentStrong}}/>
  </View>;
}
