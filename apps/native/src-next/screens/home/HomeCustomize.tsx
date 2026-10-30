import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {findNodeHandle, ScrollView, Text, TVEventHandler, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import {defaultI18n, type I18n} from '../../../../../packages/i18n/src';
import {serverTextLabel} from '../../../../../packages/client-core/src/presentation/server-text';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import {Button, Focusable, Switch} from '../../kit/controls';
import {useFocusRegistry} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {alpha} from '../../kit/browse/presentation';
import {BottomSheet, Dialog, SidePanel} from '../../kit/overlays';
import {focusTarget} from '../../kit/overlays/OverlayHost';
import {nativeType, useTheme} from '../../kit/theme';
import {NativeHomeCustomizeController, type NativeHomeResetOpening} from './customize-controller';
import {createTVKeyAdapter} from '../../platform/tv-key-adapter';
import {tvKeySource} from '../../platform/tv-key-source';
import {createHomeRowDrag, type HomeDragRow} from './customize-drag';

/** Exact catalogue values supplied by the canonical copy owner. No screen copy
 * or server row label is manufactured by this presentation. */
export type NativeHomeCustomizeCopy = Readonly<{
  title: string; instruction: string; alwaysOn: string; addView: string; reset: string; save: string;
  conflictTitle: string; conflictBody: string; reload: string; keepEditing: string;
  move: string; drop: string; putBack: string;
}>;

/** A pick-up lands on the row itself: the D-pad listener moves only a lifted row that holds
 * focus, so a pick started from the row's own handle must not leave focus on the handle. A
 * later Back, reset, removal, close or unmount leaves the frame with nothing to do. */
function focusWhenPicked(
  controller: NativeHomeCustomizeController,
  frame: {current: Readonly<{controller: NativeHomeCustomizeController; id: number}> | undefined},
  picked: {current: View | null},
  pickedId: string,
  also?: () => void,
) {
  if (frame.current) cancelAnimationFrame(frame.current.id);
  const id = requestAnimationFrame(() => {
    const held = frame.current;
    frame.current = undefined;
    if (!held || held.controller !== controller || !controller.isCurrent() || controller.getSnapshot().pickedId !== pickedId) return;
    also?.();
    focusTarget(picked.current);
  });
  frame.current = {controller, id};
}

export function HomeDragHandle({controller, id, disabled, geometry, scrollGesture, testID}: {
  controller: NativeHomeCustomizeController; id: string; disabled: boolean;
  geometry: () => readonly HomeDragRow[]; scrollGesture: ReturnType<typeof Gesture.Native>; testID: string;
}) {
  const theme = useTheme(), latest = useRef(geometry);
  latest.current = geometry;
  const drag = useMemo(() => createHomeRowDrag(controller, id, () => latest.current()), [controller, id]);
  useEffect(() => () => drag.finish(false), [drag]);
  const pan = useMemo(() => Gesture.Pan().enabled(!disabled).activateAfterLongPress(200).blocksExternalGesture(scrollGesture).activeOffsetY([-2, 2]).runOnJS(true)
    .onStart(() => {drag.begin();}).onUpdate(event => {drag.update(event.translationY);})
    .onFinalize((_event, success) => {drag.finish(success);}), [disabled, drag, scrollGesture]);
  return <GestureDetector gesture={pan}><View testID={testID} accessible={false} collapsable={false}
    style={{width: 44, minHeight: 50, alignItems: 'center', justifyContent: 'center'}}>
    <Icon name="i-grip" size={20} color={theme.colors.textTertiary}/>
  </View></GestureDetector>;
}

export function HomeCustomize({controller, copy, i18n, onClose, onAddView, views, summary, notice, noticeAction, renderError, testID}: {
  controller: NativeHomeCustomizeController; copy: NativeHomeCustomizeCopy; i18n: Pick<I18n, 't' | 'has'>;
  onClose: () => void; onAddView: () => void; testID: string; summary: (view: PersonalResource) => string | undefined; notice?: string; noticeAction?: Readonly<{label: string; onPress: () => void}>;
  renderError?: (error: unknown, retry: () => void) => React.ReactNode;
  views?: Readonly<{items: readonly PersonalResource[]; loading: boolean; error?: unknown; hasMore: boolean; loadMore: () => void; renderError: (error: unknown, retry: () => void) => React.ReactNode; retry: () => void}>;
}) {
  const theme = useTheme(), registry = useFocusRegistry();
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const addView = useRef<View>(null), resetButton = useRef<View>(null);
  const rowRefs = useRef(new Map<string, View>());
  const first = useRef<View>(null), reload = useRef<View>(null), cancelReset = useRef<View>(null);
  const picked = useRef<View>(null), scroller = useRef<ScrollView>(null), returning = useRef(false);
  const [reset, setReset] = useState<NativeHomeResetOpening>(), [choosing, setChoosing] = useState(false);
  const resetOwner = useRef<NativeHomeResetOpening | undefined>(undefined);
  if (resetOwner.current && (resetOwner.current.controller !== controller || resetOwner.current.layout !== state.layout || state.phase !== 'ready' || state.pending || state.retry || state.pickedId || state.conflict)) resetOwner.current = undefined;
  const resetOpening = reset && resetOwner.current === reset && reset.controller === controller ? reset : undefined;
  const closeReset = (opening: NativeHomeResetOpening | undefined) => {
    if (!opening || resetOwner.current !== opening) return;
    resetOwner.current = undefined;setReset(value => value === opening ? undefined : value);
  };
  const confirmReset = (opening: NativeHomeResetOpening | undefined) => {
    if (!opening || resetOwner.current !== opening || opening.controller !== controller || !controller.isCurrent()) return;
    closeReset(opening);void opening.controller.resetConfirmed(opening);
  };
  useLayoutEffect(() => () => {if (resetOwner.current?.controller === controller) resetOwner.current = undefined;}, [controller]);
  const firstView = useRef<View>(null), viewEntryFocused = useRef(false);
  const [heights, setHeights] = useState<ReadonlyMap<string, number>>(new Map());
  const [positions, setPositions] = useState<ReadonlyMap<string, number>>(new Map());
  useEffect(() => {
    const ids = new Set(state.rows.map(row => row.id));
    const prune = (before: ReadonlyMap<string, number>) => {if ([...before.keys()].every(id => ids.has(id))) return before;return new Map([...before].filter(([id]) => ids.has(id)));};
    setPositions(prune);setHeights(prune);
  }, [state.rows]);
  const regionId = testID;
  const blocked = state.phase !== 'ready' || state.pending || !!state.retry;
  const returnToAdd = () => {setChoosing(false);requestAnimationFrame(() => {if (controller.isCurrent()) focusTarget(addView.current);});};
  const back = () => {if (choosing) {returnToAdd();return;} if (!controller.cancelPickup()) onClose();};
  useEffect(() => {
    if (!choosing) {viewEntryFocused.current = false; return;}
    if (!theme.isTV || views?.loading || viewEntryFocused.current) return;
    const frame = requestAnimationFrame(() => {if (firstView.current) {focusTarget(firstView.current); viewEntryFocused.current = true;}});
    return () => cancelAnimationFrame(frame);
  }, [choosing, theme.isTV, views?.items, views?.loading]);
  useEffect(() => {
    if (!controller.isCurrent() || !theme.isTV || state.phase !== 'ready') return;
    const frame = requestAnimationFrame(() => controller.isCurrent() && focusTarget(state.showConflict ? reload.current : first.current ?? addView.current));
    return () => cancelAnimationFrame(frame);
  }, [controller, state.phase, state.showConflict, theme.isTV]);
  /** collections.html 29: the list comes back with the new row held, focused and in view, so Up
   * and Down move it at once. The pick-up listener only moves a row that holds focus, so a held
   * row no one can reach would be a dead end. */
  useEffect(() => {
    if (!returning.current || choosing || !theme.isTV) return;
    returning.current = false;
    if (!state.pickedId) return;
    focusWhenPicked(controller, focusFrame, picked, state.pickedId, () => {scroller.current?.scrollToEnd({animated: true});});
  }, [choosing, controller, state.pickedId, theme.isTV]);
  useEffect(() => {
    if (!theme.isTV || !state.pickedId || state.pending) return;
    const keys = createTVKeyAdapter({source: tvKeySource});
    const listener = TVEventHandler.addListener(raw => {
      const event = keys.read(raw);
      if (!event || event.phase !== 'press' || !['up', 'down'].includes(event.key)) return;
      const active = registry.capture();
      if (active?.regionId !== regionId || active.childId !== state.pickedId || !picked.current) return;
      if (event.tag !== undefined && event.tag !== findNodeHandle(picked.current)) return;
      controller.move(event.key === 'up' ? -1 : 1);
    });
    return () => listener?.remove();
  }, [controller, regionId, registry, state.pending, state.pickedId, theme.isTV]);
  const rowHeight = theme.isTV ? 70 : 58;
  /** One focus frame at a time, leased to the controller that asked for it. The lease is
   * cancelled synchronously when this screen is torn down or handed a different controller, so a
   * retained component can never run an old callback against its own `picked` ref, and the frame
   * still re-checks that the same controller reports that exact row as picked. */
  const focusFrame = useRef<Readonly<{controller: NativeHomeCustomizeController; id: number}> | undefined>(undefined);
  useLayoutEffect(() => () => {
    if (focusFrame.current) cancelAnimationFrame(focusFrame.current.id);
    focusFrame.current = undefined;
  }, [controller]);
  const pickRow = (id: string) => {
    if (!controller.pick(id)) return;
    focusWhenPicked(controller, focusFrame, picked, id);
  };
  const rowsById = new Map(state.rows.map(row => [row.id, row]));
  const displayed = state.pickupOrder ? state.pickupOrder.map(id => rowsById.get(id)!).filter(Boolean) : state.rows;
  const targetIndex = state.rows.findIndex(row => row.id === state.pickedId), targetId = state.pickupOrder?.[targetIndex];
  const targetY = targetId ? positions.get(targetId) : undefined;
  const dragGeometry = (): readonly HomeDragRow[] => {
    let top = theme.isTV ? 16 : 8;
    return displayed.map(row => {
      const height = heights.get(row.id) ?? rowHeight;
      const slot = {id: row.id, top: positions.get(row.id) ?? top, height};
      top = slot.top + height;
      return slot;
    });
  };
  const scrollGesture = useMemo(() => Gesture.Native(), []);
  const rowList = <ScrollView ref={scroller} removeClippedSubviews={false} scrollEnabled={theme.isTV || !state.pickedId} contentContainerStyle={{paddingTop: theme.isTV ? 16 : 8, paddingBottom: 20}}>
    {displayed.map((row, index) => {
      const lifted = row.id === state.pickedId;
      const title = serverTextLabel(i18n, row.titleText, row.title);
      const detail = row.savedView ? summary(row.savedView) : serverTextLabel(i18n, row.availabilityText);
      const measuredHeight = heights.get(row.id) ?? rowHeight;
      const originalY = positions.get(row.id), shift = lifted && targetY !== undefined && originalY !== undefined ? targetY - originalY : 0;
      return <View key={row.id} onLayout={event => {const {y, height} = event.nativeEvent.layout;setHeights(before => {if (before.get(row.id) === height) return before;const next = new Map(before);next.set(row.id, height);return next;}); setPositions(before => {if (before.get(row.id) === y) return before; const next = new Map(before); next.set(row.id, y); return next;});}}
        style={{minHeight: rowHeight, height: lifted ? measuredHeight : undefined, zIndex: lifted ? 8 : undefined, ...(lifted ? {borderWidth: 2, borderStyle: 'dashed', borderRadius: theme.isTV ? 0 : 14, borderColor: theme.isTV ? theme.colors.line : alpha(theme.colors.lineStrong, .6)} : {})}}><View style={{flexDirection: 'row', alignItems: 'center', minHeight: rowHeight, paddingVertical: theme.isTV ? 6 : 4, gap: theme.isTV ? 18 : 10, position: lifted ? 'absolute' : undefined, left: lifted ? 0 : undefined, right: lifted ? 0 : undefined,
        backgroundColor: lifted ? theme.isTV ? theme.colors.raisedHigh : theme.colors.raisedBright : undefined, borderRadius: 14, transform: lifted ? [{translateY: shift}, {scale: theme.isTV ? 1.03 : 1.02}] : undefined,
        ...(!theme.isTV && lifted ? {boxShadow: [{offsetX: 0, offsetY: 24, blurRadius: 50, spreadDistance: -16, color: alpha(theme.colors.shadow, .9)}, {offsetX: 0, offsetY: 0, blurRadius: 0, spreadDistance: 2, color: theme.colors.accent}]} : {})}}>
        {theme.isTV && row.reorderable && <Focusable focusId={`${row.id}:handle`} testID={`${testID}-handle-${row.id}`} accessibilityRole="button"
          accessibilityLabel={defaultI18n.t('next.home.layoutMoveAction')} disabled={blocked || !!state.pickedId && !lifted}
          focusKind="button" focusRadius={10} onPress={() => pickRow(row.id)}
          style={{width: 44, height: 50, alignItems: 'center', justifyContent: 'center'}}>
          <Icon name="i-grip" size={26} color={lifted ? theme.colors.text : theme.colors.textTertiary}/>
        </Focusable>}
        {!theme.isTV && row.reorderable && <HomeDragHandle controller={controller} id={row.id} disabled={blocked || !!state.pickedId && !lifted} geometry={dragGeometry} scrollGesture={scrollGesture} testID={`${testID}-handle-${row.id}`}/>}
        {row.savedView && <View style={{width: 36, height: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .1)}}>
          <Icon name="i-filter" size={22} color={theme.colors.textSecondary}/>
        </View>}
        <Focusable ref={node => {if (node) rowRefs.current.set(row.id, node);else rowRefs.current.delete(row.id);if (lifted) picked.current = node;if (index === 0) first.current = node;}} focusId={row.id} testID={`${testID}-row-${row.id}`}
          disabled={blocked || !!state.pickedId && !lifted} focusKind="row" focusRadius={16}
          hasTVPreferredFocus={index === 0 && !state.showConflict && !choosing} accessibilityLabel={title} accessibilityState={{selected: lifted}}
          onPress={theme.isTV ? () => pickRow(row.id) : undefined} onAccessibilityAction={event => {
            if (event.nativeEvent.actionName === 'increment' || event.nativeEvent.actionName === 'decrement') {
              if (!lifted && !controller.pick(row.id)) return;
              if (controller.getSnapshot().pickedId !== row.id) return;
              controller.move(event.nativeEvent.actionName === 'increment' ? 1 : -1); controller.drop();
            }
          }} accessibilityActions={row.reorderable ? [{name: 'increment', label: defaultI18n.t('home.customize.moveDown', {title})}, {name: 'decrement', label: defaultI18n.t('home.customize.moveUp', {title})}] : []}
          style={{flex: 1, minHeight: rowHeight, flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 18 : 14, paddingLeft: theme.isTV ? 12 : 8}}>
          <View style={{flex: 1}}><Text style={[nativeType({size: theme.isTV ? 26 : 16, weight: theme.isTV ? 600 : 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: row.hidden ? theme.colors.textTertiary : theme.colors.text}]}>{title}</Text>
            {!!detail && <Text style={[nativeType({size: theme.isTV ? 19 : 13, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginTop: theme.isTV ? 1 : 4}]}>{detail}</Text>}
          </View>
        </Focusable>
        {row.savedView && <Focusable focusId={`${row.id}:remove`} testID={`${testID}-remove-${row.id}`} accessibilityRole="button"
          accessibilityLabel={defaultI18n.t('home.customize.removeRow', {title})} disabled={blocked || !!state.pickedId}
          focusKind="button" focusRadius={21} onPress={() => {const neighbor = state.rows[index + 1]?.id ?? state.rows[index - 1]?.id;if (controller.removeSavedView(row.id)) requestAnimationFrame(() => {if (controller.isCurrent()) focusTarget(neighbor ? rowRefs.current.get(neighbor) ?? addView.current : addView.current);});}}
          style={{width: theme.isTV ? 42 : 44, height: theme.isTV ? 42 : 44, borderRadius: 22, alignItems: 'center', justifyContent: 'center'}}>
          <Icon name="i-close" size={22} color={theme.colors.textTertiary}/>
        </Focusable>}
        {row.required ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 9, paddingRight: 18}}><Icon name="i-lock" size={theme.isTV ? 18 : 14} color={theme.colors.textTertiary}/><Text style={[nativeType({size: theme.isTV ? 20 : 12, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{copy.alwaysOn}</Text></View>
          : row.hideable && <Switch focusId={`${row.id}:visibility`} checked={!row.hidden} disabled={blocked || !!state.pickedId}
            accessibilityLabel={defaultI18n.t('home.customize.showRow', {title})} onCheckedChange={() => controller.toggle(row.id)}/>}
      </View></View>;
    })}
    {state.pickedId && targetY !== undefined && (theme.isTV ? <View pointerEvents="none" style={{position: 'absolute', left: 12, right: 12, top: targetY - 3, height: 6, backgroundColor: theme.colors.accentStrong, borderRadius: 3}}/>
      : <View pointerEvents="none" style={{position: 'absolute', zIndex: 5, left: 4, right: 8, top: targetY - 2, height: 4, backgroundColor: theme.colors.accentStrong, borderRadius: 2,
        boxShadow: [{offsetX: 0, offsetY: 0, blurRadius: 16, color: alpha(theme.colors.accentStrong, .6)}]}}>
        <View style={{position: 'absolute', left: -6, top: -4, width: 12, height: 12, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/>
      </View>)}
    {!state.pickedId && <Button ref={addView} focusId="add-view" variant="text" icon="i-plus" disabled={blocked || !!state.pickedId} onPress={() => {if (views) setChoosing(true); onAddView();}}>{copy.addView}</Button>}
  </ScrollView>;
  const rows = theme.isTV ? rowList : <GestureDetector gesture={scrollGesture}>{rowList}</GestureDetector>;
  const footer = choosing ? <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 18}}>
    {views?.hasMore ? <Button focusId="views-more" testID={`${testID}-views-more`} variant="text" disabled={views.loading} onPress={views.loadMore}>{i18n.t('action.showMore')}</Button> : <View/>}
    <Button focusId="views-cancel" testID={`${testID}-views-cancel`} variant="glass" onPress={returnToAdd}>{i18n.t('action.cancel')}</Button>
  </View> : state.pickedId ? undefined : <View style={{flexDirection: 'row', justifyContent: 'space-between', gap: 18}}>
    <Button ref={resetButton} focusId="reset" variant="glass" disabled={blocked || state.conflict || !!state.pickedId} onPress={() => {const opening = controller.captureReset(state.layout);if (opening) {resetOwner.current = opening;setReset(opening);}}}>{i18n.t('action.resetToDefault')}</Button>
    <Button focusId="save" disabled={blocked || state.conflict || !state.dirty || !!state.pickedId} loading={state.pending} onPress={() => {void controller.save().then(saved => {if (saved) onClose();});}}>{copy.save}</Button>
  </View>;
  const body = choosing && views ? views.loading && !views.items.length ? <View/> : views.error ? views.renderError(views.error, views.retry) : <HomeSavedViewChoices disabled={views.loading || blocked} summary={summary} views={views.items.filter(view => !state.rows.some(row => row.id === `view:${view.id}`))} hasMore={views.hasMore} i18n={i18n} firstRef={firstView}
    onChoose={view => {if (!controller.addSavedView(view, theme.isTV)) return; if (theme.isTV) returning.current = true; setChoosing(false);}}/> : <>{(notice || noticeAction) && <View style={{paddingVertical: 12, gap: 8}}>{notice && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{notice}</Text>}{noticeAction && <Button variant="text" onPress={noticeAction.onPress}>{noticeAction.label}</Button>}</View>}{state.showConflict && <View style={{paddingVertical: 20, flexDirection: 'row', gap: 16}}>
    <Icon name="i-warn" size={30} color={theme.colors.textSecondary}/><View style={{flex: 1, gap: 12}}><Text style={[theme.type.title, {color: theme.colors.text}]}>{copy.conflictTitle}</Text><Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{copy.conflictBody}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 18}}><Button focusId="reload" ref={reload} disabled={state.pending || state.phase === 'loading'} onPress={() => {void controller.load();}}>{copy.reload}</Button><Button focusId="keep-editing" variant="glass" onPress={() => {controller.keepEditing();if (controller.isCurrent()) focusTarget(first.current ?? addView.current);}}>{copy.keepEditing}</Button></View>
    </View>
  </View>}{state.error && !state.showConflict && renderError?.(state.error, () => {if (state.phase === 'error') void controller.load();else {const saving = state.retry === 'save';void controller.retry().then(done => {if (done && saving && controller.isCurrent()) onClose();});}})}{rows}</>;
  return <>
    {theme.isTV && state.pickedId && <View pointerEvents="none" style={{position: 'absolute', left: 200, right: 960, bottom: 56, flexDirection: 'row', justifyContent: 'center', gap: 28}}>
      {[['▲▼', copy.move], [defaultI18n.t('next.layout.ok'), copy.drop], [defaultI18n.t('action.back'), copy.putBack]].map(([key, label]) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 10}}><Text style={[theme.type.captionStrong, {color: theme.colors.text, backgroundColor: theme.colors.raised, paddingHorizontal: 10, borderRadius: 6}]}>{key}</Text><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{label}</Text></View>)}
    </View>}
    {theme.isTV ? <SidePanel open onOpenChange={open => {if (!open) back();}} title={choosing ? defaultI18n.t('home.customize.addView') : copy.title} description={choosing ? defaultI18n.t('home.customize.chooseView') : state.showConflict ? undefined : copy.instruction} width={860} topInset={0} logicalCanvas={{width: 860, height: 1080}}
      showClose={false} titleType={{size: 44, weight: 800, tracking: -.03, lineHeight: 'normal'}}
      descriptionTextStyle={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 30.8}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 8}]} testID={testID} initialFocusRef={choosing ? firstView : first} footer={footer}
      style={{paddingTop: 46, paddingLeft: 60, paddingRight: 96}}>{body}</SidePanel>
      : <BottomSheet open onOpenChange={open => {if (!open) back();}} title={choosing ? defaultI18n.t('home.customize.addView') : copy.title} description={defaultI18n.t(choosing ? 'home.customize.chooseView' : 'home.customize.subtitle')}
        testID={testID} initialFocusRef={choosing ? firstView : first} footer={footer}>{body}</BottomSheet>}
    <Dialog testID={`${testID}-reset-confirm`} open={!!resetOpening} onOpenChange={open => {if (!open) closeReset(resetOpening);}} title={defaultI18n.t('confirm.resetHome.title')} description={defaultI18n.t('confirm.resetHome.body')}
      initialFocusRef={cancelReset} returnFocusRef={resetButton} showClose={false} footer={<View style={{flexDirection: 'row', gap: 18}}>
        <Button ref={cancelReset} focusId="reset-cancel" testID={`${testID}-reset-cancel`} variant="glass" onPress={() => closeReset(resetOpening)}>{defaultI18n.t('action.cancel')}</Button>
        <Button focusId="reset-confirm" testID={`${testID}-reset-submit`} variant="glass" style={{backgroundColor: `${theme.colors.record}24`}} textStyle={{color: theme.colors.record}} disabled={state.pending} onPress={() => confirmReset(resetOpening)}>{copy.reset}</Button>
      </View>}><View/></Dialog>
  </>;
}

