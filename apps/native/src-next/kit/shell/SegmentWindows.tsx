import React, {useCallback, useEffect, useRef, useState} from 'react';
import {Text, TextInput, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {runOnJS, useAnimatedStyle, useSharedValue, type SharedValue} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {segmentKinds, type SegmentKind, type SegmentReviewEntry, type SegmentReviewMarker} from '../../../../../packages/client-core/src/segment-review';
import {Focusable} from '../controls/Focusable';
import {Button} from '../controls/Button';
import {Select} from '../controls/Select';
import {BottomSheet} from '../overlays/BottomSheet';
import {Menu} from '../overlays/Menu';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {shellType, alpha} from './visual';
import {markerTime, markerWindowParts, moveMarkerEdge, parseMarkerTime, validMarker, type MarkerBounds} from './segment-geometry';

export type SegmentWindow = MarkerBounds & {id: string; label: string};
function MarkerHandle({edge, bounds, window, durationUS, width, onChange, sharedOffset, height = 30}: {edge: 'start' | 'end'; bounds: MarkerBounds; window: SegmentWindow; durationUS: string; width: number; onChange: (bounds: MarkerBounds) => void; sharedOffset?: SharedValue<number>; height?: number}) {
  const theme = useTheme();
  const ownOffset = useSharedValue(0);
  const offset = sharedOffset ?? ownOffset;
  const current = Number(edge === 'start' ? bounds.startUS : bounds.endUS);
  const from = Number(window.startUS), span = Number(window.endUS) - from;
  const base = (current - from) / span * width;
  const apply = (delta: number) => {onChange(moveMarkerEdge(bounds, edge, current + delta / width * span, Number(durationUS))); offset.value = 0;};
  const opposite = Number(edge === 'start' ? bounds.endUS : bounds.startUS);
  const inWindow = Number.isFinite(current) && current >= from && current <= from + span;
  const pan = Gesture.Pan().onBegin(() => {offset.value = 0;}).onUpdate(event => {const delta = Math.max(-base, Math.min(width - base, event.translationX)); offset.value = edge === 'start' ? Math.min(delta, (opposite - current - 1) / span * width) : Math.max(delta, (opposite - current + 1) / span * width);}).onEnd(() => {runOnJS(apply)(offset.value);}).onFinalize((_event, success) => {if (!success) offset.value = 0;});
  const transform = useAnimatedStyle(() => ({transform: [{translateX: offset.value}]}));
  if (!inWindow) return null;
  return <GestureDetector gesture={pan}><Animated.View accessible accessibilityRole="adjustable" accessibilityLabel={defaultI18n.t(edge === 'start' ? 'web.segments.start' : 'web.segments.end')} accessibilityValue={{text: markerTime(edge === 'start' ? bounds.startUS : bounds.endUS)}} accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]} onAccessibilityAction={event => onChange(moveMarkerEdge(bounds, edge, current + (event.nativeEvent.actionName === 'increment' ? 1e6 : -1e6), Number(durationUS)))} style={[{position: 'absolute', left: base - 5, top: -5, width: 10, height: height + 10, borderRadius: 4, backgroundColor: theme.colors.text}, transform]}/></GestureDetector>;
}
function EditableMarkerBand({marker, bounds, window, durationUS, width, onChange, height}: {marker: SegmentReviewMarker; bounds: MarkerBounds; window: SegmentWindow; durationUS: string; width: number; onChange: (bounds: MarkerBounds) => void; height: number}) {
  const theme = useTheme();
  const startOffset = useSharedValue(0), endOffset = useSharedValue(0);
  const parts = markerWindowParts(bounds, window);
  const baseWidth = parts ? Math.max(1, (parts.right - parts.left) * width) : 1;
  const bandStyle = useAnimatedStyle(() => ({transform: [{translateX: (startOffset.value + endOffset.value) / 2}, {scaleX: Math.max(.001, (baseWidth + endOffset.value - startOffset.value) / baseWidth)}]}));
  if (!parts) return null;
  return <><Animated.View pointerEvents="none" style={[{position: 'absolute', top: 4, bottom: 4, left: parts.left * width, width: baseWidth, borderRadius: 5, backgroundColor: marker.kind === 'credits' ? theme.colors.textSecondary : theme.colors.accentStrong}, bandStyle]}/><MarkerHandle edge="start" bounds={bounds} window={window} durationUS={durationUS} width={width} onChange={onChange} sharedOffset={startOffset} height={height}/><MarkerHandle edge="end" bounds={bounds} window={window} durationUS={durationUS} width={width} onChange={onChange} sharedOffset={endOffset} height={height}/></>;
}

