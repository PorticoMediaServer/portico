import {useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type HTMLAttributes, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import {layoutType, platformType, type LayoutPlatform} from './Lockup';
import {anchoredOffset} from './geometry';
import {cancelLayoutScroll, scrollLayout, useLayoutMotion} from './motion';
import {useLayoutPresence} from './presence';
import {useListRemovalFocus} from './use-list-removal-focus';
import styles from './layout.module.css';

export type RowProps = Omit<HTMLAttributes<HTMLElement>, 'title'> & {heading: ReactNode; subtitle?: ReactNode; actions?: ReactNode; actionsPlacement?: 'trailing' | 'inline'; onSeeAll?: () => void; seeAllHref?: string; seeAllLabel?: string; platform?: LayoutPlatform; sizeClass?: 'medium'};
export function Row({heading, subtitle, actions, actionsPlacement = 'trailing', onSeeAll, seeAllHref, seeAllLabel = defaultI18n.t('next.layout.seeAll'), platform = 'desktop', sizeClass, children, className, ...props}: RowProps) {
  const id = useId(), type = platformType(platform);
  return <section {...props} className={[styles.row, className].filter(Boolean).join(' ')} data-layout-row data-platform={platform} data-size-class={sizeClass} aria-labelledby={id}>
    <header className={styles.rowHead}><h2 id={id} style={{...layoutType(type.rowHeading), ...(sizeClass === 'medium' ? {fontSize: 21} : {})}}>{heading}</h2>
      {subtitle && <span className={styles.rowSubtitle} style={layoutType(type.rowSubtitle)}>{subtitle}</span>}
      {actions && <div className={styles.rowActions} data-placement={actionsPlacement}>{actions}</div>}
      {seeAllHref ? <a className={styles.seeAll} href={seeAllHref} onClick={onSeeAll} style={layoutType(type.seeAll)}>{seeAllLabel}<Icon name="i-right" size={16} /></a>
        : onSeeAll && <button type="button" className={styles.seeAll} onClick={onSeeAll} style={layoutType(type.seeAll)}>{seeAllLabel}<Icon name="i-right" size={16} /></button>}
    </header>{children}
  </section>;
}

export type ShelfItemContext = Readonly<{index: number; focused: boolean; key: string}>;
export type ShelfProps<T> = Omit<RowProps, 'children'> & {
  /** Only the bounded row window returned by the server; never a whole-library array. */
  items: readonly T[]; getKey: (item: T) => string; renderItem: (item: T, context: ShelfItemContext) => ReactNode;
  onItemFocus?: (item: T, index: number) => void; onVisibleRange?: (first: number, last: number) => void;
  itemWidth?: number; reducedMotion?: boolean;
  /** Original consumer/source lifetime; replacement withdraws the old DOM focus context. */
  focusScope?: object;
};

export function Shelf<T>({items, getKey, renderItem, onItemFocus, onVisibleRange, itemWidth, focusScope, reducedMotion = false, platform = 'desktop', ...props}: ShelfProps<T>) {
  const [focusedKey, setFocusedKey] = useState<string | null>(null), [edges, setEdges] = useState({left: false, right: false});
  const motion = useLayoutMotion(reducedMotion);
  const focus = useListRemovalFocus(focusScope);
  const presence = useLayoutPresence(items, getKey, motion.reduced, 180, 320, focus), track = presence.root;
  const attach = useCallback((element: HTMLDivElement | null) => {focus.root.current = element;(track as {current: HTMLDivElement | null}).current = element;}, [focus.root, track]);
  useLayoutEffect(() => {setFocusedKey(null);}, [focus.lifetime]);
  const measure = useCallback(() => {
    const element = track.current; if (!element) return;
    const left = element.scrollLeft > 1, right = element.scrollLeft + element.clientWidth < element.scrollWidth - 1;
    setEdges(current => current.left === left && current.right === right ? current : {left, right});
    let first = -1, last = -1;
    const bounds = element.getBoundingClientRect();
    [...element.children].forEach(child => { const index = Number((child as HTMLElement).dataset.shelfIndex), rect = child.getBoundingClientRect(); if (index >= 0 && rect.right > bounds.left && rect.left < bounds.right) { if (first < 0) first = index; last = index; } });
    onVisibleRange?.(first, last);
  }, [onVisibleRange, track]);
  useEffect(() => {
    const element = track.current; if (!element) return;
    const observer = new ResizeObserver(measure); observer.observe(element); measure();
    return () => { observer.disconnect(); cancelLayoutScroll(element); };
  }, [measure, presence.present, track, focus.lifetime]);
  const step = (direction: number) => { const element = track.current; if (element) scrollLayout(element, Math.max(0, Math.min(element.scrollWidth - element.clientWidth, element.scrollLeft + direction * (element.clientWidth - (platform === 'tv' ? 296 : platform === 'desktop' ? 128 : 40)))), 0, motion.reduced); };
  return <Row {...props} platform={platform} data-reduced-motion={motion.reduced}>
    <div key={focus.lifetime} ref={attach} className={styles.track} tabIndex={-1} onScroll={measure} onKeyDown={event => {
      if ((event.target as HTMLElement).closest('input,textarea,select,[contenteditable="true"]')) return;
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      const element = track.current, item = (event.target as HTMLElement).closest<HTMLElement>('[data-shelf-index]');
      if (!element || !item) return;
      const index = Number(item.dataset.shelfIndex), next = index + (event.key === 'ArrowRight' ? 1 : -1), target = element.querySelector<HTMLElement>(`[data-shelf-index="${next}"]`);
      if (!target) return;
      const focusable = target.querySelector<HTMLElement>('[data-layout-primary],button:not(:disabled),a[href],[tabindex="0"]');
      if (!focusable) return;
      event.preventDefault(); focusable.focus({preventScroll: true});
      const inset = platform === 'tv' ? 200 : platform === 'desktop' ? 64 : props.sizeClass === 'medium' ? 28 : 20;
      scrollLayout(element, anchoredOffset(target.offsetLeft, target.offsetLeft + target.offsetWidth, element.clientWidth, element.scrollLeft, inset), 0, motion.reduced, event.repeat);
    }} onBlurCapture={event => { const next = event.relatedTarget as HTMLElement | null; if (!next || !event.currentTarget.contains(next) || !next.closest('[data-item-id]')) setFocusedKey(null); }}>
      {presence.present.map(item => { const key = getKey(item), index = items.findIndex(value => getKey(value) === key), leaving = presence.leaving.has(key); return <div key={key} className={styles.shelfItem} style={itemWidth ? {width: itemWidth} : undefined} data-layout-item={key} data-leaving={leaving || undefined} inert={leaving} aria-hidden={leaving || undefined} data-shelf-index={index} data-item-id={key}
        onFocusCapture={() => { setFocusedKey(key); onItemFocus?.(item, index); }}>
        {renderItem(item, {index, key, focused: focusedKey === key})}
      </div>; })}
    </div>
    {platform === 'desktop' && <><button type="button" className={`${styles.arrow} ${styles.arrowLeft}`} aria-label={defaultI18n.t('next.layout.previousItems')} disabled={!edges.left} onClick={() => step(-1)}><Icon name="i-left" size={28} /></button>
      <button type="button" className={`${styles.arrow} ${styles.arrowRight}`} aria-label={defaultI18n.t('next.layout.nextItems')} disabled={!edges.right} onClick={() => step(1)}><Icon name="i-right" size={28} /></button></>}
  </Row>;
}

export type FocusAnchorProps = HTMLAttributes<HTMLDivElement> & {reducedMotion?: boolean; inset?: number};
/** Browser counterpart of the native worklet: observe real DOM focus and keep its slot safe. */
export function FocusAnchor({reducedMotion = false, inset = 0, onFocusCapture, onKeyDownCapture, onKeyUpCapture, children, ...props}: FocusAnchorProps) {
  const motion = useLayoutMotion(reducedMotion), ref = useRef<HTMLDivElement>(null), repeating = useRef(false);
  useEffect(() => { const element = ref.current; return () => { if (element) cancelLayoutScroll(element); }; }, []);
  return <div {...props} ref={ref} onKeyDownCapture={event => { repeating.current = event.repeat; onKeyDownCapture?.(event); }} onKeyUpCapture={event => { repeating.current = false; onKeyUpCapture?.(event); }} onFocusCapture={event => {
    const element = event.currentTarget, target = event.target as HTMLElement;
    const outer = element.getBoundingClientRect(), inner = target.getBoundingClientRect();
    scrollLayout(element, anchoredOffset(inner.left - outer.left + element.scrollLeft, inner.right - outer.left + element.scrollLeft, element.clientWidth, element.scrollLeft, inset),
      anchoredOffset(inner.top - outer.top + element.scrollTop, inner.bottom - outer.top + element.scrollTop, element.clientHeight, element.scrollTop, inset), motion.reduced, repeating.current);
    onFocusCapture?.(event);
  }}>{children}</div>;
}

export type FlowSection = Readonly<{id: string; heading: ReactNode; count?: ReactNode; cap: number; itemWidth: number; itemCount: number; fullWidth?: boolean; grow?: boolean; content: ReactNode; onSeeAll?: () => void}>;
const sectionKey = (section: FlowSection) => section.id;
export function FlowSections({sections, className, reducedMotion = false, ...props}: Omit<HTMLAttributes<HTMLDivElement>, 'children'> & {sections: readonly FlowSection[]; reducedMotion?: boolean}) {
  const motion = useLayoutMotion(reducedMotion), presence = useLayoutPresence(sections, sectionKey, motion.reduced, 120, 180);
  return <div {...props} ref={presence.root} className={[styles.flow, className].filter(Boolean).join(' ')} data-reduced-motion={motion.reduced}>{presence.present.map(section => <section key={section.id} className={styles.flowSection} data-layout-item={section.id} data-leaving={presence.leaving.has(section.id) || undefined} inert={presence.leaving.has(section.id)} aria-hidden={presence.leaving.has(section.id) || undefined}
    style={{width: section.fullWidth ? '100%' : Math.min(section.cap, section.itemCount) * section.itemWidth + Math.max(0, Math.min(section.cap, section.itemCount) - 1) * 16, flexGrow: section.grow ? 1 : 0} as CSSProperties}>
    <header className={styles.flowHead}><h2>{section.heading}</h2>{section.count !== undefined && section.count !== null && <span>{section.count}</span>}{section.onSeeAll && <button type="button" onClick={section.onSeeAll}>{defaultI18n.t('next.layout.seeAll')}<Icon name="i-right" size={16} /></button>}</header>
    {section.content}
  </section>)}</div>;
}
