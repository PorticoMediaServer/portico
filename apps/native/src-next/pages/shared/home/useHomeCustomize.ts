import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {findNodeHandle, TVEventHandler, type ScrollView, type View} from 'react-native';
import {Gesture} from 'react-native-gesture-handler';
import type {I18n} from '../../../../../../packages/i18n/src';
import type {PersonalResource} from '../../../../../../packages/client-core/src/personal-saved';
import {useCovered, useFocusRegistry} from '../../../kit/focus';
import {focusTarget} from '../../../kit/overlays/OverlayHost';
import {useTheme} from '../../../kit/theme';
import type {NativeHomeCustomizeController, NativeHomeResetOpening} from '../../../screens/home/customize-controller';
import {createTVKeyAdapter} from '../../../platform/tv-key-adapter';
import {tvKeySource} from '../../../platform/tv-key-source';
import type {HomeDragRow} from '../../../screens/home/customize-drag';
import type {NativeHomeCustomizeCopy} from '../../../screens/home/HomeCustomize';

export type HomeCustomizeProps = {
  controller: NativeHomeCustomizeController; copy: NativeHomeCustomizeCopy; i18n: Pick<I18n, 't' | 'has'>;
  onClose: () => void; onAddView: () => void; testID: string; summary: (view: PersonalResource) => string | undefined; notice?: string; noticeAction?: Readonly<{label: string; onPress: () => void}>;
  renderError?: (error: unknown, retry: () => void) => React.ReactNode;
  views?: Readonly<{items: readonly PersonalResource[]; loading: boolean; error?: unknown; hasMore: boolean; loadMore: () => void; renderError: (error: unknown, retry: () => void) => React.ReactNode; retry: () => void}>;
};

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


/** Customize Home (collections 16-20, tv-more): the layout draft, picking a row up and moving it (drag on touch, Up and Down on
 * TV), showing and hiding rows, adding and removing saved views, the reset confirmation, and where focus goes as rows change. */
export function useHomeCustomize({controller, copy, i18n, onClose, onAddView, views, summary, notice, noticeAction, renderError, testID}: HomeCustomizeProps) {
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
  /** Ruling C (step 7 item 4): a saved view that drops after an access change unmounts its row. On TV, focus that was
   * on it goes to the row now in its place, the row above, or Add view, so it stays in the panel. As the shared
   * replacement-refocus hook does: where focus was is read on the render that brings the change, while the old rows are
   * still attached, and only a capture in this panel on a dropped row counts; nothing moves while the panel is covered,
   * then or when the frame runs; and the frame moves nothing if focus has meanwhile attached somewhere else. */
  const covered = useCovered(), coveredNow = useRef(covered);
  coveredNow.current = covered;
  const shownRows = useRef(state.rows), focusBefore = useRef<ReturnType<typeof registry.capture>>(undefined);
  if (shownRows.current !== state.rows) focusBefore.current = theme.isTV ? registry.capture?.() : undefined;
  useEffect(() => {
    const before = shownRows.current, active = focusBefore.current;
    shownRows.current = state.rows;focusBefore.current = undefined;
    if (!theme.isTV || coveredNow.current || !controller.isCurrent() || !active || active.regionId !== regionId) return;
    const kept = (row: {id: string}) => state.rows.some(value => value.id === row.id);
    const on = (childId: string | undefined, row: {id: string}) => childId === row.id || !!childId?.startsWith(row.id + ':');
    const lost = before.findIndex(row => !kept(row) && on(active.childId, row));
    if (lost < 0) return;
    const neighbor = before.slice(lost + 1).find(kept) ?? before.slice(0, lost).reverse().find(kept);
    const elsewhere = () => {const now = registry.attachedFocus?.();return !!now && (now.regionId !== regionId || !before.some(row => !kept(row) && on(now.childId, row)));};
    if (elsewhere()) return;
    const frame = requestAnimationFrame(() => {
      if (!controller.isCurrent() || coveredNow.current || elsewhere()) return;
      focusTarget((neighbor && rowRefs.current.get(neighbor.id)) ?? addView.current);
    });
    return () => cancelAnimationFrame(frame);
  }, [controller, regionId, registry, state.rows, theme.isTV]);
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
  // The page's commands, so a form only wires them to its parts.
  const chooseView = (view: PersonalResource) => {if (!controller.addSavedView(view, theme.isTV)) return; if (theme.isTV) returning.current = true; setChoosing(false);};
  const keepEditing = () => {controller.keepEditing(); if (controller.isCurrent()) focusTarget(first.current ?? addView.current);};
  const retryError = () => {
    if (state.phase === 'error') {void controller.load(); return;}
    const saving = state.retry === 'save';
    void controller.retry().then(done => {if (done && saving && controller.isCurrent()) onClose();});
  };
  const startReset = () => {const opening = controller.captureReset(state.layout); if (opening) {resetOwner.current = opening; setReset(opening);}};
  const save = () => {void controller.save().then(saved => {if (saved) onClose();});};
  const removeRow = (id: string, index: number) => {
    const neighbor = state.rows[index + 1]?.id ?? state.rows[index - 1]?.id;
    if (controller.removeSavedView(id)) requestAnimationFrame(() => {if (controller.isCurrent()) focusTarget(neighbor ? rowRefs.current.get(neighbor) ?? addView.current : addView.current);});
  };
  const measure = (id: string, y: number, height: number) => {
    setHeights(before => {if (before.get(id) === height) return before; const next = new Map(before); next.set(id, height); return next;});
    setPositions(before => {if (before.get(id) === y) return before; const next = new Map(before); next.set(id, y); return next;});
  };
  return {chooseView, keepEditing, retryError, startReset, save, removeRow, measure, registry,state,addView,resetButton,rowRefs,first,reload,cancelReset,picked,scroller,returning,reset,setReset,choosing,setChoosing,resetOwner,resetOpening,closeReset,confirmReset,firstView,viewEntryFocused,heights,setHeights,positions,setPositions,regionId,blocked,returnToAdd,back,covered,coveredNow,shownRows,focusBefore,rowHeight,focusFrame,pickRow,rowsById,displayed,targetIndex,targetId,targetY,dragGeometry,scrollGesture};
}
