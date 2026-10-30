import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Shelf, type ShelfProps} from '../../kit/layout';
import {Skeleton} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import styles from './browse.module.css';

type Slot<T> = Readonly<{kind: 'item'; value: T}> | Readonly<{kind: 'pending'}>;
export type PagedShelfProps<T> = ShelfProps<T> & Readonly<{
  hasMore: boolean;
  onMore: () => void;
  /** A serialized source may be loading another section; leave this cursor unclaimed. */
  loading?: boolean;
  /** A source cursor, never an item identity. Changes only after a real page. */
  continuation: string;
  cardWidth: number;
  artworkRatio: number;
}>;
/** A bounded server row. The sole trailing placeholder is never a selectable entity. */
export function PagedShelf<T>({items, getKey, renderItem, onItemFocus, onVisibleRange, hasMore, onMore, loading = false, continuation, cardWidth, artworkRatio, ...props}: PagedShelfProps<T>) {
  const requested = useRef<string | undefined>(undefined), root = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const element = root.current; if (!element) return;
    const observer = new IntersectionObserver(entries => setVisible(entries.some(entry => entry.isIntersecting)));
    observer.observe(element);return () => observer.disconnect();
  }, []);
  const slots = useMemo<readonly Slot<T>[]>(() => [...items.map(value => ({kind: 'item' as const, value})), ...(hasMore ? [{kind: 'pending' as const}] : [])], [items, hasMore]);
  const visibleRange = useCallback((first: number, last: number) => {
    const realLast = Math.min(last, items.length - 1);
    onVisibleRange?.(first < items.length ? first : -1, realLast);
    if (!visible || !hasMore || last < Math.max(0, items.length - 10)) { requested.current = undefined; return; }
    if (loading) return;
    if (requested.current === continuation) return;
    requested.current = continuation;
    onMore();
  }, [onVisibleRange, visible, hasMore, loading, items.length, continuation, onMore]);
  const phoneLink = props.platform?.startsWith('phone') && props.onSeeAll;
  return <div ref={root}><Shelf {...props} heading={phoneLink ? <button type="button" className={styles.shelfHeading} onClick={props.onSeeAll}>{props.heading}<Icon name="i-right" size={18}/></button> : props.heading}
    onSeeAll={phoneLink ? undefined : props.onSeeAll} items={slots} itemWidth={cardWidth} getKey={slot => slot.kind === 'item' ? JSON.stringify(['entry', getKey(slot.value)]) : 'pending-page'}
    onVisibleRange={visibleRange} onItemFocus={(slot, index) => { if (slot.kind === 'item') onItemFocus?.(slot.value, index); }}
    renderItem={(slot, context) => slot.kind === 'item' ? renderItem(slot.value, {...context, key: getKey(slot.value)}) :
      <div className={styles.pendingCard} data-row-pending aria-busy="true"><Skeleton width={cardWidth} height={cardWidth / artworkRatio}/></div>} /></div>;
}
