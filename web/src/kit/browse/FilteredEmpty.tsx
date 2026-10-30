import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button, Chip} from '../controls';
import styles from './browse.module.css';

const t = defaultI18n.t;
export type RelaxSuggestion = Readonly<{id: string; label: ReactNode; count?: number; onSelect: () => void}>;
export function RelaxSuggestions({items}: Readonly<{items: readonly RelaxSuggestion[]}>) {
  if (!items.length) return null;
  return <div className={styles.relaxSuggestions}><span>{t('next.browse.try')}</span>{items.map(item => <Chip variant="quick" key={item.id} count={item.count} onClick={item.onSelect}>{item.label}</Chip>)}</div>;
}
export type FilteredEmptyProps = Readonly<{description?: ReactNode; onClear: () => void; suggestions?: readonly RelaxSuggestion[]; className?: string}>;
export function FilteredEmpty({description, onClear, suggestions = [], className}: FilteredEmptyProps) {
  return <section className={[styles.filteredEmpty, className].filter(Boolean).join(' ')}>
    <div className={styles.ghostPosters} aria-hidden="true"><span/><span/><span/></div>
    <h2>{t('next.browse.noMatch')}</h2><p>{description}{description && ' '}{t('next.browse.emptyHelp')}</p>
    <Button variant="primary" size="hero" onClick={onClear}>{t('next.browse.clearFilters')}</Button>
    <RelaxSuggestions items={suggestions}/>
  </section>;
}
