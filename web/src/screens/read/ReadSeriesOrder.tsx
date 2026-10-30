import {useId, useRef} from 'react';
import {defaultI18n} from '@i18n';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import type {ReadingLibraryItem} from './library-collection';
import styles from './series-order.module.css';

const {t} = defaultI18n;
/** An explicit owner plan over the original selected objects, never a loaded
 * series sample or an assertion that these are the current stored numbers. */
export function ReadSeriesOrder({items, order, onChange, disabled, comic, artwork}: Readonly<{
  items: readonly ReadingLibraryItem[]; order: readonly string[]; onChange: (order: readonly string[]) => void;
  disabled: boolean; comic: boolean; artwork: (item: ReadingLibraryItem) => ArtworkSource | undefined;
}>) {
  const heading = useId(), help = useId(), dragged = useRef<string | undefined>(undefined);
  const byId = new Map(items.map(item => [item.id, item]));
  if (order.length !== items.length || new Set(order).size !== items.length || order.some(id => !byId.has(id))) return null;
  const move = (id: string, target: number) => {
    if (disabled || !Number.isInteger(target) || target < 0 || target >= order.length) return;
    const from = order.indexOf(id); if (from < 0 || from === target) return;
    const next = [...order]; next.splice(from, 1); next.splice(target, 0, id); onChange(Object.freeze(next));
  };
  return <section className={styles.order} aria-labelledby={heading}>
    <h4 id={heading}>{t(comic ? 'next.reader.volumeOrder' : 'next.reader.bookOrder')}</h4>
    <ol>{order.map((id, index) => {
      const item = byId.get(id); if (!item) return null;
      return <li key={id} draggable={!disabled} onDragStart={event => {if (disabled) {event.preventDefault(); return;} dragged.current = id; event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', id);}}
        onDragEnd={() => {dragged.current = undefined;}} onDragOver={event => {if (!disabled && dragged.current) event.preventDefault();}}
        onDrop={event => {if (!disabled && dragged.current) {event.preventDefault(); move(dragged.current, index);} dragged.current = undefined;}}>
        <button type="button" disabled={disabled} aria-label={t('next.reader.moveSeriesTitle', {title: item.title, number: index + 1})} aria-describedby={help} aria-keyshortcuts="ArrowUp ArrowDown Home End"
          onKeyDown={event => {
            if (event.altKey || event.ctrlKey || event.metaKey) return;
            const target = event.key === 'ArrowUp' ? index - 1 : event.key === 'ArrowDown' ? index + 1 : event.key === 'Home' ? 0 : event.key === 'End' ? order.length - 1 : undefined;
            if (target !== undefined) {event.preventDefault(); move(id, target);}
          }}><Icon name="i-grip" size={18}/></button>
        <span className={styles.cover}><Artwork source={artwork(item)} alt="" fallback={<Icon name="i-book" size={20}/>}/></span>
        <b>{item.title}</b><span className={styles.number}>{defaultI18n.number(index + 1)}</span>
      </li>;
    })}</ol>
    <p id={help}>{t('next.reader.seriesOrderHelp', {title: byId.get(order[0])?.title ?? ''})}</p>
  </section>;
}
