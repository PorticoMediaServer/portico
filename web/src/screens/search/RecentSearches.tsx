import {Button, Chip} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {t} from '../detail/text';
import styles from './search.module.css';

/** Controlled real history, shared by initial Search and states17a's miss. */
export function RecentSearches({queries, compact, onSelect, onClear, onRemove}: Readonly<{
  queries: readonly string[]; compact: boolean; onSelect(query: string): void; onClear(): void; onRemove?(query: string): void;
}>) {
  if (!queries.length) return null;
  return <section className={styles.recent} data-compact={compact || undefined}>
    <header><h2>{t('web.search.recent')}</h2><Button variant="text" size="small" onClick={onClear}>{t('web.search.clearRecent')}</Button></header>
    <div className={compact ? styles.recentList : undefined}>{queries.map(query => compact
      ? <div className={styles.recentItem} key={query}>
        <button className={styles.recentRow} type="button" onClick={() => onSelect(query)}><Icon name="i-clock" size={18}/><span>{query}</span></button>
        {onRemove && <Button variant="icon" size="xs" icon="i-x" iconOnly className={styles.recentRemove} aria-label={t('web.search.removeRecent', {query})} onClick={() => onRemove(query)}/>}
      </div>
      : <Chip key={query} icon="i-clock" onClick={() => onSelect(query)}>{query}</Chip>)}</div>
  </section>;
}