export function MarkerWindow({window, markers, durationUS, editingId, draft, onDraftChange}: {window: SegmentWindow; markers: readonly SegmentReviewMarker[]; durationUS: string; editingId?: string; draft?: MarkerBounds; onDraftChange?: (bounds: MarkerBounds) => void}) {
  const theme = useTheme();
  const [width, setWidth] = useState(0);
  const trackHeight = editingId ? 44 : theme.platform === 'phone' ? 28 : 30;
  return <View style={{gap: 6}}>
    <View style={{flexDirection: 'row', justifyContent: 'space-between'}}><Text style={[shellType(12, 700), {color: theme.colors.textSecondary}]}>{window.label}</Text><Text style={[shellType(12), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{defaultI18n.t('web.segments.range', {start: markerTime(window.startUS), end: markerTime(window.endUS)})}</Text></View>
    <View onLayout={event => setWidth(event.nativeEvent.layout.width)} style={{height: trackHeight, borderRadius: 7, backgroundColor: theme.colors.raisedHigh}}>
      {Array.from({length: 10}, (_, index) => <View key={index} pointerEvents="none" style={{position: 'absolute', top: 0, bottom: 0, left: `${index * 10}%`, width: 1, backgroundColor: alpha(theme.colors.text, .06)}}/>)}
      {markers.map(marker => {
        if (marker.id === editingId && draft && onDraftChange && width > 0) return <EditableMarkerBand key={marker.id} marker={marker} bounds={draft} window={window} durationUS={durationUS} width={width} onChange={onDraftChange} height={trackHeight}/>;
        const bounds = marker;
        const parts = markerWindowParts(bounds, window);
        if (!parts) return null;
        return <View key={marker.id} pointerEvents="none" style={{position: 'absolute', top: 4, bottom: 4, left: `${parts.left * 100}%`, width: `${(parts.right - parts.left) * 100}%`, borderRadius: 5, backgroundColor: marker.kind === 'credits' ? theme.colors.textSecondary : theme.colors.accentStrong, opacity: marker.approved || marker.automaticSafe ? 1 : .6, borderWidth: marker.approved || marker.automaticSafe ? 0 : 1.5, borderStyle: 'dashed', borderColor: theme.colors.white}}/>;
      })}
    </View>
  </View>;
}
function TimeStepper({label, value, onChange, onStep, error}: {label: string; value: string; onChange: (value: string) => void; onStep: (delta: number) => void; error?: string}) {
  const theme = useTheme();
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);
  const step = useRef(onStep), repeated = useRef(false);
  useEffect(() => {step.current = onStep;}, [onStep]);
  const clear = useCallback(() => {if (timer.current) clearInterval(timer.current); timer.current = null;}, []);
  useEffect(() => clear, [clear]);
  const [focused, setFocused] = useState(false);
  const stepper = (delta: -1 | 1) => <Focusable accessibilityRole="button" accessibilityLabel={defaultI18n.t(delta < 0 ? 'player.skipBackSeconds' : 'player.skipForwardSeconds', {seconds: 1})} onPressIn={() => {repeated.current = false;}} onPress={() => {if (!repeated.current) step.current(delta);}} onLongPress={() => {clear(); repeated.current = true; step.current(delta); timer.current = setInterval(() => step.current(delta), 100);}} onPressOut={clear} onBlur={clear} style={{width: 40, height: 48, alignItems: 'center', justifyContent: 'center'}}><Text style={[shellType(20), {color: theme.colors.textSecondary}]}>{defaultI18n.t(delta < 0 ? 'next.shell.native.decreaseSymbol' : 'next.shell.native.increaseSymbol')}</Text></Focusable>;
  return <View style={{flex: 1, gap: 6}}><Text style={[shellType(12.5, 700), {color: theme.colors.textTertiary}]}>{label}</Text><View style={{flexDirection: 'row', alignItems: 'center', height: 48, borderRadius: 12, backgroundColor: theme.colors.raisedHigh, borderWidth: 1, borderColor: focused ? theme.colors.focus : theme.colors.lineStrong}}>{stepper(-1)}<TextInput accessibilityLabel={label} value={value} onChangeText={onChange} keyboardType="numbers-and-punctuation" onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} style={[shellType(17, 700), {flex: 1, padding: 0, color: theme.colors.text, textAlign: 'center', fontVariant: ['tabular-nums']}]}/>{stepper(1)}</View>{error && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{error}</Text>}</View>;

}
export type SegmentWindowsProps = {
  entry: SegmentReviewEntry;
  windows: readonly [SegmentWindow, SegmentWindow];
  onSave: (marker: SegmentReviewMarker, bounds: MarkerBounds) => void | Promise<void>;
  error?: string;
  /** Saved together with the bounds, never applied when the picker alone changes. */
  onKindChange?: (marker: SegmentReviewMarker, kind: SegmentKind) => void | Promise<void>;
  onApprove: (marker: SegmentReviewMarker) => void;
  onDismiss: (marker: SegmentReviewMarker) => void;
  onWatch: (marker: SegmentReviewMarker, fromUS: string) => void;
  onAdd: () => void;
  saving?: boolean;
};
export function SegmentWindows({entry, windows, onSave, onKindChange, onApprove, onDismiss, onWatch, onAdd, saving = false, error}: SegmentWindowsProps) {
  const theme = useTheme();
  const [committing, setCommitting] = useState(false);
  const [editing, setEditing] = useState<SegmentReviewMarker | null>(null);
  const [draft, setDraft] = useState<MarkerBounds>({startUS: '0', endUS: '0'});
  const [menuMarker, setMenuMarker] = useState<SegmentReviewMarker | null>(null);
  const [entryMenu, setEntryMenu] = useState(false);
  const [editingKind, setEditingKind] = useState<SegmentKind>('intro');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const update = (bounds: MarkerBounds) => {setDraft(bounds); setStart(markerTime(bounds.startUS)); setEnd(markerTime(bounds.endUS));};
  const current = editing && entry.markers.find(marker => marker.id === editing.id);
  const valid = Boolean(current && validMarker(draft, entry.durationUS));
  const save = async () => {if (current && valid && !saving && !committing) {setCommitting(true); try {if (onKindChange && editingKind !== current.kind) await onKindChange({...current, ...draft}, editingKind); else await onSave(current, draft); setEditing(null);} catch { /* Caller presents its localized save error; preserve the typed draft. */ } finally {setCommitting(false);}}};
  const windowForEdit = current ? windows.find(window => markerWindowParts(draft, window)) ?? windows[0] : windows[0];
  const watch = (marker: SegmentReviewMarker) => onWatch(marker, String(Math.max(0, Number(marker.startUS) - 3e6)));
  const adjust = (marker: SegmentReviewMarker) => {setEditing(marker); setEditingKind(marker.kind); update(marker);};
  const smallAction = (label: string, icon: 'i-play' | 'i-check' | 'i-edit' | 'i-more', action: () => void) => <Focusable accessibilityRole="button" accessibilityLabel={label} onPress={action} hitSlop={5} style={{width: 34, height: 34, alignItems: 'center', justifyContent: 'center', borderRadius: 9}}><Icon name={icon} size={17} color={theme.colors.textSecondary}/></Focusable>;
  if (theme.isTV) return null;
  return <View style={{gap: 12}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}><Text style={[shellType(15, 700), {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('web.segments.episode', {number: entry.number, title: entry.title})}</Text>{smallAction(defaultI18n.t('next.shell.native.moreOptions'), 'i-more', () => setEntryMenu(true))}</View>
    <View style={{flexDirection: theme.platform === 'phone' ? 'column' : 'row', gap: theme.platform === 'phone' ? 10 : 22}}>{windows.map(window => <View key={window.id} style={{flex: 1}}><MarkerWindow window={window} markers={entry.markers} durationUS={entry.durationUS}/>
      {entry.markers.filter(marker => markerWindowParts(marker, window)).map(marker => <View key={marker.id} style={{gap: 2, marginTop: 8}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, minHeight: 34}}><Text style={[shellType(13, 700), {color: theme.colors.text}]}>{defaultI18n.t('web.segments.kind', {kind: marker.kind})}</Text><Text style={[shellType(13), {color: theme.colors.textSecondary, flex: 1, fontVariant: ['tabular-nums']}]}>{defaultI18n.t('web.segments.range', {start: markerTime(marker.startUS), end: markerTime(marker.endUS)})}</Text><View style={{flexDirection: 'row'}}>{smallAction(defaultI18n.t('next.shell.native.watchFromBefore'), 'i-play', () => watch(marker))}{!marker.approved && smallAction(defaultI18n.t('web.segments.approve'), 'i-check', () => onApprove(marker))}{smallAction(defaultI18n.t('web.segments.adjust'), 'i-edit', () => adjust(marker))}{smallAction(defaultI18n.t('next.shell.native.moreOptions'), 'i-more', () => setMenuMarker(marker))}</View></View>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}><Text style={[shellType(12, 700), {color: marker.approved || marker.automaticSafe ? theme.colors.healthy : theme.colors.accentStrong}]}>{defaultI18n.t('web.segments.trust', {trust: marker.approved ? 'approved' : marker.automaticSafe ? 'automatic' : marker.viewerVisible ? 'suggested' : 'hidden'})}</Text><Text style={[shellType(12), {color: theme.colors.textTertiary}]}>{defaultI18n.t('web.segments.basis', {basis: marker.basis})}</Text><Text style={[shellType(12), {color: theme.colors.textTertiary}]}>{defaultI18n.t('web.segments.confidence', {percent: Math.round(marker.confidence * 100)})}</Text></View>
      </View>)}
    </View>)}</View>
    <Menu open={Boolean(menuMarker)} onOpenChange={open => {if (!open) setMenuMarker(null);}} title={menuMarker ? defaultI18n.t('web.segments.kind', {kind: menuMarker.kind}) : entry.title} presentation="sheet" items={menuMarker ? [{id: 'watch', label: defaultI18n.t('next.shell.native.watchFromBefore'), onSelect: () => watch(menuMarker)}, ...(!menuMarker.approved ? [{id: 'approve', label: defaultI18n.t('web.segments.approve'), onSelect: () => onApprove(menuMarker)}] : []), {id: 'adjust', label: defaultI18n.t('web.segments.adjust'), onSelect: () => adjust(menuMarker)}, {id: 'dismiss', label: defaultI18n.t('action.dismiss'), destructive: true, onSelect: () => onDismiss(menuMarker)}] : []}/>
    <Menu open={entryMenu} onOpenChange={setEntryMenu} title={entry.title} presentation="sheet" items={[{id: 'add', label: defaultI18n.t('web.segments.addTitle'), onSelect: onAdd}]}/>
    <BottomSheet open={Boolean(current)} onOpenChange={open => {if (!open) setEditing(null);}} title={defaultI18n.t('next.shell.native.adjustSegment', {kind: current ? defaultI18n.t('web.segments.kind', {kind: current.kind}).toLocaleLowerCase('en-US') : ''})} description={defaultI18n.t('next.shell.dragOrType')} footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 10}}><Button variant="glass" size="small" onPress={() => setEditing(null)}>{defaultI18n.t('action.cancel')}</Button><Button size="small" onPress={save} disabled={!valid} loading={saving || committing}>{defaultI18n.t('action.save')}</Button></View>}>
      <View style={{padding: 20, gap: 24}}>{error && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{error}</Text>}{current && onKindChange && <Select label={defaultI18n.t('web.segments.kindLabel')} value={editingKind} options={segmentKinds.map(kind => ({value: kind, label: defaultI18n.t('web.segments.kind', {kind})}))} onValueChange={kind => setEditingKind(kind as SegmentKind)}/>}<MarkerWindow window={windowForEdit} markers={entry.markers} durationUS={entry.durationUS} editingId={current?.id} draft={draft} onDraftChange={update}/>
        <View style={{flexDirection: 'row', gap: 14}}><TimeStepper label={defaultI18n.t('next.shell.start')} value={start} onChange={text => {setStart(text); const parsed = parseMarkerTime(text); setDraft({...draft, startUS: parsed ?? ''});}} onStep={delta => update(moveMarkerEdge(draft, 'start', Number(draft.startUS) + delta * 1e6, Number(entry.durationUS)))}/><TimeStepper label={defaultI18n.t('next.shell.end')} value={end} onChange={text => {setEnd(text); const parsed = parseMarkerTime(text); setDraft({...draft, endUS: parsed ?? ''});}} onStep={delta => update(moveMarkerEdge(draft, 'end', Number(draft.endUS) + delta * 1e6, Number(entry.durationUS)))}/></View>
      {current && <><Button variant="glass" size="small" icon="i-play" onPress={() => {if (valid) watch({...current, ...draft});}} disabled={!valid}>{defaultI18n.t('next.shell.native.watchFromBefore')}</Button><Button variant="text" size="small" onPress={() => {onDismiss(current); setEditing(null);}} textStyle={{color: theme.colors.record}}>{defaultI18n.t('action.dismiss')}</Button></>}
      </View>
    </BottomSheet>
  </View>;
}