export function HomeSavedViewChoices({views, hasMore, i18n, onChoose, firstRef, summary, disabled}: {disabled?: boolean; views: readonly PersonalResource[]; hasMore?: boolean; summary: (view: PersonalResource) => string | undefined; firstRef?: React.RefObject<View | null>; i18n: Pick<I18n, 't'>; onChoose: (view: PersonalResource) => void}) {
  const theme = useTheme();
  const eligible = views.filter(view => view.kind === 'view' && view.status === 'ready' && !view.onHome && view.actions.includes('pin'));
  return <ScrollView>{eligible.map((view, index) => {const detail = summary(view); return <Focusable ref={index === 0 ? firstRef : undefined} key={view.id} focusId={view.id} focusKind="row" disabled={disabled} onPress={() => onChoose(view)} style={{minHeight: theme.isTV ? 92 : undefined, paddingVertical: theme.isTV ? 12 : 20, paddingHorizontal: theme.isTV ? 20 : 0, flexDirection: 'row', alignItems: 'center', gap: 18}}>
      <View style={{width: theme.isTV ? 52 : 36, height: theme.isTV ? 52 : 36, borderRadius: theme.isTV ? 13 : 9, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.text}1A`}}><Icon name="i-filter" size={theme.isTV ? 28 : 20} color={theme.colors.textSecondary}/></View><View style={{flex: 1}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{view.name}</Text>
      {!!detail && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{detail}</Text>}</View>
    </Focusable>;})}{!eligible.length && !hasMore && <View style={{paddingVertical: 30, alignItems: 'center', gap: 8}}>
      <View style={{width: theme.isTV ? 52 : 44, height: theme.isTV ? 52 : 44, borderRadius: theme.isTV ? 14 : 11, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.text}1A`}}><Icon name="i-filter" size={theme.isTV ? 26 : 20} color={theme.colors.textSecondary}/></View>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{i18n.t('next.home.noViewsTitle')}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{i18n.t('next.home.noViewsBody')}</Text>
    </View>}</ScrollView>;
}
