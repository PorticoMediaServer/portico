import {Button, Chip} from '../controls';
import {Icon} from '../Icon';
import styles from './search.module.css';

export type SearchHistoryItem = Readonly<{query: string; onPress: () => void; remove?: Readonly<{label: string; onPress: () => void}>}>;
export type SearchHistoryProps = Readonly<{
  title: string;
  clear: Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
  items: readonly SearchHistoryItem[];
  /** Phone width (the shell's own width): rows rather than chips. The native part reads its form. */
  compact: boolean;
}>;

/** Recent searches (phone-app 7, desktop-search s-empty): the heading with Clear, then each search. At phone width they
 * are rows on one card with a clock and a remove ×; wider, chips on a line that wraps. Moved from `screens/search`; the
 * Search screen draws this same part. */
export function SearchHistory({title, clear, items, compact: phone}: SearchHistoryProps) {
  if (!items.length) return null;
  return <section className={styles.recent} data-compact={phone || undefined}>
    <header><h2>{title}</h2><Button variant="text" size="small" disabled={clear.disabled} onClick={clear.onPress}>{clear.label}</Button></header>
    <div className={phone ? styles.recentList : styles.recentChips}>{items.map(item => phone
      ? <div className={styles.recentItem} key={item.query}>
        <button className={styles.recentRow} type="button" onClick={item.onPress}><Icon name="i-clock" size={18}/><span>{item.query}</span></button>
        {item.remove && <Button variant="icon" size="xs" icon="i-x" iconOnly className={styles.recentRemove} aria-label={item.remove.label} onClick={item.remove.onPress}/>}
      </div>
      : <Chip key={item.query} icon="i-clock" onClick={item.onPress}>{item.query}</Chip>)}</div>
  </section>;
}
