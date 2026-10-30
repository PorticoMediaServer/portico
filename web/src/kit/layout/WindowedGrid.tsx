import {useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, cloneElement, type CSSProperties, type HTMLAttributes, type ReactElement, type ReactNode, type Ref} from 'react';
import type {WindowedCollection} from '@core/collections/windowed';
import {gridColumns, gridWindow, gridRenderIndices} from './geometry';
import {cancelLayoutScroll, scrollLayout, useLayoutMotion} from './motion';
import {RemovalFocusOwner} from './removal-focus-owner';
import {GridRemovalFocus} from './GridRemovalFocus';
import {useLayoutPresence} from './presence';
import {useGridExitPaint} from './grid-exit-paint';
import {boundedOutgoingPaint} from './retention';
import {RetainedLayoutPaint} from './retained-paint';
import {useAcceptedArrival} from '../browse/use-arrival';
import styles from './layout.module.css';

export type WindowedGridHandle = {scrollToIndex: (index: number, options?: {focus?: boolean}) => void};
export type GridItemContext = Readonly<{index: number; key: string; focused: boolean}>;
export type WindowedGridProps<T> = {
  collection: WindowedCollection<T>; renderItem: (item: T, context: GridItemContext) => ReactNode; renderPlaceholder: (index: number) => ReactNode;
  label: string; height: number | string; cardWidth: number; rowHeight: number; gap?: number; rowGap?: number; overscanRows?: number; stickyOffset?: number;
  /** Position once after this collection total and viewport are observed. */
  initialIndex?: number;
  /** One active pointer interaction may outlive its visible row window. */
  pinnedIndex?: number;
  onVisibleRange?: (first: number, last: number) => void; onItemFocus?: (item: T, index: number) => void; reducedMotion?: boolean;
  /** Opt-in original mounted paint only, bounded to40 visible outgoing slots. */
  retainRemovals?: boolean; paintScope?: object; empty?: ReactNode;
  ref?: Ref<WindowedGridHandle>; className?: string; style?: CSSProperties;
};

