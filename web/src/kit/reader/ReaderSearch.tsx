import {useId} from 'react';
import {Icon} from '../Icon';
import styles from './search.module.css';

export type ReaderSearchHit = Readonly<{
  id: string; chapter?: string; page?: string;
  /** Literal renderer result text, split into matching spans; never HTML from a book. */
  context: readonly Readonly<{text: string; match?: boolean}>[];
  /** Absent when the exact excerpt has no verified navigation location. */
  onNavigate?: () => void;
}>;
export type ReaderSearchProps = Readonly<{
  query: string; onQuery: (value: string) => void; hits: readonly ReaderSearchHit[];
  current?: string; loading: boolean; resultSummary?: string; onStep?: (direction: 'previous' | 'next') => void;
  onSearchAll?: () => void;
  copy: Readonly<{search: string; clear: string; move: string; wholeWords: string; searchAll: string}>;
}>;

/** desktop-read 8; phone-read 6. Searching and cancellation belong to the actual core. */
export function ReaderSearch({query, onQuery, hits, current, loading, resultSummary, onStep, onSearchAll, copy}: ReaderSearchProps) {
  const id = useId();
  return <div className={styles.search}>
    <div className={styles.field}><Icon name="i-search" size={22} /><input type="search" aria-label={copy.search} value={query} autoFocus
      autoComplete="off" spellCheck={false} onChange={event => onQuery(event.currentTarget.value)}
      onKeyDown={event => { if (event.nativeEvent.isComposing || !onStep || hits.length === 0 || !['ArrowUp', 'ArrowDown'].includes(event.key)) return; event.preventDefault(); onStep(event.key === 'ArrowUp' ? 'previous' : 'next'); }}
      aria-describedby={resultSummary ? `${id}-summary` : undefined} />
      {query && <button type="button" aria-label={copy.clear} onClick={() => onQuery('')}><Icon name="i-x" size={18} /></button>}
    </div>
    <div className={styles.summary} id={`${id}-summary`} role="status">{resultSummary}{onStep && hits.length > 0 && <span className={styles.keys} aria-hidden="true"><kbd>↑</kbd><kbd>↓</kbd>{copy.move}</span>}</div>
    <div className={styles.results} aria-busy={loading}>{hits.map(hit => <button key={hit.id} type="button" className={styles.hit} data-current={hit.id === current || undefined}
      aria-current={hit.id === current ? 'location' : undefined} disabled={!hit.onNavigate} onClick={hit.onNavigate}>
      {(hit.chapter || hit.page) && <span className={styles.where}>{hit.chapter && <span>{hit.chapter}</span>}{hit.page && <span>{hit.page}</span>}</span>}
      <p>{hit.context.map((span, index) => span.match ? <mark key={index}>{span.text}</mark> : span.text)}</p>
    </button>)}</div>
    <footer className={styles.footer}><span>{copy.wholeWords}</span>{onSearchAll && <button type="button" onClick={onSearchAll}>{copy.searchAll}<Icon name="i-right" size={16} /></button>}</footer>
  </div>;
}

export type ReaderBookmarkEntry = Readonly<{
  id: string; label: string; quote?: string; date?: string; onNavigate: () => void;
}>;
export function ReaderBookmarks({entries}: Readonly<{entries: readonly ReaderBookmarkEntry[]}>) {
  return <div className={styles.bookmarks}>{entries.map(entry => <button key={entry.id} type="button" className={styles.bookmark} onClick={entry.onNavigate}>
    <Icon name="i-bookmark-f" size={19} /><span><b>{entry.label}</b>{entry.quote && <small>{entry.quote}</small>}</span>{entry.date && <span className={styles.date}>{entry.date}</span>}
  </button>)}</div>;
}