// Keep even million-item spans below the smallest supported browser's box limit.
const MAX_SCROLL_HEIGHT = 8_000_000;
type SlotProps = HTMLAttributes<HTMLDivElement> & {'data-grid-leaving'?: boolean};
type PaintSlot = Readonly<{key: string; node: ReactElement<SlotProps>; content: ReactNode}>;
const paintKey = (slot: PaintSlot) => slot.key;
const livePaintSlots = (root: HTMLElement): readonly HTMLElement[] => [...root.children].filter((node): node is HTMLElement => node instanceof HTMLElement && !!node.dataset.layoutItem && !node.hasAttribute('data-grid-leaving'));
const emptyPaint = (root: HTMLElement): readonly HTMLElement[] => root.hasChildNodes() ? [root] : [];
const reducedAt = (node: HTMLElement | null) => typeof document !== 'undefined' && (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
  || !!node?.closest('[data-reduce-motion="true"]') || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true');
export function WindowedGrid<T>({collection, renderItem, renderPlaceholder, label, height, cardWidth, rowHeight, gap = 20, rowGap = 30, overscanRows = 1, stickyOffset = 0,
  initialIndex, pinnedIndex, onVisibleRange, onItemFocus, reducedMotion = false, retainRemovals = false, paintScope, empty, ref, className, style}: WindowedGridProps<T>) {
  const initialPosition = useRef<WindowedCollection<T> | undefined>(undefined);
  const root = useRef<HTMLDivElement>(null), pendingFocus = useRef<number | null>(null), [viewport, setViewport] = useState({width: 0, height: 0, top: 0});
  const [focused, setFocused] = useState<string | null>(null), motion = useLayoutMotion(reducedMotion);
  const focusOwner = useMemo(() => { /* Preserve the original ownership/invalidation keys. */ void collection; void paintScope; return new RemovalFocusOwner(); }, [collection, paintScope]);
  useLayoutEffect(() => {pendingFocus.current = null;setFocused(null);}, [focusOwner]);
  const snapshot = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  const total = snapshot.total ?? collection.pageSize, columns = gridColumns(viewport.width, cardWidth, gap), pitch = rowHeight + rowGap;
  const logicalHeight = Math.max(0, Math.ceil(total / columns) * pitch - rowGap), scrollHeight = Math.min(MAX_SCROLL_HEIGHT, logicalHeight);
  const usableHeight = Math.max(1, viewport.height - stickyOffset), maxPhysical = Math.max(1, scrollHeight - usableHeight), maxLogical = Math.max(1, logicalHeight - usableHeight);
  const logicalTop = viewport.top * maxLogical / maxPhysical;
  const range = gridWindow(total, columns, pitch, logicalTop, usableHeight, overscanRows);
  const firstPage = collection.pageOf(range.renderFirst), lastPage = collection.pageOf(Math.max(range.renderFirst, range.renderLast));
  const subscribePages = useCallback((changed: () => void) => {
    const unsubscribers: (() => void)[] = [];
    for (let page = firstPage; page <= lastPage; page++) unsubscribers.push(collection.subscribePage(page, changed));
    return () => { for (const unsubscribe of unsubscribers) unsubscribe(); };
  }, [collection, firstPage, lastPage]);
  const pageVersion = useCallback(() => { let version = ''; for (let page = firstPage; page <= lastPage; page++) version += `${collection.getPageVersion(page)};`; return version; }, [collection, firstPage, lastPage]);
  const pages = useSyncExternalStore(subscribePages, pageVersion, pageVersion);
  useEffect(() => {
    const element = root.current; if (!element) return;
    const read = () => setViewport({width: element.clientWidth, height: element.clientHeight, top: element.scrollTop});
    const observer = new ResizeObserver(read); observer.observe(element); read();
    return () => { observer.disconnect(); cancelLayoutScroll(element); };
  }, []);
  useEffect(() => {
    if (range.renderLast < range.renderFirst) return;
    collection.ensureRange(range.renderFirst, range.renderLast);
    onVisibleRange?.(range.first, range.last);
  }, [collection, range.renderFirst, range.renderLast, range.first, range.last, onVisibleRange]);
  const jump = useCallback((index: number, focus = false, repeat = false) => {
    const element = root.current; if (!element || index < 0 || index >= total) return;
    if (focus) { pendingFocus.current = index; element.focus({preventScroll: true}); }
    const rowTop = Math.floor(index / columns) * pitch, rowBottom = rowTop + rowHeight;
    const target = focus && rowTop >= logicalTop && rowBottom <= logicalTop + usableHeight ? logicalTop : focus && rowBottom > logicalTop + usableHeight ? rowBottom - usableHeight : rowTop;
    // Alphabet/End jumps go straight to their sparse window instead of fetching every crossed page.
    const instant = motion.reduced || !focus || Math.abs(target - logicalTop) > usableHeight * 2;
    scrollLayout(element, 0, Math.min(maxPhysical, Math.max(0, target * maxPhysical / maxLogical)), instant, repeat);
    if (focus) setViewport(previous => ({...previous}));
  }, [total, columns, pitch, rowHeight, logicalTop, usableHeight, maxPhysical, maxLogical, motion.reduced]);
  useLayoutEffect(() => {
    if (initialPosition.current === collection || initialIndex === undefined || !Number.isSafeInteger(initialIndex)
      || initialIndex < 0 || snapshot.total === undefined || initialIndex >= snapshot.total || viewport.width <= 0 || viewport.height <= 0) return;
    initialPosition.current = collection; jump(initialIndex);
  }, [collection, initialIndex, snapshot.total, viewport.width, viewport.height, jump]);
  useImperativeHandle(ref, () => ({scrollToIndex: (index, options) => jump(index, options?.focus)}), [jump]);
  useLayoutEffect(() => {
    const index = pendingFocus.current; if (index === null) return;
    const slot = root.current?.querySelector<HTMLElement>(`[data-grid-index="${index}"]`);
    if (!slot?.dataset.itemId) return;
    const target = slot.querySelector<HTMLElement>('[data-layout-primary],button:not(:disabled),a[href],[tabindex="0"]') ?? slot;
    target.focus({preventScroll: true}); pendingFocus.current = null;
  }, [range.renderFirst, range.renderLast, pages, viewport]);
  const {contents, keys} = useMemo(() => { /* Preserve the original ownership/invalidation keys. */ void pages; void snapshot.generation;
    const contents = [], keys = new Set<string>();
    for (const index of gridRenderIndices(range.renderFirst, range.renderLast, total, pinnedIndex)) {
      const slot = collection.slotAt(index);
      if (slot.kind === 'item') keys.add(slot.key);
      const content = slot.kind === 'item' ? renderItem(slot.item, {index, key: slot.key, focused: slot.key === focused}) : renderPlaceholder(index);
      // Capture the original content and focus callback together. Pixel scrolling only
      // repositions their wrappers; a new page/generation/callback still rebuilds them.
      const onFocusCapture = () => {if (slot.kind === 'item') {setFocused(slot.key);onItemFocus?.(slot.item, index);}};
      contents.push({slot, index, content, onFocusCapture});
    }
    return {contents, keys};
    // The geometry below is a deliberate invalidator: the slots are rebuilt when the grid's shape changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [collection, pages, snapshot.generation, snapshot.total, total, pinnedIndex, range.renderFirst, range.renderLast,
    // Restore every original rare geometry invalidator; keep raw scroll (logicalTop/viewport.top) out.
    columns, cardWidth, rowHeight, gap, pitch, stickyOffset, focused, onItemFocus, renderItem, renderPlaceholder]);
  const items = useMemo(() => contents.map(({slot, index, content, onFocusCapture}): PaintSlot => {
    const row = Math.floor(index / columns), column = index % columns;
    // Keep the exact arithmetic order, including fractional/compressed scrolling.
    // The exact original React child and its Artwork/source/action props stay with this slot.
    const node = <div key={slot.key} className={styles.gridItem} role="listitem" aria-posinset={index + 1} aria-setsize={snapshot.total ?? -1}
      tabIndex={-1} data-grid-index={index} data-item-id={slot.kind === 'item' ? slot.key : undefined} data-layout-item={slot.kind === 'item' ? slot.key : undefined}
      style={{width: cardWidth, height: rowHeight, left: column * (cardWidth + gap), top: row * pitch - logicalTop + viewport.top + stickyOffset}}
      onFocusCapture={onFocusCapture}>
      <RetainedLayoutPaint.Provider value={false}>{content}</RetainedLayoutPaint.Provider>
    </div>;
    return {key: slot.key, node, content};
  }), [contents, snapshot.total, columns, cardWidth, rowHeight, gap, pitch, logicalTop, viewport.top, stickyOffset]);
  const paintOwner = useMemo(() => ({collection, paintScope}), [collection, paintScope]);
  // Scrolling, a different original source, or a layout replacement is not a removal animation.
  const paintWindow = useMemo(() => ({owner: paintOwner, width: viewport.width, height: viewport.height, top: viewport.top, cardWidth, rowHeight, gap, rowGap, stickyOffset}),
    [paintOwner, viewport.width, viewport.height, viewport.top, cardWidth, rowHeight, gap, rowGap, stickyOffset]);
  const accepted = snapshot.status === 'ready' && !snapshot.refreshing && snapshot.total !== undefined;
  const previousAnswer = useRef<{window: object; generation: number; extent: number} | undefined>(undefined);
  const reduced = motion.reduced || reducedAt(root.current);
  const presence = useLayoutPresence(items, paintKey, reduced, 180, retainRemovals ? 320 : 0, {scope: paintWindow, maxRetained: retainRemovals ? 40 : 0, staggerMs: 40, visibleOnly: true,
    admitNew: retainRemovals && accepted && previousAnswer.current?.window === paintWindow && previousAnswer.current.generation !== snapshot.generation}, root);
  useGridExitPaint(presence.root, presence.exits, paintOwner, reduced);
  // Only the incoming slots fade. The old generation's outgoing paint has its own lease.
  // Waiting for the retained tree's live answer prevents consuming a generation before its DOM.
  const arrival = retainRemovals && presence.ready ? snapshot : undefined;
  useAcceptedArrival(presence.root, arrival, paintOwner, livePaintSlots);
  const emptyRoot = useRef<HTMLDivElement | null>(null);
  useAcceptedArrival(emptyRoot, arrival, paintOwner, emptyPaint);
  const oldExtent = presence.leaving.size && previousAnswer.current?.window === paintWindow ? previousAnswer.current.extent : 0;
  useLayoutEffect(() => {if (accepted) previousAnswer.current = {window: paintWindow, generation: snapshot.generation, extent: Math.max(scrollHeight + stickyOffset, oldExtent)};},
    [accepted, paintWindow, snapshot.generation, scrollHeight, stickyOffset, oldExtent]);
  const incomingPaintKeys = new Set(items.map(paintKey));
  const incomingSlots = new Map(items.map(slot => [slot.key, slot]));
  const painted = retainRemovals ? boundedOutgoingPaint(presence.present, incomingPaintKeys, paintKey, presence.exits, presence.visible, 40) : items;
  // Keep the original viewport while its bounded outgoing rows fade, including total→0.
  const extentHeight = Math.max(scrollHeight + stickyOffset, retainRemovals ? oldExtent : 0, ...(retainRemovals ? painted.map(slot => {
    const box = slot.node.props.style;return Number(box?.top ?? 0) + Number(box?.height ?? 0);
  }) : []), retainRemovals && presence.leaving.size ? viewport.top + viewport.height : 0);
  return <GridRemovalFocus owner={focusOwner} root={root} generation={snapshot.generation} accepted={accepted} keys={keys} viewport={viewport}>
    <div ref={root} tabIndex={-1} className={[styles.grid, className].filter(Boolean).join(' ')} style={{height, ...style}} role="list" aria-label={label} aria-busy={snapshot.status === 'loading'}
    onScroll={event => { const element = event.currentTarget; setViewport({width: element.clientWidth, height: element.clientHeight, top: element.scrollTop}); }}
    onBlurCapture={event => { const next = event.relatedTarget as HTMLElement | null; if (!next || !event.currentTarget.contains(next) || !next.closest('[data-item-id]')) setFocused(null); }}
    onKeyDown={event => {
      if ((event.target as HTMLElement).closest('input,textarea,select,[contenteditable="true"]')) return;
      const slot = (event.target as HTMLElement).closest<HTMLElement>('[data-grid-index]'); if (!slot || slot.hasAttribute('data-grid-leaving')) return;
      const index = Number(slot.dataset.gridIndex);
      const delta = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : event.key === 'ArrowDown' ? columns : event.key === 'ArrowUp' ? -columns : 0;
      const target = event.key === 'Home' ? 0 : event.key === 'End' ? total - 1 : index + delta;
      if (!delta && event.key !== 'Home' && event.key !== 'End') return;
      if (target < 0 || target >= total) return;
      event.preventDefault(); jump(target, true, event.repeat);
    }}><div key={focusOwner.lifetime} ref={presence.root} className={styles.gridExtent} style={{height: extentHeight, minWidth: columns * cardWidth + (columns - 1) * gap}}>{painted.map(slot => {
        const incoming = incomingSlots.get(slot.key);
        if (!retainRemovals) return slot.node;
        // Surviving/returning rows expose only the real incoming props. Keep the
        // old position for this measurement commit, then FLIP to the new layout.
        if (incoming) return presence.ready ? incoming.node : cloneElement(incoming.node, {style: slot.node.props.style});
        return cloneElement(slot.node, {inert: true, 'aria-hidden': true, 'data-grid-leaving': true, onFocusCapture: undefined,
          children: <RetainedLayoutPaint.Provider value>{slot.content}</RetainedLayoutPaint.Provider>});
      })}</div>{retainRemovals && <div ref={emptyRoot} className={styles.gridEmpty} style={{top: viewport.top + stickyOffset}}>{snapshot.total === 0 && !presence.leaving.size ? empty : null}</div>}</div>
  </GridRemovalFocus>;
}
